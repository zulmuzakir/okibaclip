package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"okibaclip/internal/capture"
	"okibaclip/internal/fsutil"
	"okibaclip/internal/history"
	"okibaclip/internal/paste"
	"okibaclip/internal/search"
	"okibaclip/internal/settings"
)

type origin int

const (
	fromHotkey origin = iota
	fromTray
)

var tabLabels = []string{"All", "Pinned", "Text", "Links", "Images", "Files", "Snippets"}
var tabFilters = []string{"all", "pinned", "text", "link", "image", "files"}

const (
	tabPinned   = 1
	tabSnippets = 6
)

// Folder choices of the snippet list besides the folders themselves.
const (
	allFolders = "All folders"
	noFolder   = "No folder"
)

// hotkeyRegistry is the global-shortcut service.
type hotkeyRegistry interface {
	Register(acc string, fn func()) error
	Unregister(acc string)
}

// paster sends the paste keystroke to the app the user came from.
type paster interface {
	Remember()
	Trusted(prompt bool) bool
	Paste(left int) error
}

// listLimit is the most history rows the panel lists.
const listLimit = 1000

// captureControl is what the app asks of the clipboard monitor.
type captureControl interface {
	Pause(d time.Duration)
	Resume()
	Paused() (bool, time.Time)
	IgnoreNext()
	Suppress(hash string)
	Unsuppress(hash string)
	SetConfig(s settings.Settings) error
}

type app struct {
	panel, settingsWin *mygo.Window
	tray               *mygo.Tray
	trayErr            string
	shortcut           string
	shortcutErr        string

	store   *history.Store
	mon     captureControl
	cfg     settings.Settings
	cfgPath string
	dataDir string
	ctx     context.Context
	cancel  context.CancelFunc
	src     capture.Source

	// View state, on the main thread.
	items         []history.Entry
	snippets      []history.Snippet
	query         string
	shown         search.Query // the query a.items and a.snippets answer, for highlights
	tab           int
	chosen        int
	selected      ui.Selection[int64]
	list          ui.ListState
	search        ui.Handle
	preview       history.Entry
	previewBitmap *ui.Bitmap
	previewStats  string // textStats of preview
	previewCode   bool   // preview's text looksLikeCode
	syntaxOpen    bool
	thumbs        map[int64]*ui.Bitmap
	gen           uint64
	previewGen    uint64
	toast         string
	toastUndo     []history.Entry
	hint          string
	footerNote    string
	snipList      ui.ListState
	chosenSnippet int
	editor        struct {
		open             bool
		id               int64
		name, body       string
		folder, shortcut string
		err              string
		saving           bool
	}
	edit struct {
		open   bool
		id     int64
		rename bool
		value  string
		err    string
		saving bool
	}
	snipFolders   []string
	snipFolder    string           // "" = all folders, noFolder, or a folder
	snipFolderSel string           // the select's label for snipFolder
	snipKeys      map[string]int64 // registered snippet accelerators -> snippet id
	appIcons      map[string]*ui.Bitmap
	listStale     bool   // the query or tab changed and its results haven't landed
	afterList     func() // runs once fresh results land
	pending       func() // chosen in a context menu, run after the list is built

	// Tray data, refreshed by refreshData.
	trayRecent, trayPinned []history.Entry
	traySnippets           []history.Snippet
	counts                 struct{ entries, pinned, snippets int }
	trayTimer              *time.Timer
	trayPaused             bool
	trayGen, statsGen      uint64

	quitting    bool
	waylandMode string
	// waylandCapture is set when wl-paste captures copies natively.
	waylandCapture bool
	pasteOK        bool
	pasteNote      string

	// Seams for tests.
	async          func(func())
	post           func(func())
	writeClipboard func(transfer.Data) error
	hotkeys        hotkeyRegistry
	paster         paster
	fileIcon       func(path string, size int) ([]byte, error)
	saveDialog     func(mygo.SaveDialogOptions) (string, error)

	settingsUI settingsState
}

