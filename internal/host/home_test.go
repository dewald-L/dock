package host

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
)

// sumProbe is a tile probe with a Summary method.
type sumProbe struct {
	probe
	fn func() (plugin.Summary, error)
}

func (p sumProbe) Summary(plugin.Context) (plugin.Summary, error) { return p.fn() }

func withSummary(id string, fn func() (plugin.Summary, error)) sumProbe {
	return sumProbe{tile(id), fn}
}

func lines(ls ...string) func() (plugin.Summary, error) {
	return func() (plugin.Summary, error) { return plugin.Summary{Lines: ls}, nil }
}

// newHomeApp is newApp with a short summary timeout, so the fake runtime
// (which drops commands slower than 50ms) sees timeouts come back.
func newHomeApp(t *testing.T, cfg *config.Config, w, h int, native ...plugin.Plugin) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "cfg"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	if cfg == nil {
		cfg = &config.Config{}
	}
	a := New(Options{Config: cfg, Native: native, Version: "test"})
	a.sumTimeout = 20 * time.Millisecond
	run(a, a.Init())
	send(a, tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func frameAt(t *testing.T, a *App, w, h int) string {
	t.Helper()
	v := a.View()
	ls := strings.Split(v, "\n")
	if len(ls) != h {
		t.Fatalf("%dx%d: view has %d lines", w, h, len(ls))
	}
	for i, l := range ls {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("%dx%d: line %d is %d wide: %q", w, h, i, lw, ansi.Strip(l))
		}
	}
	return ansi.Strip(v)
}

func block(t *testing.T, v, title string) string {
	t.Helper()
	// the block's lines: from its title row down to its bottom border,
	// within its column
	ls := strings.Split(v, "\n")
	for y, l := range ls {
		x := strings.Index(l, " "+title+" ")
		if x < 0 {
			continue
		}
		col := len([]rune(l[:x]))
		var out []string
		for _, bl := range ls[y:] {
			r := []rune(bl)
			if col-2 < 0 || col-2 >= len(r) {
				break
			}
			end := col
			for end < len(r) && r[end] != '╮' && r[end] != '│' && r[end] != '╯' {
				end++
			}
			out = append(out, string(r[col-2:min(end+1, len(r))]))
			if r[col-2] == '╰' {
				break
			}
		}
		return strings.Join(out, "\n")
	}
	t.Fatalf("no block titled %q in:\n%s", title, v)
	return ""
}

// ---- summary fetching ----

func TestSummaryIsolation(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var goodCalls atomic.Int32
	var goodText atomic.Value
	goodText.Store("first")
	a := newHomeApp(t, nil, 100, 40,
		withSummary("good", func() (plugin.Summary, error) {
			goodCalls.Add(1)
			return plugin.Summary{Lines: []string{goodText.Load().(string), "\x1b[31mred\x1b[0m\tline"}, Level: plugin.LevelWarn}, nil
		}),
		withSummary("slow", func() (plugin.Summary, error) { <-release; return plugin.Summary{Lines: []string{"late"}}, nil }),
		withSummary("failing", func() (plugin.Summary, error) { return plugin.Summary{}, errors.New("disk on fire") }),
		withSummary("panicky", func() (plugin.Summary, error) { panic("kaboom") }),
		tile("plain"),
	)
	v := frameAt(t, a, 100, 40)
	if b := block(t, v, "[1] good"); !strings.Contains(b, "first") || !strings.Contains(b, "red line") || strings.Contains(b, "\x1b") {
		t.Fatalf("good block should show its cleaned lines:\n%s", b)
	}
	if !strings.Contains(block(t, v, "[1] good"), "●") {
		t.Fatal("a warn level shows a marker")
	}
	if b := block(t, v, "[2] slow"); !strings.Contains(b, "timed out") {
		t.Fatalf("slow block should time out on its own:\n%s", b)
	}
	if b := block(t, v, "[3] failing"); !strings.Contains(b, "disk on fire") {
		t.Fatalf("failing block should show its error:\n%s", b)
	}
	if b := block(t, v, "[4] panicky"); !strings.Contains(b, "panic: kaboom") {
		t.Fatalf("panicking block should show the panic:\n%s", b)
	}
	if b := block(t, v, "[5] plain"); strings.Contains(b, "…") || strings.Contains(b, "summary") {
		t.Fatalf("a plugin without Summarizer shows only its description:\n%s", b)
	}

	// refresh: the stuck call is still running, so it isn't called again,
	// and the others refresh normally
	goodText.Store("second")
	send(a, summaryTickMsg{a.homeGen})
	v = frameAt(t, a, 100, 40)
	if b := block(t, v, "[1] good"); !strings.Contains(b, "second") || goodCalls.Load() != 2 {
		t.Fatalf("good should refresh (calls=%d):\n%s", goodCalls.Load(), b)
	}
	if b := block(t, v, "[2] slow"); !strings.Contains(b, "not responding") {
		t.Fatalf("a stuck call should not be repeated:\n%s", b)
	}
}

