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

// The home screen (shown while no tab is open) is a centred modal holding
// a grid of square tiles, one per plugin. Numbers 1-9 are fixed when
// the home screen appears and stay stable while it is open.

const (
	maxHomeNumber = 9
	homeTileH     = 12 // preferred tile height; terminal cells are ~2:1, so the width is twice this
	homeMinTileH  = 8
	homeTileGap   = 1
	homeMinModalW = 48 // room for the version / update label
	homeKeyHint   = "1-9 open · hjkl move · ? help"
)

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

// homeEntries lists the entries in display order. A plugin keeps the
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

// homeKey handles the home screen's keys.
func (a *App) homeKey(s string) tea.Cmd {
	entries := a.homeEntries()
	n := len(entries)
	sel := min(a.homeSel, max(n-1, 0))
	cols := max(a.homeCols, 1)
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
	case "left", "h":
		sel = max(sel-1, 0)
	case "right", "l":
		sel = min(sel+1, max(n-1, 0))
	case "up", "k":
		if sel-cols >= 0 {
			sel -= cols
		}
	case "down", "j":
		if sel+cols < n {
			sel += cols
		} else if sel/cols < (n-1)/cols {
			sel = n - 1 // last row is short: land on its last tile
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

// renderHome draws the home screen into a w x h area: the modal centred,
// or a plain one-line-per-plugin list when the area is too small for it.
func (a *App) renderHome(w, h int) []string {
	body := blank(w, h)
	if w <= 0 || h <= 0 {
		return body
	}
	entries := a.homeEntries()
	n := len(entries)
	sel := min(a.homeSel, max(n-1, 0))

	// Tiles are square: cells are about twice as tall as wide, so a tile is
	// twice as wide as it is high.
	aw := w - 4 // modal width limit: a 2-column margin each side
	maxH := h - 2
	pad := 1
	gridH := maxH - 2 - 2*pad
	if gridH < homeMinTileH {
		pad = 0
		gridH = maxH - 2
	}
	th := min(homeTileH, gridH, (aw-4)/2)
	if w < 40 || th < homeMinTileH {
		return a.renderHomeList(w, h, entries, sel)
	}
	tw := 2 * th
	cols := max(1, min(max(n, 1), (aw-4+homeTileGap)/(tw+homeTileGap)))
	a.homeCols = cols
	totalRows := (n + cols - 1) / cols
	visRows := max(1, min(max(totalRows, 1), gridH/th))
	gridW := cols*tw + (cols-1)*homeTileGap
	mw := max(gridW+4, homeMinModalW)
	lpad := (mw - 4 - gridW) / 2 // centre a narrow grid in the modal

	// scroll by rows: keep the selection's row in view, moving as little as possible
	selRow := sel / cols
	a.homeTop = min(a.homeTop, selRow)
	if selRow >= a.homeTop+visRows {
		a.homeTop = selRow - visRows + 1
	}
	a.homeTop = max(min(a.homeTop, max(totalRows-visRows, 0)), 0)

	var inner []string
	if n == 0 {
		inner = []string{a.st.muted.Render("No plugins enabled. Press m to manage plugins.")}
	}
	for r := a.homeTop; r < min(a.homeTop+visRows, totalRows); r++ {
		var tiles [][]string
		for c := range cols {
			i := r*cols + c
			if i < n {
				tiles = append(tiles, a.homeBlock(entries[i], tw, th, i == sel))
			} else {
				tiles = append(tiles, blank(tw, th))
			}
		}
		for y := range th {
			row := make([]string, cols)
			for c := range cols {
				row[c] = tiles[c][y]
			}
			inner = append(inner, strings.Join(row, strings.Repeat(" ", homeTileGap)))
		}
	}

	var more []string
	if a.homeTop > 0 {
		more = append(more, "↑ more")
	}
	if a.homeTop+visRows < totalRows {
		more = append(more, "↓ more")
	}
	bs := a.st.border
	side := bs.Render("│")
	padRow := side + strings.Repeat(" ", mw-2) + side
	ver := label{a.opts.Version, a.st.muted}
	if a.updates != "" {
		ver = label{a.opts.Version + " · update available", a.st.accent}
	}
	lines := []string{borderLine("╭", "╮", label{"dock", a.st.brand}, ver, mw, bs)}
	for range pad {
		lines = append(lines, padRow)
	}
	for _, l := range inner {
		l = strings.Repeat(" ", lpad) + l
		lines = append(lines, side+" "+fit(l, mw-4)+" "+side)
	}
	for range pad {
		lines = append(lines, padRow)
	}
	lines = append(lines, borderLine("╰", "╯", label{strings.Join(more, " · "), a.st.muted}, label{homeKeyHint, a.st.muted}, mw, bs))
	return overlay(body, lines, (w-mw)/2, (h-len(lines))/2)
}

// renderHomeList is the fallback for tiny terminals: one line per plugin,
// no borders.
func (a *App) renderHomeList(w, h int, entries []homeEntry, sel int) []string {
	avail := h - 1
	if avail <= 0 {
		return fitBlock([]string{a.st.muted.Render(" dock")}, w, h)
	}
	a.homeTop = min(a.homeTop, sel)
	if sel-a.homeTop >= avail {
		a.homeTop = sel - avail + 1
	}
	for a.homeTop > 0 && len(entries)-a.homeTop+1 <= avail {
		a.homeTop--
	}
	header := a.st.muted.Render(" dock " + a.opts.Version)
	if a.updates != "" {
		header += a.st.accent.Render(" · update available")
	}
	var more []string
	if a.homeTop > 0 {
		more = append(more, "↑")
	}
	if a.homeTop+avail < len(entries) {
		more = append(more, "↓")
	}
	if len(more) > 0 {
		m := a.st.muted.Render(strings.Join(more, " ") + " more ")
		header = fit(header, max(w-ansi.StringWidth(m), 0)) + m
	}
	lines := []string{header}
	if len(entries) == 0 {
		lines = append(lines, a.st.muted.Render(" No plugins. Press m."))
	}
	names := make([]string, len(entries))
	nameW := 0
	for i, he := range entries {
		names[i] = he.e.Manifest.Name
		if he.num > 0 {
			names[i] = fmt.Sprintf("[%d] %s", he.num, names[i])
		}
		nameW = max(nameW, ansi.StringWidth(names[i]))
	}
	for i := a.homeTop; i < len(entries) && len(lines) <= avail; i++ {
		he := entries[i]
		name := names[i] + strings.Repeat(" ", nameW-ansi.StringWidth(names[i]))
		detail := he.e.Manifest.Description
		if st := a.sums[he.e.Manifest.ID]; st != nil && st.ok && len(st.sum.Lines) > 0 {
			detail = st.sum.Lines[0]
		}
		l := "  " + name
		if i == sel {
			l = a.st.sel.Render("› " + name)
		}
		lines = append(lines, l+"  "+a.st.muted.Render(detail))
	}
	return fitBlock(truncLines(lines, w), w, h)
}

// homeBlock renders one plugin's entry, w wide and h tall.
func (a *App) homeBlock(he homeEntry, w, h int, selected bool) []string {
	e := he.e
	title := e.Manifest.Name
	if he.num > 0 {
		title = fmt.Sprintf("[%d] %s", he.num, title)
	}
	iw := max(w-4, 1)
	wrap := func(text string, st lipgloss.Style) []string {
		var out []string
		for _, l := range strings.Split(ansi.Wrap(text, iw, ""), "\n") {
			out = append(out, st.Render(l))
		}
		return out
	}
	inner := wrap(e.Manifest.Description, a.st.muted)
	var mark label
	if a.summarizer(e) != nil {
		st := a.sums[e.Manifest.ID]
		switch {
		case st != nil && st.ok:
			inner = append(inner, "")
			for _, l := range st.sum.Lines {
				inner = append(inner, wrap(l, lipgloss.NewStyle())...)
			}
			if st.err != "" {
				inner = append(inner, wrap("refresh failed: "+st.err, a.st.muted)...)
			}
			mark = a.levelMark(st.sum.Level)
		case st != nil && st.err != "":
			inner = append(inner, "")
			inner = append(inner, wrap("summary unavailable: "+st.err, a.st.muted)...)
		default:
			inner = append(inner, "", a.st.muted.Render("…"))
		}
	}
	return a.block(title, mark, kindHint(e), inner, w, h, selected)
}

func (a *App) levelMark(l plugin.Level) label {
	var c lipgloss.Color
	switch l {
	case plugin.LevelOk:
		c = a.theme.Ok
	case plugin.LevelWarn:
		c = a.theme.Warn
	case plugin.LevelErr:
		c = a.theme.Err
	default:
		return label{}
	}
	return label{"●", lipgloss.NewStyle().Foreground(c)}
}

// block draws a rounded box with a title on the top border, an optional
// mark at the top right and a footer at the bottom right. Inner lines get
// one cell of padding on each side and are truncated with "…".
func (a *App) block(title string, mark label, footer string, inner []string, w, h int, selected bool) []string {
	if w < 4 || h < 2 {
		return blank(max(w, 0), max(h, 0))
	}
	bs := a.st.border
	ts := lipgloss.NewStyle()
	if selected {
		bs, ts = a.st.borderOn, a.st.accent.Bold(true)
	}
	out := []string{borderLine("╭", "╮", label{title, ts}, mark, w, bs)}
	side := bs.Render("│")
	if len(inner) > h-2 {
		inner = append(inner[:h-3:h-3], a.st.muted.Render("…"))
	}
	for _, l := range fitBlock(truncLines(inner, w-4), w-4, h-2) {
		out = append(out, side+" "+l+" "+side)
	}
	return append(out, borderLine("╰", "╯", label{}, label{footer, a.st.muted}, w, bs))
}

// label is text drawn on a border line.
type label struct {
	text  string
	style lipgloss.Style
}

// borderLine draws a border w wide: a corner, a dash, the left label,
// dashes, the right label, a dash and the other corner. Labels get a space
// on each side. When they don't fit, the right one is dropped first, then
// the left one is truncated.
func borderLine(lc, rc string, l, r label, w int, bs lipgloss.Style) string {
	if w < 4 {
		return fit(bs.Render(lc+strings.Repeat("─", max(w-2, 0))+rc), w)
	}
	room := w - 4 // between the corner+dash pairs
	lt, rt := "", ""
	if l.text != "" {
		lt = " " + l.text + " "
	}
	if r.text != "" {
		rt = " " + r.text + " "
	}
	lw, rw := ansi.StringWidth(lt), ansi.StringWidth(rt)
	if lw+rw+1 > room {
		rt, rw = "", 0
	}
	if lw > room {
		lt = ""
		if room >= 4 {
			lt = ansi.Truncate(" "+l.text, room-1, "…") + " "
		}
		lw = ansi.StringWidth(lt)
	}
	s := bs.Render(lc + "─")
	if lt != "" {
		s += l.style.Render(lt)
	}
	s += bs.Render(strings.Repeat("─", room-lw-rw))
	if rt != "" {
		s += r.style.Render(rt)
	}
	return s + bs.Render("─"+rc)
}

// truncLines shortens every line to w cells with "…".
func truncLines(lines []string, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, w, "…")
	}
	return out
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}
