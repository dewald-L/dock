// Package host is dock's Bubble Tea root model: tabs, tiled panes, the
// command palette and the plumbing that routes messages to plugins.
package host

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/execplugin"
	"github.com/dewald-L/dock/internal/manager"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

const maxPanes = 4

// Options configure the host.
type Options struct {
	Config  *config.Config
	Native  []plugin.Plugin
	Open    []string // plugin IDs to open at start; default Config.Startup
	Version string
}

type instance struct {
	id    int
	entry *registry.Entry
	model tea.Model
	title string
	w, h  int
	dead  bool
	crash any
	shown bool // has been in the active tab; transients close only after this
}

func (in *instance) name() string {
	if in.title != "" {
		return in.title
	}
	return in.entry.Manifest.Name
}

type tab struct {
	panes []int // instance IDs
	focus int   // index into panes
	rows  bool  // stack panes top-to-bottom instead of side by side
	zoom  bool  // show only the focused pane
}

type overlayKind int

const (
	ovNone overlayKind = iota
	ovPalette
	ovHelp
)

// App is the root model. It is used through a pointer.
type App struct {
	opts  Options
	cfg   *config.Config
	reg   *registry.Registry
	theme plugin.Theme
	st    styles

	inst      map[int]*instance
	nextID    int
	tabs      []*tab
	active    int
	lastFocus int

	w, h    int
	leader  bool
	overlay overlayKind
	pal     palette
	homeSel int
	homeTop int // first grid row shown on the home screen

	// home screen summaries (summary.go)
	homeOn     bool     // the home screen is showing
	homeGen    int      // bumped whenever homeOn changes; retires old ticks
	homeIDs    []string // plugin order (and so numbers) while it shows
	sums       map[string]*sumState
	sumEvery   time.Duration
	sumTimeout time.Duration

	status    string
	statusErr bool
	statusSeq int
	loadErrs  []error
}

type clearStatusMsg struct{ seq int }

type execDoneMsg struct {
	name string
	run  *execplugin.Run
	err  error
}

// New builds the host.
func New(opts Options) *App {
	a := &App{
		opts:  opts,
		cfg:   opts.Config,
		theme: plugin.DefaultTheme(),
		inst:  map[int]*instance{},
		sums:  map[string]*sumState{},

		sumEvery:   summaryEvery,
		sumTimeout: summaryTimeout,
	}
	a.st = newStyles(a.theme)
	a.buildRegistry()
	return a
}

// SystemPlugins are built into the host and cannot be disabled. reg may be
// nil when only manifests are needed (the CLI).
func SystemPlugins(reg func() *registry.Registry) []plugin.Plugin {
	return []plugin.Plugin{manager.Plugin{Registry: reg}}
}

func (a *App) buildRegistry() {
	a.reg, a.loadErrs = registry.Build(a.cfg, SystemPlugins(func() *registry.Registry { return a.reg }), a.opts.Native)
}

// Init opens the startup plugins.
func (a *App) Init() tea.Cmd {
	ids := a.opts.Open
	if len(ids) == 0 {
		ids = a.cfg.Startup
	}
	var cmds []tea.Cmd
	for _, id := range ids {
		cmds = append(cmds, a.open(id, false))
	}
	if len(a.tabs) > 0 {
		a.active = 0
	}
	if len(a.loadErrs) > 0 {
		cmds = append(cmds, a.notify(a.loadErrs[0].Error(), true))
	}
	return tea.Batch(append(cmds, a.sync())...)
}

// ---- status bar ----

func (a *App) notify(text string, isErr bool) tea.Cmd {
	a.status, a.statusErr = text, isErr
	a.statusSeq++
	seq := a.statusSeq
	d := 4 * time.Second
	if isErr {
		d = 8 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{seq} })
}

// ---- lookup ----

func (a *App) curTab() *tab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *App) focusedID() int {
	t := a.curTab()
	if t == nil || len(t.panes) == 0 {
		return 0
	}
	return t.panes[t.focus]
}