func TestSummaryKeepsLastGood(t *testing.T) {
	var fail atomic.Bool
	a := newHomeApp(t, nil, 100, 40, withSummary("good", func() (plugin.Summary, error) {
		if fail.Load() {
			return plugin.Summary{}, errors.New("nope")
		}
		return plugin.Summary{Lines: []string{"all fine"}}, nil
	}))
	// while a refresh is in flight the last summary stays
	a.sums["good"].loading = true
	if b := block(t, frameAt(t, a, 100, 40), "[1] good"); !strings.Contains(b, "all fine") || strings.Contains(b, "…") {
		t.Fatalf("refreshing must not blank the block:\n%s", b)
	}
	a.sums["good"].loading = false
	fail.Store(true)
	send(a, summaryTickMsg{a.homeGen})
	b := block(t, frameAt(t, a, 100, 40), "[1] good")
	if !strings.Contains(b, "all fine") || !strings.Contains(b, "refresh failed: nope") {
		t.Fatalf("a failed refresh keeps the last good summary:\n%s", b)
	}
}

func TestSummaryLoadingShowsEllipsis(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, withSummary("good", lines("x")))
	delete(a.sums, "good")
	if b := block(t, frameAt(t, a, 100, 40), "[1] good"); !strings.Contains(b, "…") {
		t.Fatalf("before the first result the block shows …:\n%s", b)
	}
}

func TestSummaryRefreshStopsWhenHidden(t *testing.T) {
	var calls atomic.Int32
	a := newHomeApp(t, nil, 100, 40, withSummary("good", func() (plugin.Summary, error) {
		calls.Add(1)
		return plugin.Summary{Lines: []string{"hi"}}, nil
	}))
	if calls.Load() != 1 || !a.homeOn {
		t.Fatalf("showing home fetches once: calls=%d", calls.Load())
	}
	gen := a.homeGen
	keys(a, "1") // open it: home hidden
	if a.homeOn || len(a.tabs) != 1 {
		t.Fatal("opening a plugin hides home")
	}
	if cmd := a.onSummaryTick(summaryTickMsg{gen}); cmd != nil {
		t.Fatal("a tick from the hidden home screen must not schedule anything")
	}
	if cmd := a.onSummaryTick(summaryTickMsg{a.homeGen}); cmd != nil {
		t.Fatal("no refresh while home is hidden")
	}
	send(a, summaryTickMsg{gen})
	if calls.Load() != 1 {
		t.Fatalf("hidden home must not fetch: calls=%d", calls.Load())
	}
	keys(a, "ctrl+g", "x") // close the tab: home shows again
	if !a.homeOn || calls.Load() != 2 {
		t.Fatalf("home shown again fetches at once: calls=%d", calls.Load())
	}
	if cmd := a.onSummaryTick(summaryTickMsg{a.homeGen}); cmd == nil {
		t.Fatal("ticks of the current showing keep refreshing")
	}
	if cmd := a.onSummaryTick(summaryTickMsg{gen}); cmd != nil {
		t.Fatal("ticks of an earlier showing are ignored")
	}
}

