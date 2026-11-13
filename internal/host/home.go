package host

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

// The home screen (shown while no tab is open) is a grid of numbered
// plugin blocks. Numbers 1-9 are fixed when the home screen appears and
// stay stable while it is open.

const maxHomeNumber = 9

type homeEntry struct {
	e   *registry.Entry
	num int // 1-9, or 0 for no number
}

// refreshHomeIDs appends enabled plugins missing from homeIDs. Installed
// plugins come first in registry order, built-in (system) plugins last.
func (a *App) refreshHomeIDs() {
	have := map[string]bool{}
	for _, id := range a.homeIDs {
		have[id] = true
	}
	enabled := a.reg.Enabled()
	for _, system := range []bool{false, true} {
		for _, e := range enabled {
			if (e.Kind == registry.System) == system && !have[e.Manifest.ID] {
				a.homeIDs = append(a.homeIDs, e.Manifest.ID)
				have[e.Manifest.ID] = true
			}
		}
	}
}

// homeEntries lists the blocks in display order. A plugin keeps the
// number it was given when the home screen appeared, even if one before it
// is disabled meanwhile.
func (a *App) homeEntries() []homeEntry {
	var out []homeEntry
	for i, id := range a.homeIDs {
		e := a.reg.Get(id)
		if e == nil || !e.Enabled {
			continue
		}
		num := 0
		if i < maxHomeNumber {
			num = i + 1
		}
		out = append(out, homeEntry{e: e, num: num})
	}
	return out
}

func homeCols(w int) int {
	switch {
	case w >= 120:
		return 3
	case w >= 80:
		return 2
	}
	return 1
}

// homeKey handles the home screen's keys.
func (a *App) homeKey(s string) tea.Cmd {
	entries := a.homeEntries()
	n := len(entries)
	cols := homeCols(a.w)
	sel := min(a.homeSel, max(n-1, 0))
	switch s {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		want := int(s[0] - '0')
		for i, he := range entries {
			if he.num == want {
				a.homeSel = i
				return a.open(he.e.Manifest.ID, false)
			}
		}
		return nil
	case "up", "k":
		if sel-cols >= 0 {
			sel -= cols
		}
	case "down", "j":
		switch {
		case sel+cols < n:
			sel += cols
		case sel/cols < (n-1)/cols:
			sel = n - 1 // the row below is shorter
		}
	case "left", "h":
		if sel%cols > 0 {
			sel--
		}
	case "right", "l":
		if sel%cols < cols-1 && sel+1 < n {
			sel++
		}
	case "home", "g":
		sel = 0
	case "end", "G":
		sel = max(n-1, 0)
	case "enter", "o":
		if sel < n {
			return a.open(entries[sel].e.Manifest.ID, false)
		}
	case ":", "p":
		return a.openPalette("")
	case "m":
		return a.open("plugins", false)
	case "?":
		a.overlay = ovHelp
	case "q":
		return a.quit()
	}
	a.homeSel = sel
	return nil
}

// ---- rendering ----

// homeBlock renders one plugin's block, w wide and h tall.
func (a *App) homeBlock(he homeEntry, w, h int, selected bool) []string {
	e := he.e
	title := e.Manifest.Name
	if he.num > 0 {
		title = fmt.Sprintf("[%d] %s", he.num, title)
	}
	inner := []string{a.st.muted.Render(e.Manifest.Description)}
	right := ""
	if a.summarizer(e) != nil {
		st := a.sums[e.Manifest.ID]
		switch {
		case st != nil && st.ok:
			inner = append(inner, st.sum.Lines...)
			if st.err != "" {
				inner = append(inner, a.st.muted.Render("refresh failed: "+st.err))
			}
			right = a.levelMark(st.sum.Level)
		case st != nil && st.err != "":
			inner = append(inner, a.st.muted.Render("summary unavailable: "+st.err))
		default:
			inner = append(inner, a.st.muted.Render("…"))
		}
	}
	return a.block(title, right, kindHint(e), inner, w, h, selected)
}

func (a *App) levelMark(l plugin.Level) string {
	switch l {
	case plugin.LevelOk:
		return lipgloss.NewStyle().Foreground(a.theme.Ok).Render("●")
	case plugin.LevelWarn:
		return lipgloss.NewStyle().Foreground(a.theme.Warn).Render("●")
	case plugin.LevelErr:
		return lipgloss.NewStyle().Foreground(a.theme.Err).Render("●")
	}
	return ""
}

// homeBlockHeight is the outer height e's block wants.
func (a *App) homeBlockHeight(e *registry.Entry) int {
	n := 1 // description
	if a.summarizer(e) != nil {
		st := a.sums[e.Manifest.ID]
		switch {
		case st != nil && st.ok:
			n += len(st.sum.Lines)
			if st.err != "" {
				n++
			}
		default:
			n++ // "…" or the error
		}
	}
	return n + 2
}

// block draws a rounded box with a title on the top border, an optional
// mark at the top right and a footer at the bottom right. Inner lines get
// one cell of padding on each side and are truncated with "…".
func (a *App) block(title, mark, footer string, inner []string, w, h int, selected bool) []string {
	if w < 4 || h < 2 {
		return blank(max(w, 0), max(h, 0))
	}
	bs := a.st.border
	ts := lipgloss.NewStyle()
	if selected {
		bs, ts = a.st.borderOn, a.st.accent.Bold(true)
	}
	// edge is a border line: a corner, a dash, the label, dashes to fill,
	// then optionally " mark ─", and the other corner. A right-aligned
	// label is dashes, label, one dash.
	edge := func(left, right, label string, style lipgloss.Style, labelRight bool, mark string) string {
		room := w - 2 // between the corners
		mw := ansi.StringWidth(mark)
		if mw > 0 && mw+3+4 <= room {
			room -= mw + 3
		} else {
			mark, mw = "", 0
		}
		label = ansi.Truncate(" "+label+" ", max(room-2, 0), "… ")
		if room-2 < 3 {
			label = ""
		}
		fill := strings.Repeat("─", room-ansi.StringWidth(label)-1)
		var s string
		if labelRight {
			s = bs.Render(left+fill) + style.Render(label) + bs.Render("─")
		} else {
			s = bs.Render(left+"─") + style.Render(label) + bs.Render(fill)
		}
		if mw > 0 {
			s += " " + mark + " " + bs.Render("─")
		}
		return s + bs.Render(right)
	}
	out := []string{edge("╭", "╮", title, ts, false, mark)}
	side := bs.Render("│")
	for _, l := range fitBlock(truncLines(inner, w-4), w-4, h-2) {
		out = append(out, side+" "+l+" "+side)
	}
	return append(out, edge("╰", "╯", footer, a.st.muted, true, ""))
}

// truncLines shortens every line to w cells with "…".
func truncLines(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, w, "…")
	}
	return out
}
