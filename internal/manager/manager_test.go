package manager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

const fixture = `{
  "version": 1,
  "plugins": [
    {"id": "lazy1on1", "name": "lazy1on1", "description": "Continuous 1:1 notes: people, meetings, goals, action items",
     "kind": "native", "package": "example.com/lazy1on1/dockplugin", "repo": "https://example.com/lazy1on1", "default": true},
    {"id": "osheet", "name": "OSheet", "description": "Log timesheet entries", "kind": "exec",
     "command": ["osheet"], "requires": "needs osheet on PATH"},
    {"id": "tool", "name": "Tool", "description": "Already installed", "kind": "exec", "command": ["tool"]}
  ]
}`

// sandbox points dock at temp dirs and an index served by httptest. It
// returns the server's hit counter.
func sandbox(t *testing.T) *atomic.Int32 {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCK_REGISTRY", srv.URL)
	cfg := &config.Config{Exec: []config.ExecPlugin{{ID: "tool", Name: "Tool", Command: []string{"tool"}}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return &hits
}

func newModel(t *testing.T) *model {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var reg *registry.Registry
	p := Plugin{Registry: func() *registry.Registry { return reg }}
	reg, _ = registry.Build(cfg, []plugin.Plugin{p}, nil)
	m, err := p.New(plugin.Context{Theme: plugin.DefaultTheme()})
	if err != nil {
		t.Fatal(err)
	}
	mm := m.(*model)
	mm.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm
}

// run executes cmd and every command it batches, returning the messages.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func key(m *model, k string) tea.Cmd {
	var msg tea.KeyMsg
	switch k {
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	_, cmd := m.Update(msg)
	return cmd
}

// deliver feeds every message cmd produces back into the model and returns
// the messages that aren't the model's own.
func deliver(m *model, cmd tea.Cmd) []tea.Msg {
	var other []tea.Msg
	for _, msg := range run(cmd) {
		switch msg.(type) {
		case availMsg, installedMsg:
			_, c := m.Update(msg)
			other = append(other, deliver(m, c)...)
		default:
			other = append(other, msg)
		}
	}
	return other
}

func screen(m *model) string { return ansi.Strip(m.View()) }

func TestAvailableLoadsAsynchronously(t *testing.T) {
	hits := sandbox(t)
	m := newModel(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must return the index fetch")
	}
	if hits.Load() != 0 {
		t.Fatal("fetched before the command ran")
	}
	s := screen(m)
	if !strings.Contains(s, "Available  loading…") {
		t.Fatalf("no loading state:\n%s", s)
	}
	// View and Update never fetch
	m.View()
	key(m, "j")
	key(m, "k")
	if hits.Load() != 0 {
		t.Fatal("View or Update fetched")
	}

	deliver(m, cmd)
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	s = screen(m)
	if strings.Contains(s, "loading") {
		t.Fatalf("still loading:\n%s", s)
	}
	if !strings.Contains(s, "lazy1on1") || !strings.Contains(s, "OSheet") {
		t.Fatalf("available entries missing:\n%s", s)
	}
	// "tool" is installed, so the index's entry for it is not listed
	if strings.Contains(s, "Already installed") {
		t.Fatalf("installed plugin listed as available:\n%s", s)
	}
	if av := m.available(); len(av) != 2 {
		t.Fatalf("available: %+v", av)
	}
}

func TestStaleResponseIgnored(t *testing.T) {
	sandbox(t)
	m := newModel(t)
	first := m.Init()
	second := m.load(true)
	deliver(m, second)
	m.loadErr = nil
	// the older request finishing late must not override anything
	msg := run(first)[0].(availMsg)
	msg.err = os.ErrNotExist
	m.Update(msg)
	if m.loadErr != nil || m.res == nil {
		t.Fatal("stale response applied")
	}
}

func TestAvailableErrorStaysInSection(t *testing.T) {
	sandbox(t)
	t.Setenv("DOCK_REGISTRY", filepath.Join(t.TempDir(), "missing.json"))
	m := newModel(t)
	other := deliver(m, m.Init())
	for _, msg := range other {
		if _, ok := msg.(plugin.NotifyMsg); ok {
			t.Fatalf("error went to the status bar: %+v", msg)
		}
	}
	s := screen(m)
	if !strings.Contains(s, "could not load the plugin index") {
		t.Fatalf("no error in the section:\n%s", s)
	}
	// the installed list still works
	if !strings.Contains(s, "Plugins") || !strings.Contains(s, "Tool") {
		t.Fatalf("installed list gone:\n%s", s)
	}
}

func TestInstallKey(t *testing.T) {
	sandbox(t)
	m := newModel(t)
	deliver(m, m.Init())

	// on an installed row, i does nothing but say so
	if msgs := run(key(m, "i")); len(msgs) != 1 || !strings.Contains(msgs[0].(plugin.NotifyMsg).Text, "already installed") {
		t.Fatalf("i on installed: %+v", msgs)
	}

	key(m, "G") // last available entry: osheet
	key(m, "k") // lazy1on1
	if _, a := m.selection(); a == nil || a.ID != "lazy1on1" {
		t.Fatalf("selection: %+v", a)
	}
	if h := m.Help(); h[0].Key != "i" {
		t.Fatalf("hints on an available row: %+v", h)
	}
	if run(key(m, "enter"))[0].(plugin.NotifyMsg).Text != "lazy1on1 is not installed: press i to install it" {
		t.Fatal("enter on an available row")
	}

	msgs := run(key(m, "i"))
	x, ok := msgs[0].(plugin.ExecMsg)
	if !ok {
		t.Fatalf("i must hand the terminal over: %+v", msgs)
	}
	args := strings.Join(x.Cmd.Args, " ")
	if !strings.HasSuffix(args, "plugin install lazy1on1") || x.Cmd.Args[0] != "sh" {
		t.Fatalf("command: %s", args)
	}
	exe, _ := os.Executable()
	if !strings.Contains(args, exe) {
		t.Fatalf("does not run this dock binary: %s", args)
	}

	// the install finished
	var reloaded bool
	for _, msg := range deliver(m, func() tea.Msg { return x.Fn(nil) }) {
		if r, ok := msg.(ReloadMsg); ok && strings.Contains(r.Note, "restart dock") {
			reloaded = true
		}
	}
	if !reloaded {
		t.Fatal("no config reload after the install")
	}
	s := screen(m)
	if !strings.Contains(s, "installed lazy1on1: restart dock to load it") {
		t.Fatalf("no notice:\n%s", s)
	}
	if !strings.Contains(s, "installed · restart dock to load it") {
		t.Fatalf("entry not marked:\n%s", s)
	}
	if msgs := run(key(m, "i")); !strings.Contains(msgs[0].(plugin.NotifyMsg).Text, "restart dock") {
		t.Fatalf("second i: %+v", msgs)
	}
}

func TestInstallFailure(t *testing.T) {
	sandbox(t)
	m := newModel(t)
	deliver(m, m.Init())
	key(m, "G")
	x := run(key(m, "i"))[0].(plugin.ExecMsg)
	msgs := deliver(m, func() tea.Msg { return x.Fn(os.ErrPermission) })
	if len(msgs) != 1 || !msgs[0].(plugin.NotifyMsg).Error {
		t.Fatalf("%+v", msgs)
	}
	if s := screen(m); !strings.Contains(s, "install of osheet failed") || strings.Contains(s, "restart dock") {
		t.Fatalf("screen:\n%s", s)
	}
}

func TestRefreshKey(t *testing.T) {
	hits := sandbox(t)
	m := newModel(t)
	deliver(m, m.Init())
	// the second load would come from the cache...
	deliver(m, m.load(false))
	if hits.Load() != 1 || !m.res.Cached {
		t.Fatalf("cache not used: hits=%d", hits.Load())
	}
	if !strings.Contains(screen(m), "(cached, 0s)") {
		t.Fatalf("no cached note:\n%s", screen(m))
	}
	// ...but r bypasses it, and reloads the config too
	var reloaded bool
	for _, msg := range deliver(m, key(m, "r")) {
		if _, ok := msg.(ReloadMsg); ok {
			reloaded = true
		}
	}
	if hits.Load() != 2 || m.res.Cached || !reloaded {
		t.Fatalf("hits=%d cached=%v reloaded=%v", hits.Load(), m.res.Cached, reloaded)
	}
}

func TestViewFits(t *testing.T) {
	sandbox(t)
	for _, size := range [][2]int{{60, 20}, {120, 40}, {40, 8}, {200, 60}} {
		w, h := size[0], size[1]
		m := newModel(t)
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		check := func(when string) {
			t.Helper()
			lines := strings.Split(m.View(), "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d %s: %d lines", w, h, when, len(lines))
			}
			for i, l := range lines {
				if ansi.StringWidth(l) != w {
					t.Fatalf("%dx%d %s: line %d is %d wide: %q", w, h, when, i, ansi.StringWidth(l), ansi.Strip(l))
				}
			}
		}
		cmd := m.Init()
		check("loading")
		deliver(m, cmd)
		check("loaded")
		for range 6 {
			key(m, "j")
			check("moving")
			// the selection is always on screen
			if _, a := m.selection(); a != nil && !strings.Contains(screen(m), "› ") {
				t.Fatalf("%dx%d: selection scrolled off:\n%s", w, h, screen(m))
			}
		}
		key(m, "?")
		check("help")
		key(m, "x")
		m.notice = strings.Repeat("long notice ", 30)
		check("notice")
	}
}

func TestDetailOfAvailableEntry(t *testing.T) {
	sandbox(t)
	m := newModel(t)
	deliver(m, m.Init())
	key(m, "G")
	s := screen(m)
	for _, want := range []string{"needs osheet on PATH", "not installed · i to install", "command"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
}

func TestRefreshOfflineKeepsCachedList(t *testing.T) {
	sandbox(t)
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "offline", http.StatusBadGateway)
			return
		}
		w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCK_REGISTRY", srv.URL)
	m := newModel(t)
	deliver(m, m.Init())
	down.Store(true)
	deliver(m, key(m, "r"))
	s := screen(m)
	if !strings.Contains(s, "(cached, 0s)  refresh failed") || !strings.Contains(s, "lazy1on1") {
		t.Fatalf("screen:\n%s", s)
	}
}
