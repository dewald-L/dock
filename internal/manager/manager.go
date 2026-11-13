// Package manager is the built-in "plugins" system plugin: list, open,
// enable, disable, inspect and remove plugins, and install plugins from the
// plugin index.
package manager

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/execplugin"
	"github.com/dewald-L/dock/internal/index"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

// ID of the manager plugin.
const ID = "plugins"

// ReloadMsg asks the host to re-read the config. Note is shown on success.
type ReloadMsg struct{ Note string }

func reload(note string) tea.Cmd { return func() tea.Msg { return ReloadMsg{note} } }

// Plugin is the manager. Registry returns the host's live registry.
type Plugin struct {
	Registry func() *registry.Registry
}

func (Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: ID, Name: "Plugins", Version: "0.2.0",
		Description: "Open, enable, disable, inspect and install plugins",
		Display:     plugin.DisplayFull, Lifecycle: plugin.Persistent,
	}
}

func (p Plugin) New(ctx plugin.Context) (tea.Model, error) {
	t := ctx.Theme
	return &model{
		reg:       p.Registry,
		installed: map[string]bool{},
		accent:    lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		muted:     lipgloss.NewStyle().Foreground(t.Muted),
		ok:        lipgloss.NewStyle().Foreground(t.Ok),
		warn:      lipgloss.NewStyle().Foreground(t.Warn).Bold(true),
		errSt:     lipgloss.NewStyle().Foreground(t.Err),
	}, nil
}

// ---- messages ----

// availMsg carries a loaded plugin index (or the error) for request gen.
type availMsg struct {
	gen int
	res *index.Result
	cfg *config.Config
	err error
}

// installedMsg reports the end of `dock plugin install <id>`.
type installedMsg struct {
	id   string
	kind string
	err  error
}

// fetchIndex loads the config and the plugin index off the UI goroutine.
func fetchIndex(gen int, refresh bool) tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return availMsg{gen: gen, err: err}
		}
		r, err := index.Load(index.Source(cfg), refresh)
		return availMsg{gen: gen, res: r, cfg: cfg, err: err}
	}
}

// installCmd runs `dock plugin install <id>` with the terminal handed over,
// so the build output is visible, and waits for Enter before returning.
func installCmd(e index.Entry) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return plugin.Error(err)
	}
	argv := execplugin.Paused([]string{exe, "plugin", "install", e.ID})
	c := exec.Command(argv[0], argv[1:]...)
	id, kind := e.ID, e.Kind
	return plugin.Exec(c, func(err error) tea.Msg { return installedMsg{id: id, kind: kind, err: err} })
}

// ---- model ----

type model struct {
	reg     func() *registry.Registry
	sel     int
	top     int // first visible line of the list
	w, h    int
	confirm string // exec plugin id awaiting removal confirmation
	help    bool

	// the "Available" section
	gen       int // current index request
	loading   bool
	res       *index.Result
	cfg       *config.Config // config as of the last index load
	loadErr   error
	installed map[string]bool // installed from this screen since it opened
	notice    string

	accent, muted, ok, warn, errSt lipgloss.Style
}

func (m *model) Init() tea.Cmd { return m.load(false) }

func (m *model) load(refresh bool) tea.Cmd {
	m.gen++
	m.loading = true
	return fetchIndex(m.gen, refresh)
}

func (m *model) entries() []*registry.Entry { return m.reg().Entries }

// avail is one entry of the Available section.
type avail struct {
	index.Entry
	pending bool // installed, but this dock binary doesn't have it yet
}

// available returns the index entries that the running dock doesn't have.
func (m *model) available() []avail {
	if m.res == nil {
		return nil
	}
	ids := map[string]bool{}
	pkgs := map[string]bool{}
	for _, e := range m.entries() {
		ids[e.Manifest.ID] = true
		if e.Package != "" {
			pkgs[e.Package] = true
		}
	}
	cfg := m.cfg
	if cfg == nil {
		cfg = &config.Config{}
	}
	var out []avail
	for _, e := range m.res.Plugins {
		if ids[e.ID] || (e.Kind == index.KindNative && pkgs[e.Package]) {
			continue
		}
		out = append(out, avail{Entry: e, pending: m.installed[e.ID] || e.Installed(cfg, nil)})
	}
	return out
}