func newApp() *app {
	a := &app{
		ctx:        context.Background(),
		thumbs:     map[int64]*ui.Bitmap{},
		appIcons:   map[string]*ui.Bitmap{},
		snipKeys:   map[string]int64{},
		src:        capture.SystemClipboard{},
		hotkeys:    mygo.GlobalShortcut,
		paster:     paste.System{},
		fileIcon:   mygo.Shell.FileIcon,
		saveDialog: mygo.Dialog.Save,
		cfg:        settings.Default(),
	}
	a.async = func(fn func()) { go fn() }
	a.post = func(fn func()) {
		if a.panel != nil {
			a.panel.Update(fn)
		} else {
			fn()
		}
	}
	a.writeClipboard = func(d transfer.Data) error { return mygo.Clipboard.Write(d) }
	return a
}

func (a *app) ready() {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		mygo.Dialog.Error(appName, "Cannot find the data folder: "+err.Error())
		mygo.App.Quit()
		return
	}
	a.dataDir = dir
	if err := os.MkdirAll(dir, 0o700); err == nil {
		if err := fsutil.Restrict(dir); err != nil {
			slog.Warn("restrict data folder", "err", err)
		}
	} else {
		mygo.Dialog.Error(appName, "Cannot create "+dir+": "+err.Error())
		mygo.App.Quit()
		return
	}
	a.cfgPath = filepath.Join(dir, "settings.json")
	cfg, err := settings.Load(a.cfgPath)
	if err != nil {
		slog.Warn("settings", "err", err)
	}
	a.cfg = cfg
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx, a.cancel = ctx, cancel
	a.store, err = history.Open(ctx, filepath.Join(dir, "history.db"))
	if err != nil {
		mygo.Dialog.Error(appName, "Cannot open history: "+err.Error())
		mygo.App.Quit()
		return
	}

	a.panel = mygo.NewWindow(mygo.WindowOptions{
		Title:         appName,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  14,
		Width:         720,
		Height:        480,
		DisableResize: true,
		Content:       ui.View(a.view),
	})
	a.panel.OnBlur(a.hide)
	a.panel.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.hide()
	})

	a.registerShortcut()
	a.setupTray()

	if a.waylandMode == "wayland" || a.waylandMode == "xwayland" {
		if src, err := capture.NewWaylandSource(ctx); err == nil {
			a.src, a.waylandCapture = src, true
		} else {
			slog.Info("wl-paste capture unavailable", "err", err)
		}
	}
	a.pasteOK, a.pasteNote = paste.Available(a.waylandMode)
	a.async(a.cleanImageFiles)
	m := capture.New(a.src, a.store, a.cfg, func() { a.post(a.changed) })
	a.mon = m
	go m.Run(ctx)

	shown := a.command(os.Args[1:], false)
	if !shown && !mygo.App.WasOpenedAtLogin() && (!a.cfg.FirstRunDone || a.trayErr != "") {
		a.show(fromHotkey)
		if !a.cfg.FirstRunDone {
			a.cfg.FirstRunDone = true
			if err := settings.Save(a.cfgPath, a.cfg); err != nil {
				slog.Error("save settings", "err", err)
			}
		}
	}
	a.refreshData()
	a.registerSnippetShortcuts()

	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.cancel()
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.store.Close()
	})
}

// ---- panel ----

func (a *app) show(o origin) {
	if a.panel == nil {
		return
	}
	a.placePanel(o)
	a.query, a.tab, a.chosen = "", 0, 0
	a.selected.Clear()
	a.toast, a.toastUndo = "", nil
	a.footerNote, a.hint = a.hint, ""
	a.paster.Remember()
	a.syntaxOpen = false
	a.requery()
	a.search.Focus()
	a.panel.Show()
}

