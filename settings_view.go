package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
	"okibaclip/internal/settings"
)

type settingsState struct {
	tab        int
	form       settings.Settings
	maxEntries float64
	retention  float64
	imageMB    float64
	storageMB  float64
	ignoreApps string
	recentApps []history.SourceApp
	poll       string
	patterns   string
	patErr     string
	shortcut   string
	shortcutOK string
	shortErr   string
	login      bool
	loginErr   string
	toast      string
	stats      string
	toggleCmd  string
}

var settingsTabs = []string{"General", "History", "Data", "About"}

var settingsIcons = []*ui.SVG{iconSettings, iconHistory, iconDatabase, iconInfo}

func pollLabel(ms int) string { return fmt.Sprintf("%d ms", ms) }

// pollChoices are the labels of settings.PollIntervals.
var pollChoices = func() []string {
	var out []string
	for _, ms := range settings.PollIntervals {
		out = append(out, pollLabel(ms))
	}
	return out
}()

// syncSettingsUI copies the saved settings into the form.
func (a *app) syncSettingsUI() {
	s := &a.settingsUI
	s.form = a.cfg
	s.maxEntries, s.retention, s.imageMB = float64(a.cfg.MaxEntries), float64(a.cfg.RetentionDays), float64(a.cfg.MaxImageMB)
	s.storageMB = float64(a.cfg.MaxStorageMB)
	s.ignoreApps = strings.Join(a.cfg.IgnoreApps, "\n")
	s.poll = pollLabel(a.cfg.PollMillis)
	s.patterns = strings.Join(a.cfg.IgnorePatterns, "\n")
	s.patErr = ""
	s.shortcut = a.shortcut
	if a.cfg.Shortcut != "" {
		s.shortcut = a.cfg.Shortcut
	}
	s.shortErr = ""
	s.login = mygo.App.OpenAtLogin()
	if isLinux {
		exe, err := os.Executable()
		if err != nil {
			exe = "okibaclip"
		}
		s.toggleCmd = shellQuote(exe) + " --toggle"
	}
}

