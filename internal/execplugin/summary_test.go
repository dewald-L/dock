package execplugin

import (
	"strings"
	"testing"

	"github.com/dewald-L/dock/plugin"
)

func TestParseSummary(t *testing.T) {
	if s := ParseSummary("a\nb\nc\nd\n"); strings.Join(s.Lines, "|") != "a|b|c" || s.Widgets != nil {
		t.Fatalf("plain lines keep working: %+v", s)
	}
	if s := ParseSummary(""); s.Lines != nil {
		t.Fatalf("empty output: %+v", s)
	}
	s := ParseSummary(`{"lines":["fallback"],"level":"ok","widgets":[{"type":"sparkline","values":[1,2,3]}]}` + "\n")
	if s.Level != plugin.LevelOk || len(s.Widgets) != 1 || s.Widgets[0].Kind != plugin.KindSparkline || s.Lines[0] != "fallback" {
		t.Fatalf("json form: %+v", s)
	}
	// "{" that isn't valid JSON stays plain text
	if s := ParseSummary("{not json}\nsecond"); len(s.Lines) != 2 || s.Lines[0] != "{not json}" {
		t.Fatalf("invalid JSON is plain text: %+v", s)
	}
}

func TestParseSummaryColumns(t *testing.T) {
	s := ParseSummary(`{"lines":["x"],"widgets":[{"type":"columns","height":2,"cells":[` +
		`{"title":"Do","count":3,"lines":["a","b"],"level":"warn"},{"title":"Drop"}]}]}`)
	if len(s.Widgets) != 1 || s.Widgets[0].Kind != plugin.KindColumns || s.Widgets[0].Height != 2 {
		t.Fatalf("%+v", s.Widgets)
	}
	c := s.Clean().Widgets[0].Cells
	if len(c) != 2 || c[0].Title != "Do" || c[0].Count != 3 || c[0].Level != plugin.LevelWarn || len(c[0].Lines) != 2 || c[1].Title != "Drop" {
		t.Fatalf("%+v", c)
	}
}