func TestExecSummary(t *testing.T) {
	cfg := &config.Config{Exec: []config.ExecPlugin{
		{ID: "lines", Command: []string{"true"}, Summary: []string{"sh", "-c", `printf 'a\nb\nc\nd\n'`}},
		{ID: "broken", Command: []string{"true"}, Summary: []string{"sh", "-c", "echo bad config >&2; exit 3"}},
		{ID: "hang", Command: []string{"true"}, Summary: []string{"sh", "-c", "sleep 5"}},
		{ID: "none", Command: []string{"true"}},
	}}
	start := time.Now()
	a := newHomeApp(t, cfg, 100, 40)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a hanging summary command held things up for %v", d)
	}
	v := frameAt(t, a, 100, 40)
	if b := block(t, v, "[1] lines"); !strings.Contains(b, "a") || !strings.Contains(b, "c") || strings.Contains(b, "│ d") {
		t.Fatalf("exec summary shows the first 3 lines:\n%s", b)
	}
	if b := block(t, v, "[2] broken"); !strings.Contains(b, "bad config") {
		t.Fatalf("exec summary failure shows stderr:\n%s", b)
	}
	if b := block(t, v, "[3] hang"); !strings.Contains(b, "timed out") {
		t.Fatalf("a hanging command times out:\n%s", b)
	}
	if b := block(t, v, "[4] none"); strings.Contains(b, "…") {
		t.Fatalf("no summary command, no summary:\n%s", b)
	}
}

func TestSummaryClean(t *testing.T) {
	s := plugin.Summary{Lines: []string{"a\nb", "\x1b[1mc\x1b[0m\x07", "d", ""}, Level: 42}.Clean()
	if fmt.Sprint(s.Lines) != "[a b c]" || s.Level != plugin.LevelNone {
		t.Fatalf("Clean: %q level %v", s.Lines, s.Level)
	}
}

// ---- modal layout ----

func manyPlugins(n int) []plugin.Plugin {
	var ps []plugin.Plugin
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("p%02d", i)
		if i%2 == 1 {
			ps = append(ps, withSummary(id, lines("today 1h15 · week 18h15 / 40h", "M█ T▆ W▃ T· F· S· S·")))
		} else {
			ps = append(ps, tile(id))
		}
	}
	return ps
}

func TestHomeFits(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{60, 20}, {90, 24}, {160, 45}, {40, 10}, {20, 5}} {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			a := newHomeApp(t, nil, sz.w, sz.h, manyPlugins(10)...)
			frameAt(t, a, sz.w, sz.h)
			for range 12 {
				keys(a, "j")
				frameAt(t, a, sz.w, sz.h)
			}
			keys(a, "G")
			frameAt(t, a, sz.w, sz.h)
			keys(a, "?")
			frameAt(t, a, sz.w, sz.h)
			keys(a, "x", ":")
			frameAt(t, a, sz.w, sz.h)
			keys(a, "esc")
		})
	}
}

func TestHomeBlocksFillWidth(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{90, 24}, {160, 45}, {60, 20}} {
		a := newHomeApp(t, nil, sz.w, sz.h, manyPlugins(2)...)
		v := frameAt(t, a, sz.w, sz.h)
		b := strings.Split(block(t, v, "[1] p01"), "\n")
		if w := len([]rune(b[0])); w != sz.w-2 {
			t.Errorf("%dx%d: block %d wide, want %d", sz.w, sz.h, w, sz.w-2)
		}
		if !strings.Contains(v, "today 1h15") || !strings.Contains(v, "native · tile") || !strings.Contains(v, homeKeyHint) {
			t.Errorf("entries show summaries and kind hints, the footer its keys:\n%s", v)
		}
		if strings.Contains(v, "╭─ dock ") {
			t.Errorf("no modal frame expected:\n%s", v)
		}
	}
}

func TestHomeVerticalList(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, manyPlugins(3)...)
	v := frameAt(t, a, 100, 40)
	row := map[string]int{}
	for y, l := range strings.Split(v, "\n") {
		if n := strings.Count(l, "╭─ ["); n > 1 {
			t.Fatalf("one entry per row, got %d on %q", n, l)
		}
		for _, k := range []string{"[1] p01", "[2] p02", "[3] p03", "[4] Plugins"} {
			if strings.Contains(l, k) {
				row[k] = y
			}
		}
	}
	if !(row["[1] p01"] < row["[2] p02"] && row["[2] p02"] < row["[3] p03"] && row["[3] p03"] < row["[4] Plugins"]) {
		t.Fatalf("entries stack top to bottom: %v\n%s", row, v)
	}
}

