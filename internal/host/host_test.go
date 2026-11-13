package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
	"github.com/dewald-L/dock/plugins/scratch"
)

// ---- a configurable test plugin ----

type (
	pingMsg  struct{ n int }
	panicMsg struct{}
	quitMsg  struct{}
	batchMsg struct{}
	seqMsg   struct{}
)

type probe struct {
	m plugin.Manifest
}

func (p probe) Manifest() plugin.Manifest { return p.m }
func (p probe) New(plugin.Context) (tea.Model, error) {
	return &probeModel{id: p.m.ID}, nil
}

type probeModel struct {
	id      string
	w, h    int
	focused bool
	pings   []int
	keys    []string
	closed  *bool
}

func (m *probeModel) Init() tea.Cmd { return nil }
func (m *probeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case plugin.FocusMsg:
		m.focused = true
	case plugin.BlurMsg:
		m.focused = false
	case pingMsg:
		m.pings = append(m.pings, msg.n)
	case panicMsg:
		panic("boom")
	case quitMsg:
		return m, tea.Quit
	case batchMsg:
		return m, tea.Batch(ping(1), ping(2))
	case seqMsg:
		return m, tea.Sequence(ping(3), ping(4))
	case tea.KeyMsg:
		m.keys = append(m.keys, msg.String())
		if msg.String() == "P" {
			return m, ping(99)
		}
	}
	return m, nil
}
func (m *probeModel) View() string {
	// deliberately too wide and too tall: the host must clip
	return strings.Repeat(m.id+" ", 100) + strings.Repeat("\nline", 100)
}

func ping(n int) tea.Cmd { return func() tea.Msg { return pingMsg{n} } }

func tile(id string) probe {
	return probe{plugin.Manifest{ID: id, Name: id, Display: plugin.DisplayTile}}
}

// ---- driver ----

const W, H = 100, 30

func newApp(t *testing.T, cfg *config.Config, native ...plugin.Plugin) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "cfg"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	if cfg == nil {
		cfg = &config.Config{}
	}
	a := New(Options{Config: cfg, Native: native, Version: "test"})
	run(a, a.Init())
	send(a, tea.WindowSizeMsg{Width: W, Height: H})
	return a
}

// run executes cmd and feeds results back into the app, like the Bubble
// Tea runtime would. Slow commands (timers) are dropped.
func run(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(50 * time.Millisecond):
		return
	}
	if msg == nil {
		return
	}
	if cmds, _, ok := unpackCmds(msg); ok {
		for _, c := range cmds {
			run(a, c)
		}
		return
	}
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		return
	}
	send(a, msg)
}

func send(a *App, msg tea.Msg) {
	_, cmd := a.Update(msg)
	run(a, cmd)
}