// selection returns the selected installed entry or available entry
// (exactly one is non-nil when there is anything to select).
func (m *model) selection() (*registry.Entry, *avail) {
	es, av := m.entries(), m.available()
	m.sel = min(max(m.sel, 0), max(len(es)+len(av)-1, 0))
	switch {
	case m.sel < len(es):
		return es[m.sel], nil
	case m.sel-len(es) < len(av):
		return nil, &av[m.sel-len(es)]
	}
	return nil, nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case availMsg:
		if msg.gen != m.gen {
			return m, nil // superseded by a newer request
		}
		m.loading = false
		m.loadErr = msg.err
		if msg.err == nil {
			m.res, m.cfg = msg.res, msg.cfg
		}
	case installedMsg:
		if msg.err != nil {
			m.notice = "install of " + msg.id + " failed (" + msg.err.Error() + ")"
			return m, plugin.Error(fmt.Errorf("installing %s failed", msg.id))
		}
		m.installed[msg.id] = true
		if msg.kind == index.KindExec {
			m.notice = "added " + msg.id
		} else {
			m.notice = "installed " + msg.id + ": restart dock to load it"
		}
		return m, tea.Batch(reload(m.notice), m.load(false))
	case tea.KeyMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) tea.Cmd {
	if m.help {
		m.help = false
		return nil
	}
	if m.confirm != "" {
		target := m.confirm
		m.confirm = ""
		if k != "y" {
			return plugin.Notify("kept " + target)
		}
		return edit(func(c *config.Config) error {
			if !c.RemoveExec(target) {
				return fmt.Errorf("%s is not an exec plugin", target)
			}
			return nil
		}, "removed "+target)
	}

	e, a := m.selection()
	switch k {
	case "up", "k":
		m.sel--
	case "down", "j":
		m.sel++
	case "g", "home":
		m.sel = 0
	case "G", "end":
		m.sel = len(m.entries()) + len(m.available()) - 1
	case "?":
		m.help = true
	case "r":
		return tea.Batch(reload("config reloaded"), m.load(true))
	case "q", "esc":
		return plugin.Close()
	case "i":
		switch {
		case a != nil && a.pending:
			return plugin.Notify(a.ID + " is installed: restart dock to load it")
		case a != nil:
			m.notice = ""
			return installCmd(a.Entry)
		case e != nil:
			return plugin.Notify(e.Manifest.Name + " is already installed")
		}
	case "enter", "o", "s", " ", "e", "d", "delete":
		if a != nil {
			if a.pending {
				return plugin.Notify(a.ID + " is installed: restart dock to load it")
			}
			return plugin.Notify(a.ID + " is not installed: press i to install it")
		}
		if e != nil {
			return m.installedKey(k, e)
		}
	}
	m.selection() // clamp
	return nil
}

func (m *model) installedKey(k string, e *registry.Entry) tea.Cmd {
	id := e.Manifest.ID
	switch k {
	case "enter", "o":
		if id == ID {
			return nil
		}
		return plugin.Open(id, false)
	case "s":
		return plugin.Open(id, true)
	case " ", "e":
		if e.Kind == registry.System {
			return plugin.Notify(e.Manifest.Name + " is built in and always enabled")
		}
		on := !e.Enabled
		verb := "disabled"
		if on {
			verb = "enabled"
		}
		return edit(func(c *config.Config) error { c.SetEnabled(id, on); return nil }, e.Manifest.Name+" "+verb)
	case "d", "delete":
		if e.Kind != registry.Exec {
			return plugin.Notify("native plugins are removed from the shell (it rebuilds dock): dock plugin remove " + id)
		}
		m.confirm = id
	}
	return nil
}

// edit loads the config, applies f, saves and asks the host to reload.
func edit(f func(*config.Config) error, note string) tea.Cmd {
	cfg, err := config.Load()
	if err == nil {
		err = f(cfg)
	}
	if err == nil {
		err = cfg.Save()
	}
	if err != nil {
		return plugin.Error(err)
	}
	return reload(note)
}

func (m *model) Help() []plugin.KeyHelp {
	if _, a := m.selection(); a != nil {
		return []plugin.KeyHelp{
			{Key: "i", Desc: "install"}, {Key: "r", Desc: "refresh"}, {Key: "?", Desc: "help"}, {Key: "q", Desc: "close"},
		}
	}
	return []plugin.KeyHelp{
		{Key: "enter", Desc: "open"}, {Key: "space", Desc: "enable/disable"}, {Key: "d", Desc: "remove exec"},
		{Key: "r", Desc: "refresh"}, {Key: "?", Desc: "help"}, {Key: "q", Desc: "close"},
	}
}

// ---- view ----

// line is one rendered line of the list; row is the selectable index it
// shows, or -1.
type line struct {
	s   string
	row int
}

