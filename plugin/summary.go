package plugin

import (
	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// MaxSummaryLines is the most summary lines the home screen shows. Extra
// lines are dropped.
const MaxSummaryLines = 3

// Limits for the structured form (Summary.Widgets). Clean enforces them, so
// a plugin (or an exec summary command) can't make the host do unbounded
// work or draw an unbounded block.
const (
	MaxSummaryRows   = 8   // total rows of all widgets in one block
	MaxChartHeight   = 6   // plot rows of a BarChart, not counting its label rows
	MaxChartBars     = 31  // bars in a BarChart
	MaxSparkValues   = 512 // values in a Sparkline
	MaxWidgets       = 8   // widgets in one Summary
	MaxWidgetLabel   = 64  // runes in a label or text
	MaxWidgetCaption = 16  // runes in a caption
)

// Level is an optional status for a Summary. The host picks the colour
// from its theme.
type Level int

const (
	LevelNone Level = iota // no status marker
	LevelOk
	LevelWarn
	LevelErr
)

func (l Level) String() string {
	switch l {
	case LevelOk:
		return "ok"
	case LevelWarn:
		return "warn"
	case LevelErr:
		return "err"
	}
	return "none"
}

// MarshalText and UnmarshalText make Level "none", "ok", "warn" or "err" in
// JSON (exec summaries). An unknown name decodes as LevelNone.
func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func (l *Level) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	case "ok":
		*l = LevelOk
	case "warn":
		*l = LevelWarn
	case "err":
		*l = LevelErr
	default:
		*l = LevelNone
	}
	return nil
}

// Summary is what a plugin shows in its block on dock's home screen.
//
// The simple form is Lines: a few short lines of plain text (no ANSI codes,
// no layout) plus an optional Level. Lines that don't fit the block are
// truncated.
//
// The rich form adds Widgets: structured bars, sparklines and charts that
// the host draws at the block's width, in the theme's colours. Lines then
// serve as the fallback, shown instead when the host can't draw the widgets
// (the block would be too tall or too narrow, or the terminal is tiny), so
// always set it to a meaningful text version.
type Summary struct {
	Lines   []string `json:"lines,omitempty"` // at most MaxSummaryLines; keep each under ~40 cells
	Level   Level    `json:"level,omitempty"`
	Widgets []Widget `json:"widgets,omitempty"` // optional; see Widget
}

// WidgetKind says what a Widget draws.
type WidgetKind string

const (
	KindText      WidgetKind = "text"      // one line of text
	KindProgress  WidgetKind = "progress"  // label, bar filled Value/Max, caption
	KindSparkline WidgetKind = "sparkline" // label and one row of tiny bars
	KindBarChart  WidgetKind = "barchart"  // labelled vertical bars
)

// Widget is one element of a rich Summary. Build them with Text, Progress,
// Sparkline and BarChart; the fields a kind doesn't use are ignored. The
// host owns width, colour and truncation. Numbers are data: NaN, infinities
// and negatives count as 0, and anything over the limits is cut.
type Widget struct {
	Kind    WidgetKind `json:"type"`
	Text    string     `json:"text,omitempty"`    // text: the line
	Label   string     `json:"label,omitempty"`   // progress, sparkline: left-hand label
	Caption string     `json:"caption,omitempty"` // progress: right-hand text (default "62%")
	Value   float64    `json:"value,omitempty"`   // progress: the amount
	Max     float64    `json:"max,omitempty"`     // progress, barchart: full scale (0: progress 1, barchart the largest bar)
	Values  []float64  `json:"values,omitempty"`  // sparkline: oldest first
	Bars    []Bar      `json:"bars,omitempty"`    // barchart
	Height  int        `json:"height,omitempty"`  // barchart: plot rows, 1-MaxChartHeight (default 4)
	Level   Level      `json:"level,omitempty"`   // colour of text, progress and sparkline
}

// Bar is one bar of a BarChart.
type Bar struct {
	Label   string  `json:"label,omitempty"`   // under the bar; cut to the bar's width
	Caption string  `json:"caption,omitempty"` // second line under the bar, e.g. its value
	Value   float64 `json:"value"`
	Level   Level   `json:"level,omitempty"` // colour; LevelNone uses the default (accent when Focus)
	Focus   bool    `json:"focus,omitempty"` // highlight (e.g. today): accent colour, bold label
}

// Text is a line of text, coloured by level.
func Text(text string, level Level) Widget { return Widget{Kind: KindText, Text: text, Level: level} }

// Progress is "label ████░░░░ caption", filled value/max of the way.
// An empty caption shows the percentage.
func Progress(label string, value, max float64, caption string, level Level) Widget {
	return Widget{Kind: KindProgress, Label: label, Value: value, Max: max, Caption: caption, Level: level}
}