func keys(a *App, ks ...string) {
	for _, k := range ks {
		var m tea.KeyMsg
		switch k {
		case "ctrl+g":
			m = tea.KeyMsg{Type: tea.KeyCtrlG}
		case "enter":
			m = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			m = tea.KeyMsg{Type: tea.KeyEsc}
		case " ":
			m = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			m = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		send(a, m)
	}
}

func typeText(a *App, s string) {
	for _, r := range s {
		keys(a, string(r))
	}
}

func model(t *testing.T, a *App, pluginID string) *probeModel {
	t.Helper()
	in := a.findByPlugin(pluginID)
	if in == nil {
		t.Fatalf("%s is not open", pluginID)
	}
	return in.model.(*probeModel)
}

func checkFrame(t *testing.T, a *App) string {
	t.Helper()
	v := a.View()
	lines := strings.Split(v, "\n")
	if len(lines) != H {
		t.Fatalf("view has %d lines, want %d", len(lines), H)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != W {
			t.Fatalf("line %d is %d wide, want %d: %q", i, w, W, ansi.Strip(l))
		}
	}
	return ansi.Strip(v)
}

// ---- tests ----

func TestHomeOpenAndSize(t *testing.T) {
	a := newApp(t, nil, tile("alpha"))
	if v := checkFrame(t, a); !strings.Contains(v, "alpha") || !strings.Contains(v, "Plugins") {
		t.Fatalf("home should list plugins:\n%s", v)
	}
	keys(a, "j", "enter") // entries: plugins, alpha
	m := model(t, a, "alpha")
	if m.w != W || m.h != H-2 || !m.focused {
		t.Fatalf("alpha got %dx%d focused=%v, want %dx%d focused", m.w, m.h, m.focused, W, H-2)
	}
	v := checkFrame(t, a)
	if !strings.Contains(strings.Split(v, "\n")[0], "1 alpha") {
		t.Fatalf("tab bar: %q", strings.Split(v, "\n")[0])
	}
	keys(a, "x")
	if got := model(t, a, "alpha").keys; len(got) != 1 || got[0] != "x" {
		t.Fatalf("plain keys must reach the plugin, got %v", got)
	}
}

func TestSplitTilesAndFullRefusesSplit(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha"}}, tile("alpha"), tile("beta"))
	keys(a, "ctrl+g", "v")
	typeText(a, "beta")
	keys(a, "enter")
	al, be := model(t, a, "alpha"), model(t, a, "beta")
	if len(a.tabs) != 1 || len(a.tabs[0].panes) != 2 {
		t.Fatalf("want one tab with two panes, got %d tabs", len(a.tabs))
	}
	if al.w != W/2-2 || be.h != H-4 || al.focused || !be.focused {
		t.Fatalf("split sizes/focus wrong: alpha %dx%d f=%v beta %dx%d f=%v", al.w, al.h, al.focused, be.w, be.h, be.focused)
	}
	checkFrame(t, a)

	keys(a, "ctrl+g", "tab")
	if !al.focused || be.focused {
		t.Fatal("ctrl+g tab should move focus to alpha")
	}
	keys(a, "ctrl+g", "z")
	if al.w != W || al.h != H-2 {
		t.Fatalf("zoom should give alpha the whole area, got %dx%d", al.w, al.h)
	}
	keys(a, "ctrl+g", "z", "ctrl+g", "r")
	if al.w != W-2 || al.h != (H-2)/2-2 {
		t.Fatalf("rows layout: alpha %dx%d", al.w, al.h)
	}
	checkFrame(t, a)

	// the manager is DisplayFull: split must fall back to a new tab
	send(a, envelope{a.findByPlugin("alpha").id, plugin.OpenMsg{ID: "plugins", Split: true}})
	if len(a.tabs) != 2 || a.active != 1 {
		t.Fatalf("full plugin should open in a new tab, tabs=%d active=%d", len(a.tabs), a.active)
	}
	if v := checkFrame(t, a); !strings.Contains(v, "can't share this tab") {
		t.Fatal("expected a status note about the new tab")
	}
}

func TestSingletonFocusesExisting(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, tile("alpha"), tile("beta"))
	if a.active != 0 {
		t.Fatalf("startup should land on the first tab, got %d", a.active)
	}
	keys(a, "ctrl+g", "o")
	typeText(a, "beta")
	keys(a, "enter")
	if len(a.tabs) != 2 || a.active != 1 {
		t.Fatalf("opening an open singleton should focus it: tabs=%d active=%d", len(a.tabs), a.active)
	}
}

func TestTransientClosedWhenLeft(t *testing.T) {
	tr := probe{plugin.Manifest{ID: "form", Name: "form", Display: plugin.DisplayFull, Lifecycle: plugin.Transient}}
	a := newApp(t, &config.Config{Startup: []string{"alpha", "form"}}, tile("alpha"), tr)
	keys(a, "ctrl+g", "2")
	if a.findByPlugin("form") == nil {
		t.Fatal("form should be open while visible")
	}
	keys(a, "ctrl+g", "1")
	if a.findByPlugin("form") != nil || len(a.tabs) != 1 {
		t.Fatal("transient plugin should close when its tab is left")
	}
}

