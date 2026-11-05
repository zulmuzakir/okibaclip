package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
	"okibaclip/internal/search"
	"okibaclip/internal/transform"
)

// clearPreview drops the preview pane's loaded entry and derived fields.
func (a *app) clearPreview() {
	a.preview, a.previewBitmap, a.previewStats, a.previewCode = history.Entry{}, nil, "", false
	a.previewBody, a.previewMore = "", 0
}

// truncateRunes returns the first n runes of s and how many runes follow them.
func truncateRunes(s string, n int) (head string, more int) {
	count := 0
	for i := range s {
		if count == n {
			for range s[i:] {
				more++
			}
			return s[:i], more
		}
		count++
	}
	return s, 0
}

func (a *app) thumb(e history.Entry) *ui.Bitmap {
	if b, ok := a.thumbs[e.ID]; ok {
		return b
	}
	var b *ui.Bitmap
	if len(e.Thumb) > 0 {
		b, _ = ui.DecodeBitmap(e.Thumb)
	}
	a.thumbs[e.ID] = b
	return b
}

// highlighted shows s with what the shown search matches in it in bold on
// hl.
func (a *app) highlighted(c *ui.Context, s string, hl ui.Color) ui.Element {
	rs := search.Highlight(s, a.shown.Terms)
	if len(rs) == 0 {
		return ui.Text(c, s)
	}
	spans := make([]ui.Span, 0, 2*len(rs)+1)
	at := 0
	for _, r := range rs {
		if r.Start > at {
			spans = append(spans, ui.Span{Text: s[at:r.Start]})
		}
		spans = append(spans, ui.Span{Text: s[r.Start:r.End], Weight: 700, Background: hl, Radius: 3})
		at = r.End
	}
	if at < len(s) {
		spans = append(spans, ui.Span{Text: s[at:]})
	}
	return ui.RichText(c, spans...)
}

// entryMenu fills e's menu, shared by its row's context menu and the preview's More actions button.
func (a *app) entryMenu(m *ui.Menu, e history.Entry) {
	ids := a.selectedIDs()
	if len(ids) > 1 && !a.selected.Has(e.ID) {
		ids = []int64{e.ID}
	}
	if len(ids) > 1 {
		if m.Item(fmt.Sprintf("Copy %d Merged", len(ids))).Chosen() {
			a.pending = func() { a.copyMerged(ids) }
		}
		if m.Item(fmt.Sprintf("Delete %d", len(ids))).Chosen() {
			a.pending = func() { a.deleteEntries(ids) }
		}
		m.Separator()
	}
	if m.Item("Copy").Chosen() {
		a.pending = func() { a.copyEntry(e, false, false) }
	}
	if m.Item("Copy as Plain Text").Chosen() {
		a.pending = func() { a.copyEntry(e, true, false) }
	}
	if e.Kind == history.KindText || e.Kind == history.KindLink || e.Kind == history.KindColor {
		m.Submenu("Transform", func(m *ui.Menu) {
			for _, tr := range transform.All {
				if m.Item(tr.Name).Chosen() {
					a.pending = func() { a.copyTransformed(e, tr) }
				}
			}
		})
	}
	if e.Kind == history.KindText || e.Kind == history.KindLink || e.Kind == history.KindColor {
		if m.Item("Edit…").Chosen() {
			a.pending = func() { a.openEntryEdit(e, false) }
		}
	}
	label := "Pin"
	if e.Pinned {
		label = "Unpin"
	}
	if m.Item(label).Chosen() {
		a.pending = func() { a.setPinned(e.ID, !e.Pinned) }
	}
	if e.Pinned && m.Item("Rename…").Chosen() {
		a.pending = func() { a.openEntryEdit(e, true) }
	}
	if e.Kind == history.KindImage {
		if m.Item("Save Image As…").Chosen() {
			a.pending = func() { a.saveImage(e) }
		}
		if m.Item("Copy as File").Chosen() {
			a.pending = func() { a.copyImageFile(e) }
		}
	}
	if e.Kind != history.KindImage && m.Item("Save as Snippet…").Chosen() {
		a.pending = func() { a.saveAsSnippet(e) }
	}
	if e.Kind == history.KindLink && m.Item("Open Link").Chosen() {
		a.pending = func() { openLink(strings.TrimSpace(e.Text)) }
	}
	if e.Kind == history.KindFiles && m.Item("Show in Folder").Chosen() {
		a.pending = func() { showInFolder(strings.Split(e.Text, "\n")[0]) }
	}
	m.Separator()
	if m.Item("Delete").Chosen() {
		a.pending = func() { a.deleteEntries([]int64{e.ID}) }
	}
}