// Sparkline is a label and one row of tiny bars, oldest value first. When
// there are more values than cells, the newest are kept.
func Sparkline(label string, level Level, values ...float64) Widget {
	return Widget{Kind: KindSparkline, Label: label, Values: values, Level: level}
}

// BarChart is vertical bars, height plot rows tall, with a label row (and a
// caption row when any bar has a caption) below. max is the value of a full
// bar (0: the largest bar); taller bars are clamped. The bars share the
// block's width.
func BarChart(height int, max float64, bars ...Bar) Widget {
	return Widget{Kind: KindBarChart, Height: height, Max: max, Bars: bars}
}

// Rows is how many terminal rows w takes (after Clean).
func (w Widget) Rows() int {
	if w.Kind != KindBarChart {
		return 1
	}
	n := w.Height + 1
	for _, b := range w.Bars {
		if b.Caption != "" {
			return n + 1
		}
	}
	return n
}

// Summarizer is an optional interface on the Plugin value (not the model),
// so the home screen can show a summary while no instance is open.
//
// The host calls Summary off the UI goroutine when the home screen is
// shown, then about every 30 seconds while it stays visible. It may run
// while an instance of the same plugin is open, and calls for different
// plugins run in parallel. So Summary must:
//
//   - be read-only and not depend on a running instance;
//   - be safe to call concurrently (with itself and with your model);
//   - be cheap: aim for under 100 ms. After 2 seconds the host gives up and
//     shows the block as not responding; a call that never returns stops
//     further calls for that plugin.
//
// A returned error is shown, muted, in the plugin's block. Panics are
// recovered and shown the same way.
type Summarizer interface {
	Summary(ctx Context) (Summary, error)
}

// Clean returns s with control characters and ANSI codes removed, embedded
// newlines split into separate lines, blank trailing lines dropped and at
// most MaxSummaryLines lines kept. Widgets are validated: unknown kinds are
// dropped, text is cleaned and cut, numbers are made finite and
// non-negative, slices and heights are clamped, and widgets past
// MaxWidgets or MaxSummaryRows rows are dropped. The host applies it to
// every summary.
func (s Summary) Clean() Summary {
	var lines []string
	for _, l := range s.Lines {
		for _, part := range strings.Split(l, "\n") {
			lines = append(lines, cleanLine(part))
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > MaxSummaryLines {
		lines = lines[:MaxSummaryLines]
	}
	return Summary{Lines: lines, Level: cleanLevel(s.Level), Widgets: cleanWidgets(s.Widgets)}
}

func cleanLevel(l Level) Level {
	if l < LevelNone || l > LevelErr {
		return LevelNone
	}
	return l
}

func cleanWidgets(in []Widget) []Widget {
	var out []Widget
	rows := 0
	for _, w := range in {
		if len(out) >= MaxWidgets {
			break
		}
		w, ok := cleanWidget(w)
		if !ok {
			continue
		}
		if rows+w.Rows() > MaxSummaryRows {
			break
		}
		rows += w.Rows()
		out = append(out, w)
	}
	return out
}

func cleanWidget(w Widget) (Widget, bool) {
	c := Widget{
		Kind:    w.Kind,
		Text:    cutRunes(cleanLine(firstLine(w.Text)), MaxWidgetLabel),
		Label:   cutRunes(cleanLine(firstLine(w.Label)), MaxWidgetLabel),
		Caption: cutRunes(cleanLine(firstLine(w.Caption)), MaxWidgetCaption),
		Value:   num(w.Value),
		Max:     num(w.Max),
		Level:   cleanLevel(w.Level),
	}
	switch w.Kind {
	case KindText:
		return c, true
	case KindProgress:
		if c.Max == 0 {
			c.Max = 1
		}
		return c, true
	case KindSparkline:
		vs := w.Values
		if len(vs) > MaxSparkValues {
			vs = vs[len(vs)-MaxSparkValues:] // newest
		}
		for _, v := range vs {
			c.Values = append(c.Values, num(v))
		}
		return c, len(c.Values) > 0
	case KindBarChart:
		c.Height = w.Height
		if c.Height <= 0 {
			c.Height = 4
		}
		c.Height = min(c.Height, MaxChartHeight)
		for _, b := range w.Bars {
			if len(c.Bars) >= MaxChartBars {
				break
			}
			c.Bars = append(c.Bars, Bar{
				Label:   cutRunes(cleanLine(firstLine(b.Label)), MaxWidgetCaption),
				Caption: cutRunes(cleanLine(firstLine(b.Caption)), MaxWidgetCaption),
				Value:   num(b.Value),
				Level:   cleanLevel(b.Level),
				Focus:   b.Focus,
			})
		}
		return c, len(c.Bars) > 0
	}
	return Widget{}, false
}

// num makes v a finite, non-negative number.
func num(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func cleanLine(l string) string {
	l = ansi.Strip(l)
	l = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, l)
	return strings.TrimRight(l, " ")
}
