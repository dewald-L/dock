package host

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/plugin"
)

// Rich summary widgets (plugin.Widget). The plugin supplies data only; the
// host draws it at the block's inner width with the theme's colours. The
// widgets have already been through plugin.Summary.Clean, but the drawing
// code still tolerates odd values (a zero Max, an empty slice) so it can't
// panic or overflow the width.

const (
	vbars   = "▁▂▃▄▅▆▇█" // eighths, bottom up
	hbars   = "▏▎▍▌▋▊▉"  // partial cells, 1/8 to 7/8
	maxBarW = 14         // widest bar of a BarChart
	minProg = 4          // narrowest progress bar worth drawing
)

// levelStyle colours text by level; LevelNone leaves the default colour.
func (a *App) levelStyle(l plugin.Level) lipgloss.Style {
	switch l {
	case plugin.LevelOk:
		return lipgloss.NewStyle().Foreground(a.theme.Ok)
	case plugin.LevelWarn:
		return lipgloss.NewStyle().Foreground(a.theme.Warn)
	case plugin.LevelErr:
		return lipgloss.NewStyle().Foreground(a.theme.Err)
	}
	return lipgloss.NewStyle()
}

// renderWidgets draws ws into rows at most w cells wide. ok is false when a
// widget can't be drawn that narrow; the caller then falls back to text.
func (a *App) renderWidgets(ws []plugin.Widget, w int) (rows []string, ok bool) {
	if w < 1 {
		return nil, false
	}
	for _, wd := range ws {
		var r []string
		switch wd.Kind {
		case plugin.KindText:
			r, ok = []string{a.levelStyle(wd.Level).Render(ansi.Truncate(wd.Text, w, "…"))}, true
		case plugin.KindProgress:
			r, ok = a.progress(wd, w)
		case plugin.KindSparkline:
			r, ok = a.sparkline(wd, w)
		case plugin.KindBarChart:
			r, ok = a.barChart(wd, w)
		default:
			continue // unknown kind: skip it
		}
		if !ok {
			return nil, false
		}
		rows = append(rows, r...)
	}
	return rows, true
}

// labelCol returns the styled, padded label column ("label ") for a
// progress bar or sparkline, and its width. At most a third of w.
func (a *App) labelCol(label string, w int) (string, int) {
	if label == "" {
		return "", 0
	}
	lw := min(ansi.StringWidth(label), max(w/3, 1))
	t := ansi.Truncate(label, lw, "…")
	pad := strings.Repeat(" ", max(lw-ansi.StringWidth(t), 0))
	return a.st.muted.Render(t+pad) + " ", lw + 1
}

func frac(v, max float64) float64 {
	if !(max > 0) || math.IsNaN(v) {
		return 0
	}
	return math.Min(math.Max(v/max, 0), 1)
}

func (a *App) progress(wd plugin.Widget, w int) ([]string, bool) {
	prefix, pw := a.labelCol(wd.Label, w)
	caption := wd.Caption
	if caption == "" {
		caption = fmt.Sprintf("%d%%", int(math.Round(frac(wd.Value, wd.Max)*100)))
	}
	cw := ansi.StringWidth(caption)
	bw := w - pw - 1 - cw
	if bw < minProg {
		return nil, false
	}
	eighths := int(math.Round(frac(wd.Value, wd.Max) * float64(bw) * 8))
	if wd.Value > 0 && eighths == 0 {
		eighths = 1
	}
	full, part := eighths/8, eighths%8
	bar := strings.Repeat("█", full)
	used := full
	if part > 0 {
		bar += string([]rune(hbars)[part-1])
		used++
	}
	line := prefix + a.levelStyle(wd.Level).Render(bar) +
		a.st.muted.Render(strings.Repeat("░", bw-used)) + " " + caption
	return []string{line}, true
}