func openLink(u string) {
	if history.TextKind(u) != history.KindLink {
		slog.Warn("open link: not an http(s) URL")
		return
	}
	if err := mygo.Shell.OpenExternal(u); err != nil {
		slog.Error("open link", "err", err)
	}
}

func showInFolder(p string) { mygo.Shell.ShowItemInFolder(p) }

func (a *app) previewPane(c *ui.Context) {
	t := c.Theme()
	e, ok := a.chosenEntry()
	if !ok {
		return
	}
	if a.preview.ID == e.ID {
		e = a.preview
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, kindIcon(e.Kind)).FontSize(14).TextColor(t.TextMuted)
		ui.Text(c, kindTitle(e)).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
		pin, pinFg := "Pin", t.TextMuted
		if e.Pinned {
			pin, pinFg = "Unpin", t.Accent
		}
		iconButton(c, iconPin, pin, pin+" ("+cmdSym()+"P)", pinFg).OnClick(func() { a.setPinned(e.ID, !e.Pinned) })
		iconButton(c, iconMore, "More actions", "More actions", t.TextMuted).Menu(func(m *ui.Menu) { a.entryMenu(m, e) })
		ui.PrimaryButton(c, "Copy").Tooltip("Copy (↵)").OnClick(func() { a.copyEntry(e, false, false) })
	})
	hl := t.Accent.Alpha(0.22)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			switch e.Kind {
			case history.KindImage:
				if a.preview.ID == e.ID && a.previewBitmap != nil {
					ui.Image(c, a.previewBitmap).Fit(ui.Contain).Height(300)
				} else if b := a.thumb(e); b != nil {
					ui.Image(c, b).Fit(ui.Contain).Height(300)
				}
			case history.KindFiles:
				for _, p := range strings.Split(e.Text, "\n") {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						a.highlighted(c, p, hl).Wrap().Grow(1).Selectable()
						pillButton(c, iconFolder, "Show in Folder", t.Text).OnClick(func() { showInFolder(p) })
					})
				}
			case history.KindColor:
				// ui.Hex panics on bad input, and imports can carry an invalid color.
				if history.TextKind(e.Text) != history.KindColor {
					a.textBody(c, e, hl)
					break
				}
				col := ui.Hex(strings.TrimSpace(e.Text))
				hex, rgb := colorValues(col)
				ui.Box(c).FillWidth().Height(120).Radius(10).Background(col).Border(1, t.Text.Alpha(0.12))
				ui.Text(c, hex).Font("monospace").FontSize(13).Selectable()
				ui.Text(c, rgb).Font("monospace").FontSize(13).Selectable()
			default:
				a.textBody(c, e, hl)
			}
		})
	})
	stats := ""
	if a.preview.ID == e.ID {
		stats = a.previewStats
	}
	ui.Text(c, details(e, stats, time.Now())).FontSize(11).TextColor(t.TextMuted).Wrap()
	// A More actions choice is reported while this pane builds, after the
	// list already ran its pending work.
	a.runPending()
}

// textBody shows e's text, capped, in monospace when it reads as code, and
// an Open Link button for links.
func (a *app) textBody(c *ui.Context, e history.Entry, hl ui.Color) {
	var text string
	var more int
	if a.preview.ID == e.ID {
		text, more = a.previewBody, a.previewMore
	} else {
		text, more = truncateRunes(e.Text, 20000)
	}
	if more > 0 {
		text += fmt.Sprintf("… (%d more characters)", more)
	}
	h := a.highlighted(c, text, hl)
	if a.preview.ID == e.ID && a.previewCode {
		h.Font("monospace").FontSize(12)
	}
	h.Wrap().Selectable()
	if e.Kind == history.KindLink {
		ui.Row(c).Children(func() {
			if pillButton(c, iconExternal, "Open Link", c.Theme().Accent).Clicked() {
				openLink(strings.TrimSpace(e.Text))
			}
		})
	}
}