func (m *model) View() string {
	w, h := m.w, m.h
	if w <= 0 || h <= 0 {
		return ""
	}
	if m.help {
		return fitBlock(m.helpLines(), w, h)
	}
	es := m.entries()
	av := m.available()
	m.selection() // clamp

	head := m.accent.Render("Plugins") + m.muted.Render(fmt.Sprintf("  %d installed · config %s", len(es), config.Path()))
	list := m.listLines(es, av)
	detail := m.detailLines(es, av)
	foot := m.footLines(h)

	// layout: head, blank, list, blank, detail, padding, footer
	rem := max(h-2-len(foot), 0)
	listH := min(len(list), rem)
	if len(detail) > 0 && listH+1+len(detail) > rem {
		// the list keeps at least half the room (or all it needs)
		listH = min(len(list), max(rem-1-len(detail), rem/2))
	}
	detailH := 0
	if rem-listH-1 > 0 {
		detailH = min(len(detail), rem-listH-1)
	}

	// scroll the list so the selected row is visible
	selLine := 0
	for i, l := range list {
		if l.row == m.sel {
			selLine = i
		}
	}
	m.top = min(m.top, selLine)
	if selLine >= m.top+listH {
		m.top = selLine - listH + 1
	}
	if m.sel == len(es) && m.top == selLine && selLine > 0 && listH >= 2 {
		m.top-- // the first available row: show the section heading too
	}
	m.top = max(min(m.top, len(list)-listH), 0)

	out := []string{head, ""}
	for _, l := range list[m.top : m.top+listH] {
		out = append(out, l.s)
	}
	if detailH > 0 {
		out = append(out, "")
		out = append(out, detail[:detailH]...)
	}
	for len(out) < h-len(foot) {
		out = append(out, "")
	}
	out = append(out, foot...)
	return fitBlock(out, w, h)
}

func (m *model) listLines(es []*registry.Entry, av []avail) []line {
	nameW := 4
	for _, e := range es {
		nameW = max(nameW, ansi.StringWidth(e.Manifest.Name))
	}
	for _, a := range av {
		nameW = max(nameW, ansi.StringWidth(a.DisplayName()))
	}
	nameW = min(nameW, 24)
	pad := func(s string, n int) string {
		return ansi.Truncate(s, n, "…") + strings.Repeat(" ", max(n-ansi.StringWidth(s), 0))
	}

	var ls []line
	ls = append(ls, line{m.muted.Render("    " + pad("NAME", nameW) + "  " + pad("KIND", 6) + "  " + pad("DISPLAY", 8) + "  LIFECYCLE"), -1})
	for i, e := range es {
		dot := m.ok.Render("●")
		if !e.Enabled {
			dot = m.muted.Render("○")
		}
		row := pad(e.Manifest.Name, nameW) + "  " + pad(e.Kind.String(), 6) + "  " + pad(e.Manifest.Display.String(), 8) + "  " + e.Manifest.Lifecycle.String()
		ls = append(ls, line{m.mark(i, dot, row), i})
	}

	ls = append(ls, line{"", -1})
	title := m.accent.Render("Available")
	note := ""
	switch {
	case m.loading:
		note = "loading…"
	case m.res != nil && m.res.Cached:
		note = m.res.Note()
	}
	if note != "" {
		title += m.muted.Render("  " + note)
	}
	if m.res != nil && (m.loadErr != nil || m.res.Stale) {
		title += m.muted.Render("  refresh failed")
	}
	ls = append(ls, line{title, -1})
	switch {
	case m.res == nil && m.loadErr != nil:
		ls = append(ls, line{m.muted.Render("  could not load the plugin index: " + strings.TrimPrefix(oneLine(m.loadErr.Error()), "fetching the plugin index: ")), -1})
	case m.res == nil:
		ls = append(ls, line{m.muted.Render("  loading…"), -1})
	case len(av) == 0:
		ls = append(ls, line{m.muted.Render("  nothing else in the index"), -1})
	}
	for j, a := range av {
		i := len(es) + j
		dot := m.muted.Render("+")
		desc := a.Description
		if a.pending {
			dot = m.ok.Render("✓")
			desc = "installed · restart dock to load it"
		}
		row := pad(a.DisplayName(), nameW) + "  " + pad(a.Kind, 6) + "  " + desc
		ls = append(ls, line{m.mark(i, dot, row), i})
	}
	return ls
}

