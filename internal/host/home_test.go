package host

import (
	"errors"
	"fmt"
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
	a := newHomeApp(t, nil, 100, 30,
		withSummary("good", func() (plugin.Summary, error) {
			goodCalls.Add(1)
			return plugin.Summary{Lines: []string{goodText.Load().(string), "\x1b[31mred\x1b[0m\tline"}, Level: plugin.LevelWarn}, nil
		}),
		withSummary("slow", func() (plugin.Summary, error) { <-release; return plugin.Summary{Lines: []string{"late"}}, nil }),
		withSummary("failing", func() (plugin.Summary, error) { return plugin.Summary{}, errors.New("disk on fire") }),
		withSummary("panicky", func() (plugin.Summary, error) { panic("kaboom") }),
		tile("plain"),
	)
	v := frameAt(t, a, 100, 30)
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
	v = frameAt(t, a, 100, 30)
	if b := block(t, v, "[1] good"); !strings.Contains(b, "second") || goodCalls.Load() != 2 {
		t.Fatalf("good should refresh (calls=%d):\n%s", goodCalls.Load(), b)
	}
	if b := block(t, v, "[2] slow"); !strings.Contains(b, "not responding") {
		t.Fatalf("a stuck call should not be repeated:\n%s", b)
	}
}

func TestSummaryKeepsLastGood(t *testing.T) {
	var fail atomic.Bool
	a := newHomeApp(t, nil, 100, 30, withSummary("good", func() (plugin.Summary, error) {
		if fail.Load() {
			return plugin.Summary{}, errors.New("nope")
		}
		return plugin.Summary{Lines: []string{"all fine"}}, nil
	}))
	// while a refresh is in flight the last summary stays
	a.sums["good"].loading = true
	if b := block(t, frameAt(t, a, 100, 30), "[1] good"); !strings.Contains(b, "all fine") || strings.Contains(b, "…") {
		t.Fatalf("refreshing must not blank the block:\n%s", b)
	}
	a.sums["good"].loading = false
	fail.Store(true)
	send(a, summaryTickMsg{a.homeGen})
	b := block(t, frameAt(t, a, 100, 30), "[1] good")
	if !strings.Contains(b, "all fine") || !strings.Contains(b, "refresh failed: nope") {
		t.Fatalf("a failed refresh keeps the last good summary:\n%s", b)
	}
}

func TestSummaryLoadingShowsEllipsis(t *testing.T) {
	a := newHomeApp(t, nil, 100, 30, withSummary("good", lines("x")))
	delete(a.sums, "good")
	if b := block(t, frameAt(t, a, 100, 30), "[1] good"); !strings.Contains(b, "…") {
		t.Fatalf("before the first result the block shows …:\n%s", b)
	}
}