func (a *App) focused() *instance { return a.inst[a.focusedID()] }

func (a *App) findByPlugin(id string) *instance {
	for _, t := range a.tabs {
		for _, iid := range t.panes {
			if in := a.inst[iid]; in != nil && in.entry.Manifest.ID == id {
				return in
			}
		}
	}
	return nil
}

func (a *App) focusInstance(id int) {
	for ti, t := range a.tabs {
		if i := slices.Index(t.panes, id); i >= 0 {
			a.active, t.focus = ti, i
			return
		}
	}
}

// ---- opening and closing ----

// canTile reports whether a plugin with manifest m may join tab t.
func (a *App) canTile(t *tab, m plugin.Manifest) bool {
	if t == nil || m.Display != plugin.DisplayTile || len(t.panes) >= maxPanes {
		return false
	}
	for _, id := range t.panes {
		if in := a.inst[id]; in == nil || in.entry.Manifest.Display != plugin.DisplayTile {
			return false
		}
	}
	return true
}

func (a *App) open(id string, split bool) tea.Cmd {
	e := a.reg.Get(id)
	switch {
	case e == nil:
		return a.notify("unknown plugin: "+id, true)
	case !e.Enabled:
		return a.notify(e.Manifest.Name+" is disabled (ctrl+g m to manage plugins)", true)
	case e.Kind == registry.Exec:
		return a.runExec(e)
	}
	m := e.Manifest
	if !m.Multi {
		if in := a.findByPlugin(id); in != nil {
			a.focusInstance(in.id)
			return nil
		}
	}

	data, conf, err := config.EnsurePluginDirs(id)
	if err != nil {
		return a.notify(fmt.Sprintf("%s: %v", m.Name, err), true)
	}
	ctx := plugin.Context{DataDir: data, ConfigDir: conf, Theme: a.theme}
	model, err := safeNew(e.Plugin, ctx)
	if err != nil {
		return a.notify(fmt.Sprintf("%s: %v", m.Name, err), true)
	}
	a.nextID++
	in := &instance{id: a.nextID, entry: e, model: model}
	a.inst[in.id] = in

	var note tea.Cmd
	t := a.curTab()
	if split && a.canTile(t, m) {
		t.panes = append(t.panes, in.id)
		t.focus, t.zoom = len(t.panes)-1, false
	} else {
		if split && t != nil {
			note = a.notify(m.Name+" can't share this tab; opened in a new one", false)
		}
		a.tabs = append(a.tabs, &tab{panes: []int{in.id}})
		a.active = len(a.tabs) - 1
	}
	return tea.Batch(a.initInstance(in), note)
}

func (a *App) runExec(e *registry.Entry) tea.Cmd {
	run, err := execplugin.Prepare(e.Exec)
	if err != nil {
		return a.notify(fmt.Sprintf("%s: %v", e.Manifest.Name, err), true)
	}
	name := e.Manifest.Name
	return tea.ExecProcess(run.Cmd, func(err error) tea.Msg {
		return execDoneMsg{name: name, run: run, err: err}
	})
}

func (a *App) execDone(m execDoneMsg) tea.Cmd {
	res := m.run.Result()
	switch {
	case m.err != nil && res != "":
		return a.notify(fmt.Sprintf("%s: %s (%v)", m.name, res, m.err), true)
	case m.err != nil:
		return a.notify(fmt.Sprintf("%s: %v", m.name, m.err), true)
	case res != "":
		return a.notify(m.name+": "+res, false)
	}
	return a.notify(m.name+" finished", false)
}

// closeInstance calls the plugin's Close hook and removes it.
func (a *App) closeInstance(id int) tea.Cmd {
	in := a.inst[id]
	if in == nil {
		return nil
	}
	var cmd tea.Cmd
	if !in.dead {
		if err := safeClose(in); err != nil {
			cmd = a.notify(fmt.Sprintf("%s: close: %v", in.entry.Manifest.Name, err), true)
		}
	}
	a.remove(id)
	return cmd
}