// shellQuote quotes s for a POSIX shell unless it is already safe.
func shellQuote(s string) string {
	safe := s != ""
	for i := range len(s) {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("_./+-", b) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (a *app) openSettings() {
	if a.panel == nil {
		return
	}
	if a.settingsWin == nil {
		a.settingsWin = mygo.NewWindow(mygo.WindowOptions{
			Title:    appName + " Settings",
			Width:    560,
			Height:   640,
			StateKey: "settings",
			Content:  ui.View(a.settingsView),
		})
		a.settingsWin.OnClose(func(e *mygo.CloseEvent) {
			if a.quitting {
				return
			}
			e.PreventDefault()
			a.settingsWin.Hide()
		})
	}
	a.hide()
	a.syncSettingsUI()
	a.refreshStats()
	a.settingsWin.Show()
}

func (a *app) refreshStats() {
	a.statsGen++
	gen := a.statsGen
	a.async(func() {
		e, p, s, err := a.store.Counts(a.ctx)
		if err != nil {
			return
		}
		u, err := a.store.Usage(a.ctx)
		if err != nil {
			return
		}
		apps, err := a.store.SourceApps(a.ctx, 8)
		if err != nil {
			return
		}
		size := humanSize(fileSize(filepath.Join(a.dataDir, "history.db")))
		limit := humanSize(int64(a.cfg.MaxStorageMB) << 20)
		a.postSettings(func() {
			if gen != a.statsGen {
				return
			}
			a.settingsUI.recentApps = apps
			a.settingsUI.stats = fmt.Sprintf("%d entries · %d pinned · %d snippets · %s of %s used (images %s) · file %s", e, p, s, humanSize(u.Bytes), limit, humanSize(u.ImageBytes), size)
		})
	})
}

// applySettings validates the form, saves it and pushes it to the monitor.
func (a *app) applySettings() {
	s := &a.settingsUI
	f := s.form
	f.MaxEntries, f.RetentionDays, f.MaxImageMB = int(s.maxEntries), int(s.retention), int(s.imageMB)
	f.MaxStorageMB = int(s.storageMB)
	f.IgnoreApps = strings.Split(s.ignoreApps, "\n")
	for _, ms := range settings.PollIntervals {
		if pollLabel(ms) == s.poll {
			f.PollMillis = ms
		}
	}
	lines := strings.Split(s.patterns, "\n")
	if _, err := settings.CompilePatterns(lines); err != nil {
		s.patErr = patternErrText(err)
		f.IgnorePatterns = a.cfg.IgnorePatterns
	} else {
		s.patErr = ""
		f.IgnorePatterns = lines
	}
	f.Shortcut = a.cfg.Shortcut
	f.FirstRunDone = a.cfg.FirstRunDone
	f.Normalize()
	s.form = f
	s.maxEntries, s.retention, s.imageMB = float64(f.MaxEntries), float64(f.RetentionDays), float64(f.MaxImageMB)
	s.storageMB = float64(f.MaxStorageMB)
	a.cfg = f
	if a.cfgPath != "" {
		if err := settings.Save(a.cfgPath, f); err != nil {
			s.toast = "Could not save settings: " + err.Error()
		}
	}
	if a.mon != nil {
		if err := a.mon.SetConfig(f); err != nil {
			s.patErr = patternErrText(err)
		}
	}
	a.refreshTray()
	a.reload()
}

func (a *app) saveShortcut(acc string) {
	s := &a.settingsUI
	prev := a.cfg.Shortcut
	if err := a.applyShortcut(acc); err != nil {
		s.shortErr = err.Error()
		a.cfg.Shortcut = prev
		return
	}
	s.shortErr = ""
	a.cfg.Shortcut = strings.TrimSpace(acc)
	s.form.Shortcut = a.cfg.Shortcut
	if a.cfgPath != "" {
		if err := settings.Save(a.cfgPath, a.cfg); err != nil {
			s.shortErr = "Could not save settings: " + err.Error()
		}
	}
	s.shortcut = a.shortcut
	a.refreshTray()
}

func (a *app) settingsView(c *ui.Context) {
	s := &a.settingsUI
	if s.toast != "" {
		c.Toast(s.toast)
		s.toast = ""
	}
	c.OnShortcut(ui.Cmd, ui.KeyW, func() { a.settingsWin.Hide() })
	ui.Column(c).Fill().Children(func() {
		tabs := ui.TabsBase(c, &s.tab, len(settingsTabs))
		tabs.List.FillWidth().Padding(14, 16, 10, 16).Gap(6).AlignItems(ui.Center).Children(func() {
			for i, label := range settingsTabs {
				pillTab(c, tabs.Tab(i), i == s.tab, settingsIcons[i], label)
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(18).Padding(6, 16, 20, 16).Children(func() {
				switch s.tab {
				case 0:
					a.generalSection(c)
				case 1:
					a.historySection(c)
				case 2:
					a.dataSection(c)
				case 3:
					a.aboutSection(c)
				}
			})
		})
	})
}

// settingsGroup is a titled card of settings.
func settingsGroup(c *ui.Context, title string, fn func()) {
	t := c.Theme()
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(t.TextMuted).Margin(0, 0, 0, 4)
		ui.Column(c).Padding(4, 14).Radius(12).Background(t.Text.Alpha(0.04)).Children(fn)
	})
}

// settingRow is one setting: its name, help and error stacked on the
// left, the controls fn builds on the right; fn nil builds none. Empty
// help or err shows nothing.
func settingRow(c *ui.Context, label, help, err string, fn func()) {
	t := c.Theme()
	ui.Row(c).Padding(10, 0).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(13)
			if help != "" {
				ui.Text(c, help).FontSize(11).TextColor(t.TextMuted).Wrap()
			}
			if err != "" {
				ui.Text(c, err).FontSize(11).TextColor(t.Danger).Wrap()
			}
		})
		if fn != nil {
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(fn)
		}
	})
}

// settingBlock is a setting whose controls take the card's width below
// its name: text areas and rows of several controls.
func settingBlock(c *ui.Context, label, help, err string, fn func()) {
	t := c.Theme()
	ui.Column(c).Padding(10, 0).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(13)
		fn()
		if help != "" {
			ui.Text(c, help).FontSize(11).TextColor(t.TextMuted).Wrap()
		}
		if err != "" {
			ui.Text(c, err).FontSize(11).TextColor(t.Danger).Wrap()
		}
	})
}