func TestSummaryRefreshStopsWhenHidden(t *testing.T) {
	var calls atomic.Int32
	a := newHomeApp(t, nil, 100, 30, withSummary("good", func() (plugin.Summary, error) {
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
	a := newHomeApp(t, cfg, 100, 30)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a hanging summary command held things up for %v", d)
	}
	v := frameAt(t, a, 100, 30)
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

// ---- grid layout ----

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

func TestHomeGridFits(t *testing.T) {
	for _, sz := range []struct{ w, h, cols int }{{60, 20, 1}, {90, 24, 2}, {160, 45, 3}} {
		t.Run(fmt.Sprintf("%dx%d", sz.w, sz.h), func(t *testing.T) {
			a := newHomeApp(t, nil, sz.w, sz.h, manyPlugins(5)...)
			v := frameAt(t, a, sz.w, sz.h)
			var row1 string
			for _, l := range strings.Split(v, "\n") {
				if strings.Contains(l, "[1] p01") {
					row1 = l
				}
			}
			got := strings.Count(row1, "╭")
			if got != sz.cols {
				t.Fatalf("want %d blocks per row, got %d:\n%s", sz.cols, got, v)
			}
			if !strings.Contains(v, "today 1h15") || !strings.Contains(v, "native · tile") {
				t.Fatalf("blocks show the summary and kind hint:\n%s", v)
			}
			// every selection and the palette and help overlays still fit
			for range 8 {
				keys(a, "l", "j")
				frameAt(t, a, sz.w, sz.h)
			}
			keys(a, "?")
			frameAt(t, a, sz.w, sz.h)
			keys(a, "x", ":")
			frameAt(t, a, sz.w, sz.h)
			keys(a, "esc")
		})
	}
}

func TestHomeGridKeys(t *testing.T) {
	a := newHomeApp(t, nil, 100, 40, manyPlugins(5)...) // 2 columns; p01..p05 then Plugins
	moves := []struct {
		key  string
		want int
	}{{"l", 1}, {"l", 1}, {"j", 3}, {"h", 2}, {"h", 2}, {"k", 0}, {"k", 0}, {"j", 2}, {"j", 4}, {"j", 4}, {"l", 5}, {"l", 5}, {"G", 5}, {"g", 0}, {"down", 2}, {"right", 3}, {"up", 1}, {"left", 0}}
	for i, m := range moves {
		keys(a, m.key)
		if a.homeSel != m.want {
			t.Fatalf("move %d (%s): sel=%d, want %d", i, m.key, a.homeSel, m.want)
		}
	}
	// with an odd count, j from the right column drops to the last block
	b := newHomeApp(t, nil, 100, 40, manyPlugins(4)...)
	keys(b, "l", "j", "j")
	if b.homeSel != 4 {
		t.Fatalf("j into a shorter last row: sel=%d, want 4", b.homeSel)
	}
	v := frameAt(t, a, 100, 40)
	if !strings.Contains(v, "[6] Plugins") {
		t.Fatalf("the built-in manager comes last:\n%s", v)
	}
	keys(a, "j", "l", "enter") // sel 3 = p04
	if a.findByPlugin("p04") == nil {
		t.Fatal("enter opens the selected block")
	}
	keys(a, "ctrl+g", "x", "k", "o") // back home; sel 1 = p02
	if a.findByPlugin("p02") == nil {
		t.Fatal("o opens the selected block")
	}
}

func TestHomeScrollsToSelection(t *testing.T) {
	a := newHomeApp(t, nil, 60, 20, manyPlugins(8)...)
	v := frameAt(t, a, 60, 20)
	if !strings.Contains(v, "↓ more") || strings.Contains(v, "[9] Plugins") {
		t.Fatalf("a long list scrolls:\n%s", v)
	}
	keys(a, "G")
	v = frameAt(t, a, 60, 20)
	if !strings.Contains(v, "[9] Plugins") || !strings.Contains(v, "↑ more") || strings.Contains(v, "[1] p01") {
		t.Fatalf("the selection is scrolled into view:\n%s", v)
	}
	keys(a, "g")
	if v = frameAt(t, a, 60, 20); !strings.Contains(v, "[1] p01") {
		t.Fatalf("back to the top:\n%s", v)
	}
}

// ---- number keys ----

func TestNumberKeysOpenPlugins(t *testing.T) {
	a := newHomeApp(t, nil, 100, 30, tile("alpha"), tile("beta"), tile("gamma"))
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
	v := frameAt(t, a, 160, 45)
	if !strings.Contains(v, "[9] p09") || strings.Contains(v, "[10]") || !strings.Contains(v, " p10 ") || !strings.Contains(v, " Plugins ") {
		t.Fatalf("blocks after the ninth have no number:\n%s", v)
	}
	keys(a, "G", "enter")
	if a.findByPlugin("plugins") == nil {
		t.Fatal("unnumbered blocks open with enter")
	}
}

func TestDigitsInsideTabReachPlugin(t *testing.T) {
	a := newHomeApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, 100, 30, tile("alpha"), tile("beta"))
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
	a := newHomeApp(t, nil, 100, 30, tile("alpha"), tile("beta"), tile("gamma"))
	cfg, _ := config.Load()
	cfg.SetEnabled("alpha", false)
	cfg.Save()
	run(a, a.reload(""))
	v := frameAt(t, a, 100, 30)
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