func TestRoutingAddressesHiddenInstances(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, tile("alpha"), tile("beta"))
	al := model(t, a, "alpha")
	keys(a, "ctrl+g", "2") // beta focused, alpha hidden
	send(a, envelope{a.findByPlugin("alpha").id, batchMsg{}})
	send(a, envelope{a.findByPlugin("alpha").id, seqMsg{}})
	if fmt.Sprint(al.pings) != "[1 2 3 4]" && fmt.Sprint(al.pings) != "[2 1 3 4]" {
		t.Fatalf("batch/sequence results should reach hidden alpha, got %v", al.pings)
	}
	be := model(t, a, "beta")
	keys(a, "P")
	if fmt.Sprint(be.pings) != "[99]" || len(al.pings) != 4 {
		t.Fatalf("cmd result should return to its sender: beta=%v alpha=%v", be.pings, al.pings)
	}
}

func TestQuitClosesOnlyThePlugin(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, tile("alpha"), tile("beta"))
	send(a, envelope{a.findByPlugin("alpha").id, quitMsg{}})
	if a.findByPlugin("alpha") != nil || a.findByPlugin("beta") == nil || len(a.tabs) != 1 {
		t.Fatal("tea.Quit from a plugin should close only that plugin")
	}
}

func TestPanicIsolated(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha", "beta"}}, tile("alpha"), tile("beta"))
	send(a, envelope{a.findByPlugin("alpha").id, panicMsg{}})
	if a.findByPlugin("alpha") != nil {
		t.Fatal("crashed plugin should be removed")
	}
	if v := checkFrame(t, a); !strings.Contains(v, "alpha crashed") {
		t.Fatalf("expected crash notice:\n%s", v)
	}
	if !model(t, a, "beta").focused {
		t.Fatal("beta should be focused after alpha crashed")
	}
}

func TestLeaderTwiceForwardsLeader(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha"}}, tile("alpha"))
	keys(a, "ctrl+g", "ctrl+g")
	if got := model(t, a, "alpha").keys; len(got) != 1 || got[0] != "ctrl+g" {
		t.Fatalf("want ctrl+g forwarded, got %v", got)
	}
}

func TestManagerDisableClosesInstance(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"alpha"}}, tile("alpha"))
	keys(a, "ctrl+g", "m") // manager opens in tab 2, cursor on "plugins"
	checkFrame(t, a)
	keys(a, "j", " ") // select alpha, toggle
	if a.findByPlugin("alpha") != nil {
		t.Fatal("disabling alpha should close it")
	}
	cfg, _ := config.Load()
	if !cfg.IsDisabled("alpha") {
		t.Fatal("disable should be saved to the config")
	}
	keys(a, " ") // re-enable
	if cfg, _ = config.Load(); cfg.IsDisabled("alpha") {
		t.Fatal("enable should be saved")
	}
}

func TestScratchSavesOnClose(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"scratch"}}, scratch.Plugin{})
	typeText(a, "hello dock")
	checkFrame(t, a)
	keys(a, "ctrl+g", "x")
	b, err := os.ReadFile(filepath.Join(config.PluginDataDir("scratch"), "scratch.md"))
	if err != nil || string(b) != "hello dock" {
		t.Fatalf("scratch should save on close: %q %v", b, err)
	}
}

func TestPaletteRunsPluginCommands(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"scratch"}}, scratch.Plugin{})
	typeText(a, "abc")
	keys(a, "ctrl+g", ":")
	typeText(a, "clear")
	if v := checkFrame(t, a); !strings.Contains(v, "Scratch: clear") {
		t.Fatalf("palette should list plugin commands:\n%s", v)
	}
	keys(a, "enter")
	if v := checkFrame(t, a); strings.Contains(v, "abc") {
		t.Fatal("clear command should have emptied the pad")
	}
}

func TestTinyTerminal(t *testing.T) {
	a := newApp(t, &config.Config{Startup: []string{"scratch"}}, scratch.Plugin{})
	for _, sz := range [][2]int{{10, 3}, {1, 1}, {30, 2}} {
		send(a, tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		v := a.View()
		if n := len(strings.Split(v, "\n")); n != sz[1] {
			t.Fatalf("%dx%d: %d lines", sz[0], sz[1], n)
		}
	}
}