func (a *app) generalSection(c *ui.Context) {
	s := &a.settingsUI
	settingsGroup(c, "Startup", func() {
		settingRow(c, "Open at login", "", s.loginErr, func() {
			if ui.Switch(c, &s.login).Label("Open at login").Changed() {
				if err := mygo.App.SetOpenAtLogin(s.login); err != nil {
					s.login = !s.login
					s.loginErr = err.Error()
				} else {
					s.loginErr = ""
					a.refreshTray()
				}
			}
		})
	})
	settingsGroup(c, "Shortcut", func() {
		shortErr := s.shortErr
		if shortErr == "" && a.shortcut == "" && a.shortcutErr != "" {
			shortErr = "No shortcut registered: " + a.shortcutErr
		}
		settingBlock(c, "Global shortcut", "Format: CmdOrCtrl+Shift+V. Modifiers: CmdOrCtrl, Cmd, Ctrl, Alt, Shift, Super.", shortErr, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &s.shortcut).Label("Global shortcut").Grow(1)
				if ui.Button(c, "Apply").Clicked() {
					a.saveShortcut(s.shortcut)
				}
				if ui.Button(c, "Reset").Clicked() {
					a.saveShortcut("")
				}
			})
		})
		if isLinux {
			settingBlock(c, "Desktop shortcut", "On Wayland desktops global shortcuts may not reach the app. Add a custom shortcut in your desktop settings that runs this command.", "", func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.toggleCmd).Font("monospace").FontSize(12).Wrap().Selectable().Grow(1).MinWidth(0)
					if ui.Button(c, "Copy Command").Clicked() {
						cmd := s.toggleCmd
						if err := a.write(textHash(cmd), textData(cmd, "")); err != nil {
							c.Toast("Copy failed: " + err.Error())
						} else {
							c.Toast("Command copied")
						}
					}
				})
			})
		}
	})
	settingsGroup(c, "Pasting", func() {
		msg := "Sends " + pasteKey() + " to the app you were in after you choose an entry."
		if a.pasteNote != "" {
			msg += " " + a.pasteNote
		}
		settingRow(c, "Paste after choosing", msg, "", func() {
			sw := ui.Switch(c, &s.form.PasteAfterCopy).Label("Paste after choosing").Disabled(!a.pasteOK)
			if sw.Changed() {
				if s.form.PasteAfterCopy && isDarwin {
					a.paster.Trusted(true)
				}
				a.applySettings()
			}
		})
		if isDarwin && s.form.PasteAfterCopy && !a.paster.Trusted(false) {
			settingRow(c, "Accessibility access", "", "Allow "+appName+" in System Settings › Privacy & Security › Accessibility", func() {
				if ui.Button(c, "Open Settings").Clicked() {
					if err := mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"); err != nil {
						s.toast = err.Error()
					}
				}
			})
		}
	})
}

func (a *app) historySection(c *ui.Context) {
	s := &a.settingsUI
	sw := func(label, help string, v *bool) {
		settingRow(c, label, help, "", func() {
			if ui.Switch(c, v).Label(label).Changed() {
				a.applySettings()
			}
		})
	}
	settingsGroup(c, "Limits", func() {
		settingRow(c, "Max entries", "", "", func() {
			if ui.NumberInput(c, &s.maxEntries, 50, 5000, 50).Label("Max entries").Width(140).Changed() {
				a.applySettings()
			}
		})
		settingRow(c, "Keep for days", "0 keeps forever", "", func() {
			if ui.NumberInput(c, &s.retention, 0, 3650, 1).Label("Keep for days").Width(140).Changed() {
				a.applySettings()
			}
		})
		settingRow(c, "Storage limit (MB)", "Oldest unpinned entries are removed when history passes this size. Pinned entries count toward it but are never removed.", "", func() {
			if ui.NumberInput(c, &s.storageMB, 100, 20480, 100).Label("Storage limit (MB)").Width(140).Changed() {
				a.applySettings()
			}
		})
		settingRow(c, "Max image size (MB)", "", "", func() {
			if ui.NumberInput(c, &s.imageMB, 1, 50, 1).Label("Max image size (MB)").Width(140).Changed() {
				a.applySettings()
			}
		})
	})
	settingsGroup(c, "Capture", func() {
		sw("Capture images", "", &s.form.CaptureImages)
		sw("Capture files", "", &s.form.CaptureFiles)
		sw("Skip passwords & concealed copies", "", &s.form.SkipSensitive)
		settingRow(c, "Check clipboard every", "", "", func() {
			if ui.Select(c, &s.poll, pollChoices).Label("Check clipboard every").Width(140).Changed() {
				a.applySettings()
			}
		})
	})
	settingsGroup(c, "Behavior", func() {
		sw("Move copied entry to top", "", &s.form.MoveToTopOnCopy)
	})
	settingsGroup(c, "Ignore", func() {
		settingBlock(c, "Ignore patterns", "One regular expression per line. Text matching one entirely is not recorded.", s.patErr, func() {
			if ui.TextArea(c, &s.patterns).Label("Ignore patterns").FillWidth().Height(90).Changed() {
				a.applySettings()
			}
		})
		settingBlock(c, "Ignore apps", "One per line: bundle ID or app name on macOS (com.agilebits.onepassword7), executable on Windows (keepass.exe), window class on Linux X11. Copies from these apps are not recorded.", "", func() {
			if ui.TextArea(c, &s.ignoreApps).Label("Ignore apps").FillWidth().Height(70).Changed() {
				a.applySettings()
			}
			if len(s.recentApps) > 0 {
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for _, app := range s.recentApps {
						name := app.Name
						if name == "" {
							name = app.App
						}
						if pillButton(c, iconPlus, name, c.Theme().Text).Clicked() {
							a.ignoreApp(app.App)
						}
					}
				})
			}
		})
	})
}