func (a *app) placePanel(o origin) {
	if o == fromTray && a.tray != nil {
		anchor := a.tray.Bounds()
		if anchor.Width > 0 && anchor.Height > 0 {
			area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: anchor.X + anchor.Width/2, Y: anchor.Y + anchor.Height/2}).WorkArea
			b := a.panel.Bounds()
			x := anchor.X + (anchor.Width-b.Width)/2
			y := anchor.Y + anchor.Height + 6
			if y+b.Height > area.Y+area.Height {
				y = anchor.Y - b.Height - 6
			}
			x = max(area.X, min(x, area.X+max(0, area.Width-b.Width)))
			y = max(area.Y, min(y, area.Y+max(0, area.Height-b.Height)))
			a.panel.SetPosition(x, y)
			return
		}
		a.panel.Center()
		return
	}
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	w, h := a.panel.Size()
	a.panel.SetPosition(d.WorkArea.X+(d.WorkArea.Width-w)/2, d.WorkArea.Y+(d.WorkArea.Height-h)/3)
}

func (a *app) hide() {
	if a.panel == nil {
		return
	}
	a.panel.Hide()
}

func (a *app) toggle(o origin) {
	if a.panel == nil {
		return
	}
	if a.panel.IsVisible() {
		a.hide()
	} else {
		a.show(o)
	}
}

// reload runs the list query asynchronously and applies the newest result.
func (a *app) reload() {
	a.gen++
	gen, tab, folder := a.gen, a.tab, a.snipFolder
	q := search.Parse(a.query, time.Now())
	a.async(func() {
		var items []history.Entry
		var snips []history.Snippet
		var folders []string
		var err error
		if tab == tabSnippets {
			snips, err = a.store.Snippets(a.ctx, q.Terms)
			if err == nil {
				folders, err = a.store.SnippetFolders(a.ctx)
			}
			snips = inFolder(snips, folder)
		} else {
			items, err = a.store.List(a.ctx, history.Filter{Tab: tabFilters[tab], Query: q, Limit: listLimit})
		}
		if err != nil {
			if a.ctx.Err() == nil {
				slog.Error("list", "err", err)
			}
			a.post(func() {
				if gen == a.gen {
					a.listStale, a.afterList = false, nil
				}
			})
			return
		}
		a.post(func() {
			if gen != a.gen {
				return
			}
			a.items, a.snippets, a.shown = items, snips, q
			if tab == tabSnippets {
				a.snipFolders = folders
				if a.snipFolder != "" && a.snipFolder != noFolder && !slices.Contains(folders, a.snipFolder) {
					a.snipFolder, a.snipFolderSel = "", allFolders
					a.requery()
				}
			}
			keep := map[int64]*ui.Bitmap{}
			for _, e := range items {
				if b, ok := a.thumbs[e.ID]; ok {
					keep[e.ID] = b
				}
			}
			a.thumbs = keep
			a.chosen = max(0, min(a.chosen, len(items)-1))
			a.chosenSnippet = max(0, min(a.chosenSnippet, len(snips)-1))
			a.loadPreview()
			a.listStale = false
			if fn := a.afterList; fn != nil {
				a.afterList = nil
				fn()
			}
		})
	})
}

// requery reloads for a changed query or tab; until it lands, keyboard copies wait.
func (a *app) requery() { a.listStale = true; a.reload() }

// changed is called after the data changed: reload the list and tray data.
func (a *app) changed() {
	a.preview = history.Entry{}
	if a.panel == nil || a.panel.IsVisible() {
		a.reload()
	}
	a.refreshData()
	if a.settingsWin != nil {
		a.refreshStats()
	}
}