func (m *model) mark(i int, dot, row string) string {
	if i == m.sel {
		return m.accent.Render("› ") + dot + " " + m.accent.Render(row)
	}
	return "  " + dot + " " + row
}

func (m *model) field(b *[]string, k, v string) {
	if v != "" {
		*b = append(*b, m.muted.Render(fmt.Sprintf("  %-10s ", k))+v)
	}
}

func (m *model) detailLines(es []*registry.Entry, av []avail) []string {
	var b []string
	e, a := m.selection()
	switch {
	case e != nil:
		mf := e.Manifest
		b = append(b, m.accent.Render(mf.Name)+"  "+m.muted.Render(mf.ID))
		m.field(&b, "about", mf.Description)
		m.field(&b, "version", mf.Version)
		status := "enabled"
		if !e.Enabled {
			status = "disabled"
		}
		m.field(&b, "status", status)
		multi := "single instance"
		if mf.Multi {
			multi = "multiple instances"
		}
		m.field(&b, "runs as", fmt.Sprintf("%s · %s · %s", mf.Display, mf.Lifecycle, multi))
		m.field(&b, "package", e.Package)
		if mf.MinWidth > 0 || mf.MinHeight > 0 {
			m.field(&b, "min size", fmt.Sprintf("%dx%d", mf.MinWidth, mf.MinHeight))
		}
		if e.Exec != nil {
			m.field(&b, "command", strings.Join(e.Exec.Command, " "))
			m.field(&b, "dir", e.Exec.Dir)
			if e.Exec.Pause {
				m.field(&b, "pause", "waits for Enter after exit")
			}
		}
		m.field(&b, "data", config.PluginDataDir(mf.ID))
		m.field(&b, "settings", config.PluginConfigDir(mf.ID))
	case a != nil:
		b = append(b, m.accent.Render(a.DisplayName())+"  "+m.muted.Render(a.ID))
		m.field(&b, "about", a.Description)
		if a.pending {
			m.field(&b, "status", "installed · restart dock to load it")
		} else {
			m.field(&b, "status", "not installed · i to install")
		}
		m.field(&b, "kind", a.Kind)
		if a.Kind == index.KindNative {
			v := a.Version
			if v == "" {
				v = "latest"
			}
			m.field(&b, "package", a.Package+" ("+v+")")
		} else {
			m.field(&b, "command", strings.Join(a.Command, " "))
		}
		m.field(&b, "requires", a.Requires)
		m.field(&b, "repo", a.Repo)
		m.field(&b, "tags", strings.Join(a.Tags, ", "))
	}
	return b
}

func (m *model) footLines(h int) []string {
	if m.confirm != "" {
		return []string{m.warn.Render(fmt.Sprintf("Remove exec plugin %s from the config? y/N", m.confirm))}
	}
	var f []string
	if m.notice != "" {
		st := m.ok
		if strings.Contains(m.notice, "failed") {
			st = m.errSt
		}
		f = append(f, st.Render(m.notice))
	}
	if h >= 24 {
		f = append(f,
			m.muted.Render("Add an external program:  dock plugin add <id> -- <command> [args]"),
			m.muted.Render("Install by id or package: dock plugin install <id>|<go-package>"))
	}
	return f
}

func (m *model) helpLines() []string {
	rows := [][2]string{
		{"j k  ↑ ↓  g G", "move"},
		{"enter  o", "open the plugin"},
		{"s", "open it in a split"},
		{"space  e", "enable / disable"},
		{"d", "remove an exec plugin"},
		{"i", "install the selected Available plugin (rebuilds dock)"},
		{"r", "reload the config and refresh the plugin index"},
		{"?", "this help"},
		{"q  esc", "close"},
	}
	out := []string{m.accent.Render("Plugins: keys"), ""}
	for _, r := range rows {
		out = append(out, "  "+m.accent.Render(fmt.Sprintf("%-14s", r[0]))+"  "+r[1])
	}
	src := "the index"
	if m.res != nil {
		src = m.res.Source
	}
	out = append(out, "",
		m.muted.Render("Available lists the plugins in "+src+" that aren't installed."),
		m.muted.Render("Native plugins load after a restart: dock can't add code to itself while running."),
		"", m.muted.Render("Press any key to go back."))
	return out
}

// fitBlock returns exactly h lines, each exactly w cells wide.
func fitBlock(lines []string, w, h int) string {
	lines = slices.Clone(lines)
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		l = ansi.Truncate(l, w, "…")
		lines[i] = l + strings.Repeat(" ", max(w-ansi.StringWidth(l), 0))
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
