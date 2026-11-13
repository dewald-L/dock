package host

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
)

type styles struct {
	brand, tabOn, tabOff     lipgloss.Style
	accent, muted, err, bold lipgloss.Style
	sel                      lipgloss.Style
	border, borderOn         lipgloss.Style
	leader                   lipgloss.Style
}

func newStyles(t plugin.Theme) styles {
	return styles{
		brand:    lipgloss.NewStyle().Bold(true).Foreground(t.Accent),
		tabOn:    lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(t.Accent),
		tabOff:   lipgloss.NewStyle().Foreground(t.Muted),
		accent:   lipgloss.NewStyle().Foreground(t.Accent),
		muted:    lipgloss.NewStyle().Foreground(t.Muted),
		err:      lipgloss.NewStyle().Foreground(t.Err).Bold(true),
		bold:     lipgloss.NewStyle().Bold(true),
		sel:      lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		border:   lipgloss.NewStyle().Foreground(t.Border),
		borderOn: lipgloss.NewStyle().Foreground(t.Accent),
		leader:   lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(t.Warn),
	}
}

// ---- layout ----

// rect is a pane's outer position and the size its plugin gets.
type rect struct {
	x, y, w, h int // outer, relative to the content area
	iw, ih     int // inner (plugin) size
	boxed      bool
}

func (a *App) contentSize() (int, int) { return a.w, max(a.h-2, 0) }

func split(total, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = total / n
	}
	out[n-1] += total % n
	return out
}

// layout returns the rectangle of every visible pane of t.
func (a *App) layout(t *tab) map[int]rect {
	cw, ch := a.contentSize()
	out := map[int]rect{}
	if len(t.panes) == 0 {
		return out
	}
	if len(t.panes) == 1 || t.zoom {
		out[t.panes[t.focus]] = rect{w: cw, h: ch, iw: cw, ih: ch}
		return out
	}
	n := len(t.panes)
	if t.rows {
		y := 0
		for i, h := range split(ch, n) {
			out[t.panes[i]] = rect{y: y, w: cw, h: h, iw: max(cw-2, 0), ih: max(h-2, 0), boxed: true}
			y += h
		}
	} else {
		x := 0
		for i, w := range split(cw, n) {
			out[t.panes[i]] = rect{x: x, w: w, h: ch, iw: max(w-2, 0), ih: max(ch-2, 0), boxed: true}
			x += w
		}
	}
	return out
}

// ---- view ----

// View renders tab bar, content and status bar.
func (a *App) View() string {
	if a.w == 0 || a.h == 0 {
		return ""
	}
	if a.h < 3 {
		return strings.Join(fitBlock([]string{"dock: terminal too small"}, a.w, a.h), "\n")
	}
	cw, ch := a.contentSize()
	var body []string
	if t := a.curTab(); t != nil {
		body = a.renderTab(t, cw, ch)
	} else {
		body = a.renderHome(cw, ch)
	}
	switch a.overlay {
	case ovPalette:
		box := a.renderPalette()
		body = overlay(body, box, (cw-ansi.StringWidth(box[0]))/2, min(1, ch-1))
	case ovHelp:
		box := a.renderHelp()
		body = overlay(body, box, (cw-ansi.StringWidth(box[0]))/2, max((ch-len(box))/2, 0))
	}
	lines := make([]string, 0, a.h)
	lines = append(lines, a.renderTabBar())
	lines = append(lines, body...)
	lines = append(lines, a.renderStatus())
	return strings.Join(lines, "\n")
}

func (a *App) renderTab(t *tab, cw, ch int) []string {
	body := blank(cw, ch)
	for _, id := range t.panes {
		r, ok := a.layout(t)[id]
		in := a.inst[id]
		if !ok || in == nil {
			continue
		}
		var lines []string
		if r.boxed {
			lines = a.box(in.name(), a.renderInstance(in, r.iw, r.ih), r.w, r.h, id == a.focusedID())
		} else {
			lines = a.renderInstance(in, r.iw, r.ih)
		}
		body = overlay(body, lines, r.x, r.y)
	}
	return body
}