func TestHomeKeys(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, manyPlugins(3)...) // p01..p03 then Plugins
	moves := []struct {
		key  string
		want int
	}{{"j", 1}, {"l", 1}, {"right", 1}, {"h", 1}, {"left", 1}, {"j", 2}, {"j", 3}, {"j", 3}, {"k", 2}, {"g", 0}, {"k", 0}, {"G", 3}, {"up", 2}, {"down", 3}}
	for i, m := range moves {
		keys(a, m.key)
		if a.homeSel != m.want {
			t.Fatalf("move %d (%s): sel=%d, want %d", i, m.key, a.homeSel, m.want)
		}
	}
	keys(a, "k", "k", "enter") // sel 1 = p02
	if a.findByPlugin("p02") == nil {
		t.Fatal("enter opens the selected entry")
	}
	keys(a, "ctrl+g", "x", "k", "o") // back home; sel 0 = p01
	if a.findByPlugin("p01") == nil {
		t.Fatal("o opens the selected entry")
	}
}

func TestHomeScrollsToSelection(t *testing.T) {
	a := newHomeApp(t, nil, 60, 20, manyPlugins(10)...)
	v := frameAt(t, a, 60, 20)
	if !strings.Contains(v, "↓ more") || strings.Contains(v, "↑ more") || strings.Contains(v, " Plugins ") {
		t.Fatalf("a long list scrolls:\n%s", v)
	}
	keys(a, "G")
	v = frameAt(t, a, 60, 20)
	if !strings.Contains(v, " Plugins ") || !strings.Contains(v, "↑ more") || strings.Contains(v, "[1] p01") {
		t.Fatalf("G brings the last entry into view:\n%s", v)
	}
	keys(a, "g")
	if v = frameAt(t, a, 60, 20); !strings.Contains(v, "[1] p01") || strings.Contains(v, "↑ more") {
		t.Fatalf("g goes back to the top:\n%s", v)
	}
}

func TestHomeTinyFallsBackToList(t *testing.T) {
	a := newHomeApp(t, nil, 36, 10, manyPlugins(3)...)
	v := frameAt(t, a, 36, 10)
	if strings.Contains(v, "╭") || !strings.Contains(v, "› [1] p01") || !strings.Contains(v, "[2] p02") {
		t.Fatalf("below 40 wide: a plain list:\n%s", v)
	}
	keys(a, "j", "enter")
	if a.findByPlugin("p02") == nil {
		t.Fatal("the plain list still opens the selection")
	}
}

// ---- number keys ----

func TestNumberKeysOpenPlugins(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, tile("alpha"), tile("beta"), tile("gamma"))
	for _, c := range []struct{ key, id string }{{"1", "alpha"}, {"2", "beta"}, {"3", "gamma"}, {"4", "plugins"}} {
		keys(a, c.key)
		if len(a.tabs) != 1 || a.focused().entry.Manifest.ID != c.id {
			t.Fatalf("%s should open %s", c.key, c.id)
		}
		keys(a, "ctrl+g", "x")
		if len(a.tabs) != 0 {
			t.Fatal("closing should go back home")
		}
	}
	for _, k := range []string{"5", "9", "0"} {
		keys(a, k)
		if len(a.tabs) != 0 {
			t.Fatalf("%s has no plugin and must do nothing", k)
		}
	}
}

func TestNoNumbersPastNine(t *testing.T) {
	a := newHomeApp(t, nil, 160, 45, manyPlugins(11)...)
	seen := frameAt(t, a, 160, 45)
	keys(a, "G")
	seen += frameAt(t, a, 160, 45)
	if !strings.Contains(seen, "[9] p09") || strings.Contains(seen, "[10]") || strings.Contains(seen, "[11]") ||
		!strings.Contains(seen, " p10 ") || !strings.Contains(seen, " Plugins ") {
		t.Fatalf("entries after the ninth have no number:\n%s", seen)
	}
	keys(a, "enter")
	if a.findByPlugin("plugins") == nil {
		t.Fatal("unnumbered entries open with enter")
	}
}

