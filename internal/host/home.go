package host

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

// The home screen (shown while no tab is open) is a full-width vertical
// list of numbered plugin entries. Numbers 1-9 are fixed when the home screen
// appears and stay stable while it is open.

const (
	maxHomeNumber = 9
	homeKeyHint   = "1-9 open · j/k move · J/K reorder · ? help"
)

type homeEntry struct {
	e   *registry.Entry
	num int // 1-9, or 0 for no number
}

// refreshHomeIDs appends enabled plugins missing from homeIDs: first those
// in the config's home_order, then the rest. Installed plugins come first in
// registry order, built-in (system) plugins last.
func (a *App) refreshHomeIDs() {
	have := map[string]bool{}
	for _, id := range a.homeIDs {
		have[id] = true
	}
	enabled := a.reg.Enabled()
	for _, id := range a.cfg.HomeOrder {
		if e := a.reg.Get(id); e != nil && e.Enabled && !have[id] {
			a.homeIDs = append(a.homeIDs, id)
			have[id] = true
		}
	}
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
	case "J", "shift+down":
		return a.moveHome(sel, 1)
	case "K", "shift+up":
		return a.moveHome(sel, -1)
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

// moveHome swaps the selected entry with its neighbour in direction dir
// (-1 up, 1 down), skipping disabled plugins, keeps the selection on it and
// saves the order to the config. Numbers follow the new positions.
func (a *App) moveHome(sel, dir int) tea.Cmd {
	entries := a.homeEntries()
	if sel < 0 || sel >= len(entries) || sel+dir < 0 || sel+dir >= len(entries) {
		return nil
	}
	i := slices.Index(a.homeIDs, entries[sel].e.Manifest.ID)
	j := slices.Index(a.homeIDs, entries[sel+dir].e.Manifest.ID)
	if i < 0 || j < 0 {
		return nil
	}
	a.homeIDs[i], a.homeIDs[j] = a.homeIDs[j], a.homeIDs[i]
	a.homeSel = sel + dir
	a.cfg.HomeOrder = slices.Clone(a.homeIDs)
	if err := a.cfg.Save(); err != nil {
		return a.notify("saving order: "+err.Error(), true)
	}
	return nil
}

// ---- rendering ----

// renderHome draws the home screen into a w x h area: a header, full-width
// entries and a footer, or a plain one-line-per-plugin list when the area is too small for it.
func (a *App) renderHome(w, h int) []string {
	body := blank(w, h)
	if w <= 0 || h <= 0 {
		return body
	}
	entries := a.homeEntries()
	n := len(entries)
	sel := min(a.homeSel, max(n-1, 0))
	bw := w - 2    // blocks fill the width, one column of margin each side
	listH := h - 2 // a header row above and a footer row below
	heights := make([]int, n)
	tallest := 1
	for i, he := range entries {
		heights[i] = a.homeBlockHeight(he.e, bw, listH)
		tallest = max(tallest, heights[i])
	}

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
		inner = append(inner, a.homeBlock(entries[i], bw, listH, heights[i], i == sel)...)
		last = i
	}
	if n == 0 {
		inner = []string{a.st.muted.Render("No plugins enabled. Press m to manage plugins.")}
	}

	var more []string
	if a.homeTop > 0 {
		more = append(more, "↑ more")
	}
	if last < n-1 {
		more = append(more, "↓ more")
	}
	header := a.st.brand.Render("dock") + " " + a.st.muted.Render(a.opts.Version)
	if a.updates != "" {
		header += a.st.accent.Render(" · update available")
	}
	footer := a.st.muted.Render(strings.Join(more, " · "))
	hint := a.st.muted.Render(homeKeyHint)
	footer = fit(footer, max(bw-ansi.StringWidth(homeKeyHint), 0)) + hint

	lines := []string{" " + header}
	for _, l := range inner {
		lines = append(lines, " "+l)
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, " "+footer)
	return fitBlock(lines, w, h)
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
		if st := a.sums[he.e.Manifest.ID]; st != nil && st.ok && len(summaryText(st.sum)) > 0 {
			detail = summaryText(st.sum)[0]
		}
		l := "  " + name
		if i == sel {
			l = a.st.sel.Render("› " + name)
		}
		lines = append(lines, l+"  "+a.st.muted.Render(detail))
	}
	return fitBlock(truncLines(lines, w), w, h)
}

// homeBlock renders one plugin's entry, w wide and h tall. listH is the
// room the list has, which decides whether a rich summary can be drawn.
func (a *App) homeBlock(he homeEntry, w, listH, h int, selected bool) []string {
	e := he.e
	title := e.Manifest.Name
	if he.num > 0 {
		title = fmt.Sprintf("[%d] %s", he.num, title)
	}
	inner, mark := a.homeInner(e, w-4, listH)
	return a.block(title, mark, kindHint(e), inner, w, h, selected)
}

// homeInner is the content of e's entry (innerW wide) and its status mark.
// A summary with widgets is drawn rich when the widgets fit the width and
// the entry fits listH; otherwise it falls back to its text lines.
func (a *App) homeInner(e *registry.Entry, innerW, listH int) ([]string, label) {
	inner := []string{a.st.muted.Render(e.Manifest.Description)}
	var mark label
	if a.summarizer(e) == nil {
		return inner, mark
	}
	st := a.sums[e.Manifest.ID]
	switch {
	case st != nil && st.ok:
		body := summaryText(st.sum)
		if len(st.sum.Widgets) > 0 {
			rows, ok := a.renderWidgets(st.sum.Widgets, innerW)
			if ok && len(inner)+len(rows)+boolInt(st.err != "")+2 <= listH {
				body = rows
			}
		}
		inner = append(inner, body...)
		if st.err != "" {
			inner = append(inner, a.st.muted.Render("refresh failed: "+st.err))
		}
		mark = a.levelMark(st.sum.Level)
	case st != nil && st.err != "":
		inner = append(inner, a.st.muted.Render("summary unavailable: "+st.err))
	default:
		inner = append(inner, a.st.muted.Render("…"))
	}
	return inner, mark
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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

// homeBlockHeight is the outer height e's entry wants at width w.
func (a *App) homeBlockHeight(e *registry.Entry, w, listH int) int {
	inner, _ := a.homeInner(e, w-4, listH)
	return len(inner) + 2
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
