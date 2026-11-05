package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"okibaclip/internal/history"
)

func (a *app) leading(c *ui.Context, e history.Entry, fg ui.Color) {
	switch e.Kind {
	case history.KindImage:
		if b := a.thumb(e); b != nil {
			ui.Image(c, b).Size(28, 28).Fit(ui.Contain)
			return
		}
	case history.KindColor:
		if history.TextKind(e.Text) == history.KindColor {
			// Rows saved before imports re-derived kinds can hold an invalid color; ui.Hex panics on it.
			ui.Box(c).Size(28, 28).Radius(6).Background(ui.Hex(strings.TrimSpace(e.Text))).Border(1, c.Theme().Text.Alpha(0.12))
			return
		}
	}
	ui.Icon(c, kindIcon(e.Kind)).FontSize(20).TextColor(fg).Size(28, 28)
}

// resultCount labels how many results the shown search found, or is empty
// when nothing is searched.
func (a *app) resultCount() string {
	if a.shown.IsZero() {
		return ""
	}
	n := len(a.items)
	if a.tab == tabSnippets {
		n = len(a.snippets)
	}
	switch {
	case n == 1:
		return "1 result"
	case n >= listLimit && a.tab != tabSnippets:
		return fmt.Sprintf("%d+ results", listLimit)
	}
	return fmt.Sprintf("%d results", n)
}

// emptyState fills an empty list with an icon, a title and tips.
func emptyState(c *ui.Context, icon *ui.SVG, title string, tips ...string) {
	t := c.Theme()
	ui.Column(c).Padding(48, 24).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(32).TextColor(t.TextMuted)
		ui.Text(c, title).Bold().FontSize(15).Wrap().TextAlign(ui.Center)
		ui.Column(c).Width(380).Gap(4).AlignItems(ui.Center).Children(func() {
			for _, tip := range tips {
				ui.Text(c, tip).FontSize(11).TextColor(t.TextMuted).Wrap().TextAlign(ui.Center)
			}
		})
	})
}

// chosenTint is the accent alpha of a chosen list row.
const chosenTint = 0.14

// rowStyle paints a list row's choice on top of what the list paints
// (listTheme): the list tints the rows it has chosen (listChosen) and
// the hovered row. The keys move a.chosen without choosing it in the
// list, so rowStyle tints the chosen row when the list has not, and
// draws the accent bar on its left edge. t is the panel's theme, not
// the list's. It returns the color to highlight search matches with.
func rowStyle(t *ui.Theme, row ui.Element, chosen, listChosen bool) (hl ui.Color) {
	if chosen {
		if !listChosen {
			row.Background(t.Accent.Alpha(chosenTint))
		}
		accent := t.Accent
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 2, Y: r.Y + 10, W: 3, H: r.H - 20}, accent, 1.5)
		})
	}
	if chosen || listChosen {
		return t.Accent.Alpha(0.35)
	}
	return t.Accent.Alpha(0.22)
}

// footerStatus is the footer's right-hand note, or empty.
func (a *app) footerStatus() string {
	switch {
	case a.tab != tabSnippets && a.selected.Len() > 1:
		return fmt.Sprintf("%d selected", a.selected.Len())
	case a.footerNote != "":
		return a.footerNote
	}
	return ""
}

// noMatches fills a list the search found nothing for.
func (a *app) noMatches(c *ui.Context) {
	tips := []string{"Try fewer or shorter words."}
	if a.shown.HasFilters() {
		tips = append(tips, "Or remove a filter: type:, is:, app:, after:, before:.")
	}
	if a.tab != 0 && a.tab != tabSnippets {
		tips = append(tips, "Or search All instead of "+tabLabels[a.tab]+".")
	}
	tips = append(tips, "",
		`"exact phrase" · is:pinned · is:unpinned`,
		"type:text, link, color, image or file",
		"after:/before: 7d, today, 2026-01-31")
	emptyState(c, iconSearch, "No matches for “"+strings.TrimSpace(a.query)+"”", tips...)
}

func (a *app) historyList(c *ui.Context) {
	t := c.Theme()
	a.list.Label = func(i int) string { return preview(a.items[i]) }
	a.list.Selected = &a.chosen
	now := time.Now()
	a.list.Reorder = nil
	if a.tab == tabPinned && a.query == "" {
		a.list.Reorder = func(rows []int, to int) { a.pending = func() { a.reorderPinned(rows, to) } }
	}
	ui.Themed(c, a.listTheme(t)).Background(ui.Transparent).Grow(1).Children(func() {
		list := ui.List(c.Key("history"), &a.list, len(a.items)).
			ItemKey(func(i int) any { return a.items[i].ID }).
			Selection(&a.selected).
			Grow(1).Padding(6).Children(func() {
			switch {
			case len(a.items) > 0:
			case !a.shown.IsZero():
				a.noMatches(c)
			default:
				e := tabEmpty[a.tab]
				tips := []string{e.tip}
				if a.tab == 0 && a.shortcut != "" {
					tips = append(tips, keys(a.shortcut)+" opens this panel.")
				}
				emptyState(c, e.icon, e.title, tips...)
			}
		}).Rows(func(r ui.ListRow) { a.row(r, now, t) })
		if list.Changed() {
			a.loadPreview()
		}
		if list.Submitted() {
			a.copyChosen(false)
		}
	})
	a.runPending()
}

// row builds one history row; a choice in its context menu is noted in
// a.pending, to run once the list is built.
func (a *app) row(r ui.ListRow, now time.Time, t *ui.Theme) {
	c, i := r.Context, r.Index
	e := a.items[i]
	title, detail := rowLines(e)
	row := ui.Row(c).Padding(8, 10).Gap(12).AlignItems(ui.Center).Radius(10)
	hl := rowStyle(t, row, i == a.chosen, r.Selected()) // the keyboard moves a.chosen without filling the selection set
	text, muted := t.Text, t.TextMuted
	row.Children(func() {
		a.leading(c, e, muted)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			a.highlighted(c, title, hl).SingleLine().TextColor(text)
			a.highlighted(c, detail, hl).FontSize(11).SingleLine().TextColor(muted)
		})
		ui.Column(c).Gap(4).AlignItems(ui.End).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if b := a.appIcons[e.SourceApp]; b != nil {
					ui.Image(c, b).Size(12, 12)
				}
				ui.Text(c, relTime(now, e.UsedAt)).FontSize(11).TextColor(muted)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if e.Pinned {
					ui.Icon(c, iconPin).FontSize(12).TextColor(muted)
				}
				if i < 9 {
					ui.Text(c, fmt.Sprintf("%s%d", cmdSym(), i+1)).FontSize(10).TextColor(muted)
				}
			})
		})
	})
	row.ContextMenu(func(m *ui.Menu) { a.entryMenu(m, e) })
}