func TestDigitsInsideTabReachPlugin(t *testing.T) {
	a := newHomeApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, 100, 40, tile("alpha"), tile("beta"))
	keys(a, "ctrl+g", "1", "2", "7")
	if got := model(t, a, "alpha").keys; fmt.Sprint(got) != "[2 7]" || len(a.tabs) != 2 {
		t.Fatalf("digits in a tab go to the plugin, got %v (tabs %d)", got, len(a.tabs))
	}
	keys(a, "ctrl+g", "2")
	if a.active != 1 {
		t.Fatal("ctrl+g 2 still switches tabs")
	}
}

func TestNumbersStableWhileHomeOpen(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, tile("alpha"), tile("beta"), tile("gamma"))
	cfg, _ := config.Load()
	cfg.SetEnabled("alpha", false)
	cfg.Save()
	run(a, a.reload(""))
	v := frameAt(t, a, 100, 40)
	if strings.Contains(v, "alpha") || !strings.Contains(v, "[2] beta") || !strings.Contains(v, "[3] gamma") {
		t.Fatalf("numbers stay put while home is open:\n%s", v)
	}
	keys(a, "1")
	if len(a.tabs) != 0 {
		t.Fatal("a number whose plugin went away does nothing")
	}
	keys(a, "3")
	if a.findByPlugin("gamma") == nil {
		t.Fatal("3 still opens gamma")
	}
}

// ---- rich summaries (widgets) ----

func richSummary() (plugin.Summary, error) {
	bars := []plugin.Bar{
		{Label: "Mo", Caption: "8h", Value: 480},
		{Label: "Tu", Caption: "4h", Value: 240},
		{Label: "We", Caption: "8h15", Value: 495, Level: plugin.LevelWarn, Focus: true},
		{Label: "Th", Caption: "·"}, {Label: "Fr", Caption: "·"}, {Label: "Sa", Caption: "·"}, {Label: "Su", Caption: "·"},
	}
	return plugin.Summary{
		Lines: []string{"fallback text"},
		Level: plugin.LevelWarn,
		Widgets: []plugin.Widget{
			plugin.Text("Today 8h15 · this week 20h15 / 40h", plugin.LevelNone),
			plugin.BarChart(4, 480, bars...),
			plugin.Progress("week", 20.25, 40, "", plugin.LevelOk),
			plugin.Sparkline("spark", plugin.LevelNone, 1, 3, 2, 8),
		},
	}, nil
}

func TestRichSummaryAtWidths(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{60, 30}, {80, 30}, {160, 45}} {
		a := newHomeApp(t, nil, sz.w, sz.h, withSummary("rich", richSummary))
		v := frameAt(t, a, sz.w, sz.h)
		b := block(t, v, "[1] rich")
		for _, want := range []string{"Today 8h15", "████", "Mo", "8h15", "week", "51%"} {
			if !strings.Contains(b, want) {
				t.Errorf("%dx%d: block lacks %q:\n%s", sz.w, sz.h, want, b)
			}
		}
		if strings.Contains(b, "fallback text") || !strings.Contains(b, "●") {
			t.Errorf("%dx%d: rich form replaces Lines, level mark stays:\n%s", sz.w, sz.h, b)
		}
		if n := len(strings.Split(b, "\n")); n != 1+8+2 {
			t.Errorf("%dx%d: block is %d rows tall:\n%s", sz.w, sz.h, n, b)
		}
	}
}

func TestRichSummaryFallsBackToLines(t *testing.T) {
	// too short for the rich block: its Lines are shown, not a list
	a := newHomeApp(t, nil, 80, 12, withSummary("rich", richSummary))
	v := frameAt(t, a, 80, 12)
	if b := block(t, v, "[1] rich"); !strings.Contains(b, "fallback text") || strings.Contains(b, "█") {
		t.Fatalf("short terminal shows the text lines:\n%s", b)
	}
	// too narrow for a 7 bar chart (bars need 7 + 6 cells): same
	a = newHomeApp(t, nil, 40, 30, withSummary("rich", func() (plugin.Summary, error) {
		s, _ := richSummary()
		s.Widgets = s.Widgets[1:2]
		bs := s.Widgets[0].Bars
		s.Widgets[0].Bars = append(append(append([]plugin.Bar{}, bs...), bs...), bs...)
		return s, nil
	}))
	v = frameAt(t, a, 40, 30)
	if b := block(t, v, "[1] rich"); !strings.Contains(b, "fallback text") {
		t.Fatalf("too narrow for the chart shows the text lines:\n%s", b)
	}
	// plain list fallback (width < 40) shows the first text line
	a = newHomeApp(t, nil, 36, 12, withSummary("rich", richSummary))
	if v = frameAt(t, a, 36, 12); !strings.Contains(v, "fallback") {
		t.Fatalf("list fallback uses the first text line:\n%s", v)
	}
	// widgets only, no Lines: the list falls back to the text widgets
	a = newHomeApp(t, nil, 36, 12, withSummary("rich", func() (plugin.Summary, error) {
		s, _ := richSummary()
		s.Lines = nil
		return s, nil
	}))
	if v = frameAt(t, a, 36, 12); !strings.Contains(v, "Today 8h15") {
		t.Fatalf("list fallback derives text from text widgets:\n%s", v)
	}
}