// refreshData reloads what the tray menu and tooltip show.
func (a *app) refreshData() {
	a.trayGen++
	gen := a.trayGen
	have, fileIcon := maps.Clone(a.appIcons), a.fileIcon // read here: the closure runs off the main thread
	a.async(func() {
		recent, err1 := a.store.Recent(a.ctx, 10)
		pinned, err2 := a.store.Pinned(a.ctx, 20)
		snips, err3 := a.store.Snippets(a.ctx, nil)
		e, p, s, err4 := a.store.Counts(a.ctx)
		if err := firstErr(err1, err2, err3, err4); err != nil {
			if a.ctx.Err() == nil {
				slog.Error("refresh", "err", err)
			}
			return
		}
		if len(snips) > 30 {
			snips = snips[:30]
		}
		icons := a.loadAppIcons(have, fileIcon)
		a.post(func() {
			if gen != a.trayGen {
				return
			}
			a.trayRecent, a.trayPinned, a.traySnippets = recent, pinned, snips
			a.appIcons = icons
			a.counts.entries, a.counts.pinned, a.counts.snippets = e, p, s
			a.refreshTray()
		})
	})
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// loadAppIcons looks up icons new source apps need, then returns every
// stored icon as a bitmap, reusing those in have. It runs off the main
// thread; DecodeBitmap only builds pixels (mygo ui/bitmap.go).
func (a *app) loadAppIcons(have map[string]*ui.Bitmap, fileIcon func(string, int) ([]byte, error)) map[string]*ui.Bitmap {
	missing, err := a.store.AppIconsMissing(a.ctx)
	if err != nil {
		slog.Warn("app icons", "err", err)
	}
	for _, m := range missing {
		png, err := fileIcon(m.Path, 32)
		if err != nil {
			png = nil
		}
		if err := a.store.SetAppIcon(a.ctx, m.App, png); err != nil {
			slog.Warn("app icon", "err", err)
		}
	}
	icons, err := a.store.AppIcons(a.ctx)
	if err != nil {
		slog.Warn("app icons", "err", err)
	}
	out := make(map[string]*ui.Bitmap, len(icons))
	for _, ic := range icons {
		if b, ok := have[ic.App]; ok {
			out[ic.App] = b
		} else if b, err := ui.DecodeBitmap(ic.Icon); err == nil {
			out[ic.App] = b
		}
	}
	return out
}

// inFolder keeps the snippets in the chosen folder: all for "", the ones
// without a folder for noFolder.
func inFolder(snips []history.Snippet, choice string) []history.Snippet {
	if choice == "" {
		return snips
	}
	want := choice
	if choice == noFolder {
		want = ""
	}
	out := snips[:0:0]
	for _, s := range snips {
		if s.Folder == want {
			out = append(out, s)
		}
	}
	return out
}

// loadPreview fetches the full chosen entry for the preview pane.
func (a *app) loadPreview() {
	a.previewGen++
	gen := a.previewGen
	if a.tab == tabSnippets || a.chosen < 0 || a.chosen >= len(a.items) {
		a.preview, a.previewBitmap, a.previewStats, a.previewCode = history.Entry{}, nil, "", false
		return
	}
	id := a.items[a.chosen].ID
	if a.preview.ID == id && (a.preview.Kind != history.KindImage || a.previewBitmap != nil) {
		return
	}
	a.async(func() {
		e, err := a.store.Get(a.ctx, id)
		if err != nil {
			return
		}
		// Text can be 1 MiB and the view rebuilds often, so count here.
		var stats string
		var code bool
		if e.Kind == history.KindText {
			stats, code = textStats(e.Text), looksLikeCode(e.Text)
		}
		var bmp *ui.Bitmap
		if e.Kind == history.KindImage {
			// DecodeBitmap only builds pixels and touches no window or GPU state (mygo ui/bitmap.go).
			bmp, _ = ui.DecodeBitmap(e.Image)
		}
		a.post(func() {
			if gen != a.previewGen {
				return
			}
			a.preview = e
			a.previewStats, a.previewCode = stats, code
			a.previewBitmap = bmp
		})
	})
}

// ---- commands & shortcut ----

// command applies command-line flags. It returns whether a window was
// shown. A second instance with no recognised flag shows the panel.
func (a *app) command(args []string, second bool) (shown bool) {
	recognised := false
	for _, arg := range args {
		switch arg {
		case "--toggle":
			recognised = true
			if a.panel != nil && !a.panel.IsVisible() {
				shown = true
			}
			a.toggle(fromHotkey)
		case "--show":
			recognised, shown = true, true
			a.show(fromHotkey)
		case "--hide":
			recognised = true
			a.hide()
		case "--settings":
			recognised, shown = true, true
			a.openSettings()
		case "--pause":
			recognised = true
			if a.mon != nil {
				a.mon.Pause(-1)
			}
		case "--resume":
			recognised = true
			if a.mon != nil {
				a.mon.Resume()
			}
		case "--quit":
			recognised = true
			mygo.App.Quit()
		}
	}
	if second && !recognised {
		a.show(fromHotkey)
		shown = true
	}
	return shown
}

var defaultShortcuts = []string{"Alt+Shift+V", "CmdOrCtrl+Alt+Shift+V"}

func (a *app) onHotkey() { a.post(func() { a.toggle(fromHotkey) }) }

func (a *app) registerShortcut() {
	cands := defaultShortcuts
	if a.cfg.Shortcut != "" {
		cands = []string{a.cfg.Shortcut}
	}
	a.shortcut, a.shortcutErr = "", ""
	for _, s := range cands {
		if err := a.hotkeys.Register(s, a.onHotkey); err != nil {
			slog.Warn("shortcut unavailable", "accelerator", s, "err", err)
			a.shortcutErr = err.Error()
			continue
		}
		a.shortcut, a.shortcutErr = s, ""
		return
	}
}

// applyShortcut replaces the global shortcut; on failure the old one stays.
func (a *app) applyShortcut(acc string) error {
	acc = strings.TrimSpace(acc)
	old := a.shortcut
	if old != "" {
		a.hotkeys.Unregister(old)
	}
	restore := func() {
		if old == "" {
			return
		}
		if err := a.hotkeys.Register(old, a.onHotkey); err != nil {
			slog.Warn("shortcut restore failed", "accelerator", old, "err", err)
			a.shortcut, a.shortcutErr = "", err.Error()
			return
		}
		a.shortcut = old
	}
	if acc == "" {
		prev := a.cfg.Shortcut
		a.cfg.Shortcut = ""
		a.registerShortcut()
		if a.shortcut == "" {
			a.cfg.Shortcut = prev
			failure := errors.New(a.shortcutErr)
			restore()
			return failure
		}
		return nil
	}
	if err := a.hotkeys.Register(acc, a.onHotkey); err != nil {
		restore()
		return err
	}
	a.shortcut, a.shortcutErr = acc, ""
	return nil
}

// ---- undo & shared helpers ----

func (a *app) undoDelete() {
	entries := a.toastUndo
	a.toastUndo = nil
	if len(entries) == 0 {
		return
	}
	a.async(func() {
		if err := a.store.Restore(a.ctx, entries); err != nil {
			slog.Error("restore", "err", err)
		}
		a.post(a.changed)
	})
}

// deleteEntries removes entries and offers Undo.
func (a *app) deleteEntries(ids []int64) {
	if len(ids) == 0 {
		return
	}
	a.async(func() {
		removed, err := a.store.Delete(a.ctx, ids...)
		if err != nil {
			slog.Error("delete", "err", err)
			return
		}
		a.post(func() {
			a.selected.Clear()
			a.toast = deletedMessage(len(removed))
			a.toastUndo = removed
			a.changed()
		})
	})
}

func deletedMessage(n int) string {
	if n == 1 {
		return "Entry deleted"
	}
	return fmt.Sprintf("%d entries deleted", n)
}

func (a *app) setPinned(id int64, pinned bool) {
	a.async(func() {
		if err := a.store.SetPinned(a.ctx, id, pinned); err != nil {
			slog.Error("pin", "err", err)
			return
		}
		a.post(a.changed)
	})
}

// confirmClear asks, then clears history except pinned entries.
func (a *app) confirmClear(parent *mygo.Window) {
	a.async(func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: parent, Type: mygo.MessageWarning, Title: "Clear History",
			Message:            "Clear clipboard history?",
			Detail:             "Pinned entries and snippets are kept.",
			Buttons:            []string{"Clear History", "Cancel"},
			DefaultButton:      1,
			CancelButton:       1,
			DestructiveButtons: []int{0},
		})
		if err != nil || res.Button != 0 {
			return
		}
		if _, err := a.store.Clear(a.ctx, true); err != nil {
			slog.Error("clear", "err", err)
			return
		}
		if err := a.store.Compact(a.ctx); err != nil {
			slog.Error("compact", "err", err)
		}
		a.post(a.changed)
	})
}

func (a *app) paused() (bool, time.Time) {
	if a.mon == nil {
		return false, time.Time{}
	}
	return a.mon.Paused()
}