func (a *App) renderInstance(in *instance, w, h int) []string {
	if w <= 0 || h <= 0 {
		return blank(max(w, 0), max(h, 0))
	}
	m := in.entry.Manifest
	if w < m.MinWidth || h < m.MinHeight {
		msg := fmt.Sprintf("%s needs %dx%d (has %dx%d)", m.Name, m.MinWidth, m.MinHeight, w, h)
		return fitBlock(strings.Split(lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, a.st.muted.Render(msg)), "\n"), w, h)
	}
	return fitBlock(strings.Split(viewOf(in), "\n"), w, h)
}

func (a *App) box(title string, inner []string, w, h int, focused bool) []string {
	if w < 2 || h < 2 {
		return blank(w, h)
	}
	bs := a.st.border
	if focused {
		bs = a.st.borderOn
	}
	title = ansi.Truncate(" "+title+" ", max(w-4, 0), "…")
	tw := ansi.StringWidth(title)
	top := bs.Render("╭─") + a.titleStyle(focused).Render(title) + bs.Render(strings.Repeat("─", max(w-3-tw, 0))+"╮")
	if w-3-tw < 0 {
		top = bs.Render("╭" + strings.Repeat("─", w-2) + "╮")
	}
	out := []string{top}
	side := bs.Render("│")
	for _, l := range fitBlock(inner, w-2, h-2) {
		out = append(out, side+l+side)
	}
	return append(out, bs.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

func (a *App) titleStyle(focused bool) lipgloss.Style {
	if focused {
		return a.st.accent.Bold(true)
	}
	return a.st.muted
}

func (a *App) renderTabBar() string {
	parts := []string{a.st.brand.Render(" dock ")}
	for i, t := range a.tabs {
		names := make([]string, 0, len(t.panes))
		for _, id := range t.panes {
			if in := a.inst[id]; in != nil {
				names = append(names, in.name())
			}
		}
		label := fmt.Sprintf(" %d %s ", i+1, strings.Join(names, " + "))
		if t.zoom {
			label = strings.TrimRight(label, " ") + " [z] "
		}
		if i == a.active {
			parts = append(parts, a.st.tabOn.Render(label))
		} else {
			parts = append(parts, a.st.tabOff.Render(label))
		}
	}
	return fit(strings.Join(parts, " "), a.w)
}

func (a *App) renderStatus() string {
	lk := a.cfg.LeaderKey()
	right := a.st.muted.Render(lk + " ? help ")
	var left string
	switch {
	case a.leader:
		left = a.st.leader.Render(" "+lk+" ") + " " + a.st.accent.Render(
			": palette  o open  v split  x close  z zoom  r rotate  tab pane  [ ] tabs  m plugins  ? help  q quit")
	case a.status != "" && a.statusErr:
		left = a.st.err.Render(" " + a.status)
	case a.status != "":
		left = " " + a.status
	case a.overlay == ovPalette:
		left = a.st.muted.Render(" ↑/↓ select · enter run · esc close")
	case len(a.tabs) == 0:
		left = a.st.muted.Render(" 1-9/enter open · j/k move · : palette · m plugins · q quit")
	default:
		if in := a.focused(); in != nil {
			var hs []string
			for _, h := range helpOf(in) {
				hs = append(hs, a.st.accent.Render(h.Key)+" "+a.st.muted.Render(h.Desc))
			}
			left = " " + strings.Join(hs, a.st.muted.Render(" · "))
		}
	}
	if a.leader {
		return fit(left, a.w)
	}
	rw := ansi.StringWidth(right)
	return fit(ansi.Truncate(left, max(a.w-rw-1, 0), "…"), a.w-rw) + right
}

func (a *App) renderPalette() []string {
	cw, _ := a.contentSize()
	pw := min(70, cw-4)
	inner := max(pw-4, 1)
	p := &a.pal
	p.input.Width = max(inner-3, 1)
	lines := []string{p.input.View(), a.st.border.Render(strings.Repeat("─", inner))}
	start := 0
	if p.sel >= paletteRows {
		start = p.sel - paletteRows + 1
	}
	for i := start; i < len(p.filtered) && i < start+paletteRows; i++ {
		it := p.items[p.filtered[i]]
		label := it.label
		if i == p.sel {
			label = a.st.sel.Render("› " + label)
		} else {
			label = "  " + label
		}
		gap := inner - ansi.StringWidth(label) - ansi.StringWidth(it.hint)
		if gap >= 2 {
			label += strings.Repeat(" ", gap) + a.st.muted.Render(it.hint)
		}
		lines = append(lines, label)
	}
	if len(p.filtered) == 0 {
		lines = append(lines, a.st.muted.Render("  no matching command"))
	}
	return a.panel("Command palette", lines, pw)
}

func (a *App) renderHelp() []string {
	lk := a.cfg.LeaderKey()
	rows := [][2]string{
		{lk + " :", "command palette (also p, space)"},
		{lk + " o", "open a plugin in a new tab"},
		{lk + " v", "open a tile plugin next to this pane"},
		{lk + " x", "close the focused pane"},
		{lk + " z", "zoom / unzoom the focused pane"},
		{lk + " r", "rotate split (columns ↔ rows)"},
		{lk + " tab  h j k l", "move between panes"},
		{lk + " ]  [  1-9", "next / previous / nth tab"},
		{lk + " m", "manage plugins"},
		{lk + " " + lk, "send " + lk + " to the plugin"},
		{lk + " q   ctrl+c", "quit dock"},
	}
	home := [][2]string{
		{"1-9", "open the plugin with that number"},
		{"j k  arrows  g G", "move, first / last"},
		{"enter  o", "open the selected plugin"},
		{":  m  ?  q", "palette, plugins, help, quit"},
	}
	kw := 0
	for _, r := range append(rows, home...) {
		kw = max(kw, ansi.StringWidth(r[0]))
	}
	row := func(r [2]string) string {
		return a.st.accent.Render(fmt.Sprintf("%-*s", kw, r[0])) + "  " + r[1]
	}
	// optional lines go first when the terminal is too short
	type line struct {
		s        string
		optional bool
	}
	var ls []line
	ls = append(ls, line{a.st.muted.Render("Every host shortcut starts with " + lk + "."), true},
		line{a.st.muted.Render("All other keys go to the focused plugin."), true}, line{"", true})
	for _, r := range rows {
		ls = append(ls, line{row(r), false})
	}
	ls = append(ls, line{"", true}, line{a.st.bold.Render("Home screen") + a.st.muted.Render(" (no tab open)"), false})
	for _, r := range home {
		ls = append(ls, line{row(r), false})
	}
	ls = append(ls, line{"", true}, line{a.st.muted.Render("press any key to close"), true})

	cw, ch := a.contentSize()
	drop := len(ls) + 2 - ch
	var lines []string
	for _, l := range ls {
		if l.optional && drop > 0 {
			drop--
			continue
		}
		lines = append(lines, l.s)
	}
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}
	return a.panel("Keys", lines, min(w+4, cw))
}

// panel draws a titled, rounded box of outer width w around lines.
func (a *App) panel(title string, lines []string, w int) []string {
	return a.box(title, lines, w, len(lines)+2, true)
}

// ---- line helpers ----

func blank(w, h int) []string {
	out := make([]string, h)
	for i := range out {
		out[i] = strings.Repeat(" ", w)
	}
	return out
}

// fit truncates or pads s to exactly w cells and resets styling at the end.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(strings.ReplaceAll(s, "\t", "    "), w, "")
	return s + "\x1b[0m" + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// fitBlock makes lines exactly w x h.
func fitBlock(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := range out {
		l := ""
		if i < len(lines) {
			l = strings.TrimRight(lines[i], "\r")
		}
		out[i] = fit(l, w)
	}
	return out
}

// overlay draws box onto base with its top-left corner at (x, y).
func overlay(base, box []string, x, y int) []string {
	x = max(x, 0)
	for i, bl := range box {
		row := y + i
		if row < 0 || row >= len(base) {
			continue
		}
		line := base[row]
		lw := ansi.StringWidth(line)
		bw := ansi.StringWidth(bl)
		left := ansi.Truncate(line, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ""
		if x+bw < lw {
			right = ansi.TruncateLeft(line, x+bw, "")
		}
		base[row] = left + "\x1b[0m" + bl + "\x1b[0m" + right
	}
	return base
}

func shortPath() string {
	p := config.Path()
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