func (a *app) dataSection(c *ui.Context) {
	s := &a.settingsUI
	settingsGroup(c, "Backup", func() {
		settingRow(c, "Export", "Save history and snippets to a file.", "", func() {
			if ui.Button(c, "Export…").Clicked() {
				a.exportData()
			}
		})
		settingRow(c, "Import", "Add entries and snippets from an export file.", "", func() {
			if ui.Button(c, "Import…").Clicked() {
				a.importData()
			}
		})
	})
	settingsGroup(c, "Storage", func() {
		settingRow(c, "Data folder", a.dataDir, "", func() {
			if ui.Button(c, "Open Folder").Clicked() {
				if err := mygo.Shell.OpenPath(a.dataDir); err != nil {
					s.toast = err.Error()
				}
			}
		})
		settingRow(c, "Usage", s.stats, "", nil)
	})
	settingsGroup(c, "Clear history", func() {
		settingRow(c, "Clear history", "Deletes every unpinned entry. Pinned entries and snippets are kept.", "", func() {
			if ui.Button(c, "").Label("Clear History…").Children(func() {
				ui.Text(c, "Clear History…").TextColor(c.Theme().Danger)
			}).Clicked() {
				a.confirmClear(a.settingsWin)
			}
		})
	})
}

func (a *app) aboutSection(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(16).Gap(14).AlignItems(ui.Center).Radius(12).Background(t.Text.Alpha(0.04)).Children(func() {
		ui.Box(c).Size(48, 48).Radius(12).Background(t.Accent.Alpha(0.18)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, iconClipboard).Size(26, 26).TextColor(t.Accent)
		})
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, appName).Bold().FontSize(16)
			ui.Text(c, "Version "+mygo.App.Version()).FontSize(12).TextColor(t.TextMuted)
		})
	})
	settingsGroup(c, "Status", func() {
		v, col := "Running", t.TextMuted
		if a.trayErr != "" {
			v, col = a.trayErr, t.Danger
		} else if a.tray == nil {
			v = "Not started"
		}
		settingRow(c, "Tray icon", "", "", func() {
			ui.Text(c, v).FontSize(12).TextColor(col)
		})
		if a.waylandCapture {
			settingRow(c, "Clipboard", "", "", func() {
				ui.Text(c, "wl-paste (Wayland data-control)").FontSize(12).TextColor(t.TextMuted)
			})
		}
		if a.waylandMode == "wayland" && !a.waylandCapture {
			settingRow(c, "Clipboard", "Copies are recorded only while an "+appName+" window is focused (native Wayland).", "", nil)
		}
	})
	if isDarwin {
		settingsGroup(c, "Permissions", func() {
			settingRow(c, "Paste from Other Apps", "If macOS asks, choose Always Allow for "+appName+" (System Settings › Privacy & Security › Paste from Other Apps).", "", nil)
		})
	}
}

// patternErrText words an ignore-pattern error for the settings form.
func patternErrText(err error) string {
	var pe *settings.PatternError
	if errors.As(err, &pe) {
		return fmt.Sprintf("Line %d: %v", pe.Line, pe.Err)
	}
	return err.Error()
}

// ignoreApp adds app to the ignore list unless it is listed.
func (a *app) ignoreApp(app string) {
	s := &a.settingsUI
	for _, l := range strings.Split(s.ignoreApps, "\n") {
		if strings.EqualFold(strings.TrimSpace(l), app) {
			return
		}
	}
	if strings.TrimSpace(s.ignoreApps) != "" && !strings.HasSuffix(s.ignoreApps, "\n") {
		s.ignoreApps += "\n"
	}
	s.ignoreApps += app
	a.applySettings()
}