func TestRichSummaryBadValues(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	long := strings.Repeat("W", 300)
	bars := make([]plugin.Bar, 100)
	for i := range bars {
		bars[i] = plugin.Bar{Label: long, Caption: long, Value: nan}
	}
	a := newHomeApp(t, nil, 90, 40, withSummary("evil", func() (plugin.Summary, error) {
		return plugin.Summary{Lines: []string{"ok"}, Widgets: []plugin.Widget{
			{Kind: "x"},
			{Kind: plugin.KindBarChart, Height: -3, Max: inf, Bars: bars},
			{Kind: plugin.KindProgress, Label: long, Value: nan, Max: -1, Caption: "\x1b[31m" + long},
			{Kind: plugin.KindSparkline, Label: long, Values: []float64{nan, inf, -1}},
			plugin.Text(long, 99),
		}}, nil
	}), tile("after"))
	v := frameAt(t, a, 90, 40) // every line exactly 90 wide, no panic
	if strings.Contains(v, "\x1b") || !strings.Contains(v, "[2] after") {
		t.Fatalf("bad data must not break the frame:\n%s", v)
	}
}

func TestRichSummaryFailuresStillWork(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := newHomeApp(t, nil, 100, 40,
		withSummary("rich", richSummary),
		withSummary("slow", func() (plugin.Summary, error) { <-release; return richSummary() }),
		withSummary("panicky", func() (plugin.Summary, error) { panic("kaboom") }),
	)
	v := frameAt(t, a, 100, 40)
	if !strings.Contains(block(t, v, "[1] rich"), "█") {
		t.Fatal("rich block drawn")
	}
	if b := block(t, v, "[2] slow"); !strings.Contains(b, "timed out") {
		t.Fatalf("timeout:\n%s", b)
	}
	if b := block(t, v, "[3] panicky"); !strings.Contains(b, "panic: kaboom") {
		t.Fatalf("panic:\n%s", b)
	}
}

func TestRichSummaryScrolls(t *testing.T) {
	var ps []plugin.Plugin
	for i := 1; i <= 4; i++ {
		ps = append(ps, withSummary(fmt.Sprintf("r%d", i), richSummary))
	}
	a := newHomeApp(t, nil, 90, 24, ps...)
	frameAt(t, a, 90, 24)
	for range 5 {
		keys(a, "j")
		frameAt(t, a, 90, 24)
	}
	keys(a, "G")
	frameAt(t, a, 90, 24)
}

func TestExecSummaryJSON(t *testing.T) {
	js := `{"lines":["fallback"],"widgets":[{"type":"barchart","height":2,"bars":[{"label":"a","value":1},{"label":"b","value":2}]}]}`
	cfg := &config.Config{Exec: []config.ExecPlugin{
		{ID: "json", Command: []string{"true"}, Summary: []string{"sh", "-c", "printf '%s' '" + js + "'"}},
	}}
	a := newHomeApp(t, cfg, 100, 40)
	v := frameAt(t, a, 100, 40)
	if b := block(t, v, "[1] json"); !strings.Contains(b, "█") || strings.Contains(b, "fallback") {
		t.Fatalf("exec JSON summary draws its widgets:\n%s", b)
	}
}

