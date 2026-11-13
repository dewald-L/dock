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
// a vertical list of numbered plugin entries. Numbers 1-9 are fixed when
// the home screen appears and stay stable while it is open.

const (
	maxHomeNumber  = 9
	homeModalWidth = 64
	homeKeyHint    = "1-9 open · j/k move · ? help"
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
		sel = max(sel-1, 0)
	case "down", "j":
		sel = min(sel+1, max(n-1, 0))
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
	heights := make([]int, n)
	tallest := 1
	for i, he := range entries {
		heights[i] = a.homeBlockHeight(he.e)
		tallest = max(tallest, heights[i])
	}

	mw := min(homeModalWidth, w-4)
	maxH := h - 2 // one row of margin above and below
	pad := 1      // blank rows inside the modal, above and below the list
	if maxH-2-2*pad < tallest {
		pad = 0
	}
	listH := maxH - 2 - 2*pad
	if w < 40 || listH < tallest {
		return a.renderHomeList(w, h, entries, sel)
	}

	// scroll: keep the selection in view, moving as little as possible
	a.homeTop = min(a.homeTop, sel)
	for a.homeTop < sel && sum(heights[a.homeTop:sel+1]) > listH {
		a.homeTop++
	}
	for a.homeTop > 0 && sum(heights[a.homeTop-1:]) <= listH {
		a.homeTop-- // room for an earlier entry (e.g. after a resize)
	}

	var inner []string
	last := a.homeTop - 1
	for i := a.homeTop; i < n && len(inner)+heights[i] <= listH; i++ {
		inner = append(inner, a.homeBlock(entries[i], mw-4, heights[i], i == sel)...)
		last = i
	}
	if n == 0 {
		inner = []string{fit(a.st.muted.Render("No plugins enabled. Press m to manage plugins."), mw-4)}
	}

	var more []string
	if a.homeTop > 0 {
		more = append(more, "↑ more")
	}
	if last < n-1 {
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
		lines = append(lines, side+" "+l+" "+side)
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
	inner := []string{a.st.muted.Render(e.Manifest.Description)}
	var mark label
	if a.summarizer(e) != nil {
		st := a.sums[e.Manifest.ID]
		switch {
		case st != nil && st.ok:
			inner = append(inner, st.sum.Lines...)
			if st.err != "" {
				inner = append(inner, a.st.muted.Render("refresh failed: "+st.err))
			}
			mark = a.levelMark(st.sum.Level)
		case st != nil && st.err != "":
			inner = append(inner, a.st.muted.Render("summary unavailable: "+st.err))
		default:
			inner = append(inner, a.st.muted.Render("…"))
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

// homeBlockHeight is the outer height e's entry wants.
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
