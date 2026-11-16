package plugin

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCleanLinesUnchanged(t *testing.T) {
	s := Summary{Lines: []string{"a\nb", "\x1b[1mc\x1b[0m\x07", "d", ""}, Level: 42}.Clean()
	if strings.Join(s.Lines, "|") != "a|b|c" || s.Level != LevelNone || s.Widgets != nil {
		t.Fatalf("Clean: %q %v %v", s.Lines, s.Level, s.Widgets)
	}
}

func TestCleanWidgetsClamps(t *testing.T) {
	long := strings.Repeat("x", 500)
	nan := math.NaN()
	s := Summary{Widgets: []Widget{
		Text("\x1b[31mhi\x1b[0m\nsecond line", Level(99)),
		{Kind: "bogus"},
		Progress(long, -5, 0, long, LevelOk),
		Sparkline("s", LevelNone, nan, math.Inf(1), -1, 3),
		Sparkline("empty", LevelNone),
	}}.Clean()
	if len(s.Widgets) != 3 {
		t.Fatalf("bogus and empty widgets are dropped: %+v", s.Widgets)
	}
	if w := s.Widgets[0]; w.Text != "hi" || w.Level != LevelNone {
		t.Fatalf("text: %+v", w)
	}
	if w := s.Widgets[1]; len([]rune(w.Label)) != MaxWidgetLabel || len([]rune(w.Caption)) != MaxWidgetCaption || w.Value != 0 || w.Max != 1 {
		t.Fatalf("progress: %+v", w)
	}
	if w := s.Widgets[2]; len(w.Values) != 4 || w.Values[0] != 0 || w.Values[1] != 0 || w.Values[2] != 0 || w.Values[3] != 3 {
		t.Fatalf("sparkline values must be finite and non-negative: %v", w.Values)
	}
	s = Summary{Widgets: []Widget{BarChart(99, nan, Bar{Label: long, Value: nan}, Bar{Value: math.Inf(-1)})}}.Clean()
	if w := s.Widgets[0]; w.Height != MaxChartHeight || w.Max != 0 || w.Bars[0].Value != 0 || w.Bars[1].Value != 0 || len([]rune(w.Bars[0].Label)) > MaxWidgetCaption {
		t.Fatalf("barchart: %+v", w)
	}
}

func TestCleanWidgetLimits(t *testing.T) {
	// too many values: the newest are kept
	vs := make([]float64, MaxSparkValues+10)
	for i := range vs {
		vs[i] = float64(i)
	}
	s := Summary{Widgets: []Widget{Sparkline("", LevelNone, vs...)}}.Clean()
	if got := s.Widgets[0].Values; len(got) != MaxSparkValues || got[len(got)-1] != float64(len(vs)-1) {
		t.Fatalf("sparkline keeps the newest %d values, got %d", MaxSparkValues, len(got))
	}
	// too many bars
	bars := make([]Bar, 100)
	s = Summary{Widgets: []Widget{BarChart(2, 1, bars...)}}.Clean()
	if len(s.Widgets[0].Bars) != MaxChartBars {
		t.Fatalf("bars: %d", len(s.Widgets[0].Bars))
	}
	// too many widgets, and too many rows
	var ws []Widget
	for range 50 {
		ws = append(ws, Text("x", LevelNone))
	}
	if n := len(Summary{Widgets: ws}.Clean().Widgets); n != MaxWidgets {
		t.Fatalf("widgets: %d", n)
	}
	s = Summary{Widgets: []Widget{Text("a", 0), BarChart(6, 1, Bar{Value: 1}), BarChart(6, 1, Bar{Value: 1})}}.Clean()
	rows := 0
	for _, w := range s.Widgets {
		rows += w.Rows()
	}
	if len(s.Widgets) != 2 || rows > MaxSummaryRows {
		t.Fatalf("rows must stay within %d: %d widgets, %d rows", MaxSummaryRows, len(s.Widgets), rows)
	}
}

func TestWidgetRows(t *testing.T) {
	c := BarChart(4, 0, Bar{Label: "a"})
	if c.Rows() != 5 {
		t.Fatalf("plot + labels: %d", c.Rows())
	}
	c.Bars[0].Caption = "1h"
	if c.Rows() != 6 {
		t.Fatalf("plot + labels + captions: %d", c.Rows())
	}
}

func TestSummaryJSON(t *testing.T) {
	in := `{"lines":["x"],"level":"warn","widgets":[{"type":"barchart","height":3,"max":8,"bars":[{"label":"Mo","value":9,"level":"err","focus":true}]},{"type":"progress","value":1,"level":"nonsense"}]}`
	var s Summary
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatal(err)
	}
	if s.Level != LevelWarn || s.Widgets[0].Bars[0].Level != LevelErr || !s.Widgets[0].Bars[0].Focus || s.Widgets[1].Level != LevelNone {
		t.Fatalf("decoded: %+v", s)
	}
	out, _ := json.Marshal(s)
	if !strings.Contains(string(out), `"level":"warn"`) {
		t.Fatalf("levels encode as names: %s", out)
	}
}

func TestCleanColumns(t *testing.T) {
	long := strings.Repeat("x", 300)
	var cells []Cell
	for i := 0; i < 9; i++ {
		cells = append(cells, Cell{Title: "\x1b[31mT\nsecond", Count: -4, Level: 99,
			Lines: []string{"a\x07b", "", "\x1b[2K", long, "c", "d", "e", "f"}})
	}
	cells[1].Count = 1 << 40
	cells[2] = Cell{} // an empty cell stays
	s := Summary{Widgets: []Widget{{Kind: KindColumns, Height: 99, Cells: cells}}}.Clean()
	w := s.Widgets[0]
	if len(w.Cells) != MaxCells || w.Height != MaxCellLines || w.Rows() != 1+MaxCellLines {
		t.Fatalf("cells %d height %d rows %d", len(w.Cells), w.Height, w.Rows())
	}
	c := w.Cells[0]
	if c.Title != "T" || c.Level != LevelNone || len(c.Lines) != MaxCellLines || c.Lines[0] != "ab" ||
		len([]rune(c.Lines[1])) != MaxWidgetLabel || c.Lines[2] != "c" {
		t.Fatalf("cell not cleaned: %+v", c)
	}
	if c.Count != 6 { // blank and control-only notes don't count; the rest do
		t.Errorf("count is at least the notes offered: %d", c.Count)
	}
	if w.Cells[1].Count != MaxCellCount || w.Cells[2].Count != 0 || len(w.Cells[2].Lines) != 0 {
		t.Errorf("counts: %d, %d", w.Cells[1].Count, w.Cells[2].Count)
	}
	// default height, no cells = dropped, the 8 row budget applies
	s = Summary{Widgets: []Widget{Columns(0, Cell{Title: "a"}), {Kind: KindColumns}, Columns(3, Cell{}), Text("x", 0)}}.Clean()
	if len(s.Widgets) != 2 || s.Widgets[0].Height != 3 || s.Widgets[0].Rows()+s.Widgets[1].Rows() != MaxSummaryRows {
		t.Fatalf("defaults and budget: %+v", s.Widgets)
	}
}