func TestSparklineWidget(t *testing.T) {
	a := newHomeApp(t, nil, 60, 20, withSummary("sp", func() (plugin.Summary, error) {
		return plugin.Summary{Widgets: []plugin.Widget{plugin.Sparkline("cpu", plugin.LevelOk, 0, 1, 2, 4, 8)}}, nil
	}))
	if b := block(t, frameAt(t, a, 60, 20), "[1] sp"); !strings.Contains(b, "cpu ▁▁▂▄█") {
		t.Fatalf("sparkline scales to the largest value:\n%s", b)
	}
}

// ---- columns widget (a 2x2 matrix is two of them) ----

func cell(title string, level plugin.Level, count int, notes ...string) plugin.Cell {
	return plugin.Cell{Title: title, Count: count, Lines: notes, Level: level}
}

func matrixSummary() (plugin.Summary, error) {
	return plugin.Summary{
		Lines: []string{"fallback text"},
		Widgets: []plugin.Widget{
			plugin.Columns(2,
				cell("Do now", plugin.LevelWarn, 3, "! ship the release", "reply to Sam", "water plants"),
				cell("Schedule", plugin.LevelNone, 1, "plan Q1")),
			plugin.Columns(2,
				cell("Delegate", plugin.LevelNone, 0),
				cell("Drop", plugin.LevelNone, 2, "tidy desk", "old inbox")),
			plugin.Text("2 to sort", plugin.LevelNone),
		},
	}, nil
}

func TestMatrixExactFrames(t *testing.T) {
	want := map[int]string{
		60: `╭─ [1] mx ───────────────────────────────────────────────╮
│                                                        │
│ Do now 3                    Schedule 1                 │
│ ! ship the release          plan Q1                    │
│ +2 more                                                │
│ Delegate                    Drop 2                     │
│ —                           tidy desk                  │
│                             old inbox                  │
│ 2 to sort                                              │
╰──────────────────────────────────────── native · tile ─╯`,
		41: `╭─ [1] mx ────────────────────────────╮
│                                     │
│ Do now 3           Schedule 1       │
│ ! ship the relea…  plan Q1          │
│ +2 more                             │
│ Delegate           Drop 2           │
│ —                  tidy desk        │
│                    old inbox        │
│ 2 to sort                           │
╰───────────────────── native · tile ─╯`,
	}
	for w, exp := range want {
		a := newHomeApp(t, nil, w, 30, withSummary("mx", matrixSummary))
		b := block(t, frameAt(t, a, w, 30), "[1] mx")
		if b != exp {
			t.Errorf("%d wide:\n%s\nwant:\n%s", w, b, exp)
		}
	}
	for _, sz := range []struct{ w, h int }{{80, 24}, {160, 45}} {
		a := newHomeApp(t, nil, sz.w, sz.h, withSummary("mx", matrixSummary))
		b := block(t, frameAt(t, a, sz.w, sz.h), "[1] mx")
		for _, want := range []string{"Do now 3", "Schedule 1", "Delegate", "Drop 2", "! ship the release", "—", "2 to sort"} {
			if !strings.Contains(b, want) {
				t.Errorf("%dx%d lacks %q:\n%s", sz.w, sz.h, want, b)
			}
		}
		if strings.Contains(b, "fallback") {
			t.Errorf("%dx%d: matrix replaces Lines:\n%s", sz.w, sz.h, b)
		}
	}
}