func (a *App) sparkline(wd plugin.Widget, w int) ([]string, bool) {
	prefix, pw := a.labelCol(wd.Label, w)
	room := w - pw
	if room < 1 || len(wd.Values) == 0 {
		return nil, false
	}
	vs := wd.Values
	if len(vs) > room {
		vs = vs[len(vs)-room:] // the newest fit
	}
	top := 0.0
	for _, v := range vs {
		if v > top && !math.IsInf(v, 0) {
			top = v
		}
	}
	glyphs := []rune(vbars)
	var sb strings.Builder
	ls := a.levelStyle(wd.Level)
	for _, v := range vs {
		f := frac(v, top)
		if f == 0 {
			sb.WriteString(a.st.muted.Render("▁"))
			continue
		}
		n := min(max(int(math.Ceil(f*8)), 1), 8)
		sb.WriteString(ls.Render(string(glyphs[n-1])))
	}
	return []string{prefix + sb.String()}, true
}

// barChart draws vertical bars sharing w: a gap of 2 cells (1 when tight)
// and bars up to maxBarW wide. Heights have eighth-cell resolution.
func (a *App) barChart(wd plugin.Widget, w int) ([]string, bool) {
	n := len(wd.Bars)
	if n == 0 {
		return nil, false
	}
	gap := 2
	bw := min((w+gap)/n-gap, maxBarW)
	if bw < 3 {
		gap = 1
		bw = min((w+gap)/n-gap, maxBarW)
	}
	if bw < 1 {
		return nil, false
	}
	h := min(max(wd.Height, 1), plugin.MaxChartHeight)
	top := wd.Max
	if !(top > 0) {
		for _, b := range wd.Bars {
			if b.Value > top && !math.IsInf(b.Value, 0) {
				top = b.Value
			}
		}
	}
	sep := strings.Repeat(" ", gap)
	styles := make([]lipgloss.Style, n)
	eighths := make([]int, n)
	hasCaption := false
	for i, b := range wd.Bars {
		switch {
		case b.Level != plugin.LevelNone:
			styles[i] = a.levelStyle(b.Level)
		case b.Focus:
			styles[i] = a.st.accent
		}
		e := int(math.Round(frac(b.Value, top) * float64(h) * 8))
		if b.Value > 0 && e == 0 {
			e = 1 // a logged value is never invisible
		}
		eighths[i] = e
		hasCaption = hasCaption || b.Caption != ""
	}
	glyphs := []rune(vbars)
	var rows []string
	for r := 0; r < h; r++ { // top row first
		cells := make([]string, n)
		for i := range wd.Bars {
			e := min(max(eighths[i]-(h-1-r)*8, 0), 8)
			g := " "
			if e > 0 {
				g = string(glyphs[e-1])
			}
			cells[i] = styles[i].Render(strings.Repeat(g, bw))
		}
		rows = append(rows, strings.Join(cells, sep))
	}
	centre := func(s string, st lipgloss.Style) string {
		s = ansi.Truncate(s, bw, "")
		pad := bw - ansi.StringWidth(s)
		return strings.Repeat(" ", pad/2) + st.Render(s) + strings.Repeat(" ", pad-pad/2)
	}
	labels := make([]string, n)
	captions := make([]string, n)
	for i, b := range wd.Bars {
		ls := a.st.muted
		if b.Focus {
			ls = a.st.accent.Bold(true)
		}
		labels[i] = centre(b.Label, ls)
		captions[i] = centre(b.Caption, a.st.muted)
	}
	rows = append(rows, strings.Join(labels, sep))
	if hasCaption {
		rows = append(rows, strings.Join(captions, sep))
	}
	return rows, true
}

// summaryText is the plain-text form of a summary: its Lines, or, when a
// plugin gave only widgets, the text widgets.
func summaryText(s plugin.Summary) []string {
	if len(s.Lines) > 0 {
		return s.Lines
	}
	var out []string
	for _, w := range s.Widgets {
		if w.Kind == plugin.KindText && w.Text != "" {
			out = append(out, w.Text)
		}
	}
	return out
}
