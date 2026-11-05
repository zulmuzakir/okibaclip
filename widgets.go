package main

import (
	"github.com/egoist/mygo/ui"
)

func (a *app) banners(c *ui.Context) {
	t := c.Theme()
	line := func(icon *ui.SVG, tone ui.Color, msg, button string, fn func()) {
		right := float32(12)
		if button != "" {
			right = 6
		}
		ui.Row(c).Margin(0, 16, 8, 16).Padding(6, right, 6, 12).Gap(8).AlignItems(ui.Center).
			Radius(8).Background(tone.Alpha(0.12)).Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(tone)
			ui.Text(c, msg).FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
			if button != "" {
				pillButton(c, nil, button, tone).OnClick(fn)
			}
		})
	}
	if a.waylandMode == "wayland" && !a.waylandCapture {
		line(iconInfo, t.Accent, "Native Wayland: copies are recorded only while this window is focused.", "", nil)
	}
	if paused, until := a.paused(); paused {
		msg := "Capture paused until resumed"
		if !until.IsZero() {
			msg = "Capture paused until " + until.Format("15:04")
		}
		line(iconPause, t.Warning, msg, "Resume", func() { a.mon.Resume() })
	}
	if a.trayErr != "" {
		line(iconAlert, t.Warning, "Tray icon unavailable: "+a.trayErr, "", nil)
	}
	if a.cfgErr != "" {
		line(iconAlert, t.Warning, a.cfgErr, "", nil)
	}
}

// iconButton is a borderless button showing icon in fg, named label for
// assistive technology (and Tester.Click), with tip as its tooltip.
func iconButton(c *ui.Context, icon *ui.SVG, label, tip string, fg ui.Color) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Padding(6).Radius(6)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon).Size(16, 16).TextColor(fg) })
	return b
}

// pillButton is a compact rounded button: an optional icon and label in
// fg on a faint fill of fg, for secondary actions that should weigh less
// than ui.Button. label also names it for assistive technology and
// Tester.Click.
func pillButton(c *ui.Context, icon *ui.SVG, label string, fg ui.Color) ui.Element {
	b := ui.ButtonBase(c).Label(label).Padding(4, 10).Radius(999).Gap(6).AlignItems(ui.Center)
	fill := fg.Alpha(0.10)
	if b.Hovered() {
		fill = fg.Alpha(0.18)
	}
	b.Background(fill).Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(13, 13).TextColor(fg)
		}
		ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(fg).SingleLine()
	})
	return b
}

// softButton is a borderless button sized like ui.Button: label in fg on
// a faint fill of fg, for secondary actions beside ui.PrimaryButton and
// in settings rows. label also names it for assistive technology and
// Tester.Click.
func softButton(c *ui.Context, label string, fg ui.Color) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Label(label).Padding(t.Space(1.5), t.Space(3.5)).Radius(t.Radius)
	fill := fg.Alpha(0.08)
	if b.Hovered() {
		fill = fg.Alpha(0.14)
	}
	b.Background(fill).Children(func() { ui.Text(c, label).TextColor(fg).SingleLine() })
	return b
}

// listTheme is t for the history and snippet lists. mygo's List fills
// the rows it has chosen with Accent and colors them AccentText (ui
// node.Selected); here that fill is the soft chosen tint and the text
// keeps Text, so a chosen row looks the same whether the list or the
// search field has the focus. Cached: a build copies nothing.
func (a *app) listTheme(t *ui.Theme) *ui.Theme {
	if a.listBase != *t {
		a.listBase, a.listThemed = *t, *t
		a.listThemed.Accent = t.Accent.Alpha(chosenTint)
		a.listThemed.AccentText = t.Text
		a.listThemed.Radius = 10
	}
	return &a.listThemed
}

// fieldTheme is t for the editor modals: inputs filled with the modal's
// Background instead of Surface, so they read as outlined fields, as on
// the settings cards. Cached: a build copies nothing.
func (a *app) fieldTheme(t *ui.Theme) *ui.Theme {
	if a.fieldBase != *t {
		a.fieldBase, a.fieldThemed = *t, *t
		a.fieldThemed.Surface = t.Background
	}
	return &a.fieldThemed
}

// filterBar shows the history filters as chips, with Snippets set apart
// at the end. It is a tab list: arrows, Home and End move the choice
// while a chip has the focus.
func (a *app) filterBar(c *ui.Context) {
	tabs := ui.TabsBase(c, &a.tab, len(tabLabels))
	tabs.List.FillWidth().Padding(4, 16, 12, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		for i, label := range tabLabels {
			if i == tabSnippets {
				ui.Box(c).Grow(1)
			}
			pillTab(c, tabs.Tab(i), i == a.tab, chipIcons[i], label)
		}
	})
	if tabs.List.Changed() {
		a.setTab(a.tab)
	}
}

// pillTab styles a TabsBase tab as a borderless pill: a soft accent fill
// and semibold text when on, the hover face when hovered, muted text
// otherwise. icon may be nil.
func pillTab(c *ui.Context, chip ui.Element, on bool, icon *ui.SVG, label string) {
	t := c.Theme()
	chip.Padding(5, 12).Radius(999).Gap(6).AlignItems(ui.Center)
	fg := t.TextMuted
	switch {
	case on:
		chip.Background(t.Accent.Alpha(0.18))
		fg = t.Text
	case chip.Hovered():
		chip.Background(t.SurfaceHover)
		fg = t.Text
	}
	chip.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(13, 13).TextColor(fg)
		}
		txt := ui.Text(c, label).FontSize(12).SingleLine().TextColor(fg)
		if on {
			txt.FontWeight(600)
		}
	})
}

// syntaxHelp is the content of the search syntax popover.
func (a *app) syntaxHelp(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(320).Padding(12).Gap(6).Children(func() {
		ui.Text(c, "Search syntax").Bold().FontSize(13)
		for _, r := range syntaxRows {
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, r[0]).Font("monospace").FontSize(12).Width(120)
				ui.Text(c, r[1]).FontSize(12).TextColor(t.TextMuted).Wrap().Grow(1)
			})
		}
	})
}

// hintSpans is the footer's key hints, each key as a keycap.
func (a *app) hintSpans(t *ui.Theme) []ui.Span {
	cm := cmdSym()
	filter := [2]string{"←→", "Filter"}
	if a.query != "" {
		filter[0] = keys("Ctrl+Tab")
	}
	pairs := [][2]string{{"↵", "Copy"}, {"⇧↵", "Plain"}, filter, {cm + "P", "Pin"}, {cm + "⌫", "Delete"}, {"esc", "Close"}}
	switch {
	case a.tab == tabSnippets:
		pairs = [][2]string{{"↵", "Copy"}, filter, {cm + "N", "New"}, {cm + "⌫", "Delete"}, {"esc", "Close"}}
	case a.selected.Len() > 1:
		pairs[0][1] = "Copy merged"
	}
	var spans []ui.Span
	for i, p := range pairs {
		action := " " + p[1]
		if i < len(pairs)-1 {
			action += "    "
		}
		spans = append(spans,
			ui.Span{Text: " " + p[0] + " ", Size: 10, Weight: 600, Color: t.Text, Background: t.Text.Alpha(0.10), Radius: 5},
			ui.Span{Text: action, Color: t.TextMuted})
	}
	return spans
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
