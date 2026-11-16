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