func (a *App) remove(id int) {
	delete(a.inst, id)
	if a.lastFocus == id {
		a.lastFocus = 0
	}
	for ti := 0; ti < len(a.tabs); ti++ {
		t := a.tabs[ti]
		i := slices.Index(t.panes, id)
		if i < 0 {
			continue
		}
		t.panes = slices.Delete(t.panes, i, i+1)
		if t.focus >= i && t.focus > 0 {
			t.focus--
		}
		if len(t.panes) < 2 {
			t.zoom = false
		}
		if len(t.panes) == 0 {
			a.tabs = slices.Delete(a.tabs, ti, ti+1)
			if ti < a.active || a.active >= len(a.tabs) {
				a.active--
			}
			a.active = max(a.active, 0)
		}
		return
	}
}

// reload re-reads the config file and rebuilds the registry. note is shown
// in the status bar when the reload succeeded.
func (a *App) reload(note string) tea.Cmd {
	cfg, err := config.Load()
	if err != nil {
		return a.notify(err.Error(), true)
	}
	a.cfg = cfg
	a.buildRegistry()
	cmds := []tea.Cmd{}
	for id, in := range a.inst {
		e := a.reg.Get(in.entry.Manifest.ID)
		if e == nil || !e.Enabled {
			cmds = append(cmds, a.closeInstance(id))
			continue
		}
		in.entry = e
	}
	if a.homeOn {
		a.refreshHomeIDs()
		cmds = append(cmds, a.fetchSummaries())
	}
	a.homeSel = min(a.homeSel, max(len(a.homeEntries())-1, 0))
	if len(a.loadErrs) > 0 {
		return tea.Batch(append(cmds, a.notify(a.loadErrs[0].Error(), true))...)
	}
	if note != "" {
		cmds = append(cmds, a.notify(note, false))
	}
	return tea.Batch(cmds...)
}

func (a *App) quit() tea.Cmd {
	for id := range a.inst {
		a.closeInstance(id)
	}
	return tea.Quit
}

// sync brings every instance in line with the current layout: it removes
// crashed and hidden transient instances, sends size changes, and sends
// Focus/Blur when the focused pane changed. Update calls it every time.
func (a *App) sync() tea.Cmd {
	cmds := []tea.Cmd{a.reap()}
	if a.w > 0 && a.h > 0 {
		for _, t := range a.tabs {
			for id, r := range a.layout(t) {
				in := a.inst[id]
				if in == nil || in.dead || (in.w == r.iw && in.h == r.ih) {
					continue
				}
				in.w, in.h = r.iw, r.ih
				cmds = append(cmds, a.deliver(in, tea.WindowSizeMsg{Width: r.iw, Height: r.ih}))
			}
		}
	}
	if cur := a.focusedID(); cur != a.lastFocus {
		if old := a.inst[a.lastFocus]; old != nil {
			cmds = append(cmds, a.deliver(old, plugin.BlurMsg{}))
		}
		if in := a.inst[cur]; in != nil {
			cmds = append(cmds, a.deliver(in, plugin.FocusMsg{}))
		}
		a.lastFocus = cur
	}
	cmds = append(cmds, a.reap(), a.syncHome())
	return tea.Batch(cmds...)
}

func (a *App) reap() tea.Cmd {
	var cmds []tea.Cmd
	cur := a.curTab()
	if cur != nil {
		for _, id := range cur.panes {
			if in := a.inst[id]; in != nil {
				in.shown = true
			}
		}
	}
	for id, in := range a.inst {
		switch {
		case in.dead:
			a.remove(id)
			msg := strings.SplitN(fmt.Sprint(in.crash), "\n", 2)[0]
			cmds = append(cmds, a.notify(fmt.Sprintf("%s crashed and was closed: %s", in.entry.Manifest.Name, msg), true))
		case in.shown && in.entry.Manifest.Lifecycle == plugin.Transient && (cur == nil || !slices.Contains(cur.panes, id)):
			cmds = append(cmds, a.closeInstance(id))
		}
	}
	return tea.Batch(cmds...)
}