func TestMatrixOverflowAndUneven(t *testing.T) {
	// 5 notes offered into 3 rows, a count past the notes given, a cell
	// with fewer notes than rows, and an uneven number of cells
	a := newHomeApp(t, nil, 90, 30, withSummary("mx", func() (plugin.Summary, error) {
		return plugin.Summary{Lines: []string{"x"}, Widgets: []plugin.Widget{
			plugin.Columns(3,
				cell("Many", 0, 0, "n1", "n2", "n3", "n4", "n5"),
				cell("Counted", 0, 40, "c1", "c2"),
				cell("Exact", 0, 0, "e1", "e2", "e3")),
			plugin.Columns(0, cell("Alone", 0, 1, "only")),
		}}, nil
	}))
	b := block(t, frameAt(t, a, 90, 30), "[1] mx")
	for _, want := range []string{"Many 5", "n2", "+3 more", "Counted 40", "c2", "+38 more", "Exact 3", "e3", "Alone 1", "only"} {
		if !strings.Contains(b, want) {
			t.Errorf("lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "n3") || strings.Contains(b, "n4") {
		t.Errorf("notes past the rows are replaced by +N more:\n%s", b)
	}
	if n := len(strings.Split(b, "\n")); n != 1+1+4+4+1 { // title, description, 3+1 rows, 3+1 rows, border
		t.Errorf("block is %d rows:\n%s", n, b)
	}
}

func TestMatrixFallsBackToLines(t *testing.T) {
	// too short: the 8 rows plus description and border don't fit the list
	a := newHomeApp(t, nil, 80, 12, withSummary("mx", matrixSummary))
	if b := block(t, frameAt(t, a, 80, 12), "[1] mx"); !strings.Contains(b, "fallback text") || strings.Contains(b, "Schedule") {
		t.Fatalf("short terminal shows the text lines:\n%s", b)
	}
	// too narrow: 2 cells need 16+2+16 = 34 inner cells, a 4-cell row 70
	a = newHomeApp(t, nil, 40, 30, withSummary("mx", func() (plugin.Summary, error) {
		s, _ := matrixSummary()
		s.Widgets = s.Widgets[:1]
		s.Widgets[0].Cells = append(s.Widgets[0].Cells, cell("C", 0, 0), cell("D", 0, 0))
		return s, nil
	}))
	if b := block(t, frameAt(t, a, 40, 30), "[1] mx"); !strings.Contains(b, "fallback text") || strings.Contains(b, "Schedule") {
		t.Fatalf("narrow shows the text lines:\n%s", b)
	}
	// the plain list (width < 40) shows the first line
	a = newHomeApp(t, nil, 36, 12, withSummary("mx", matrixSummary))
	if v := frameAt(t, a, 36, 12); !strings.Contains(v, "fallback") || strings.Contains(v, "Schedule") {
		t.Fatalf("list fallback uses the first line:\n%s", v)
	}
}

func TestMatrixHostileValues(t *testing.T) {
	long := strings.Repeat("W", 300)
	wide := strings.Repeat("日本語", 40)
	var cells []plugin.Cell
	for i := 0; i < 20; i++ {
		cells = append(cells, plugin.Cell{
			Title: "\x1b[31m" + long, Count: -5 + i*1e6, Level: 99,
			Lines: []string{"\x1b[2Jred\x07\r\nsecond line", wide, "", "\t\x00tab", long, "extra"},
		})
	}
	a := newHomeApp(t, nil, 90, 40, withSummary("evil", func() (plugin.Summary, error) {
		return plugin.Summary{Lines: []string{"ok"}, Widgets: []plugin.Widget{
			{Kind: plugin.KindColumns, Height: -2, Cells: cells},
			{Kind: plugin.KindColumns, Height: 1 << 30, Cells: cells[:2]},
			{Kind: plugin.KindColumns}, // no cells: dropped
			plugin.Text("tail", 0),
		}}, nil
	}), tile("after"))
	v := frameAt(t, a, 90, 40) // every line exactly 90 wide, no panic
	if strings.Contains(v, "\x1b") || strings.Contains(v, "second line") || !strings.Contains(v, "[2] after") {
		t.Fatalf("hostile data must not break the frame:\n%s", v)
	}
}

func TestMatrixFailuresStillWork(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := newHomeApp(t, nil, 100, 40,
		withSummary("mx", matrixSummary),
		withSummary("slow", func() (plugin.Summary, error) { <-release; return matrixSummary() }),
		withSummary("panicky", func() (plugin.Summary, error) {
			s, _ := matrixSummary()
			_ = s.Widgets[5] // index out of range
			return s, nil
		}),
	)
	v := frameAt(t, a, 100, 40)
	if !strings.Contains(block(t, v, "[1] mx"), "Schedule 1") {
		t.Fatal("matrix drawn")
	}
	if b := block(t, v, "[2] slow"); !strings.Contains(b, "timed out") {
		t.Fatalf("timeout:\n%s", b)
	}
	if b := block(t, v, "[3] panicky"); !strings.Contains(b, "panic:") {
		t.Fatalf("panic:\n%s", b)
	}
}
