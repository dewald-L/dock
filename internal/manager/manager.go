// Package manager is the built-in "plugins" system plugin: list, open,
// enable, disable, inspect and remove plugins.
package manager

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dewald-L/dock/internal/config"
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
		ID: ID, Name: "Plugins", Version: "0.1.0",
		Description: "Open, enable, disable and inspect plugins",
		Display:     plugin.DisplayFull, Lifecycle: plugin.Persistent,
	}
}

func (p Plugin) New(ctx plugin.Context) (tea.Model, error) {
	t := ctx.Theme
	return &model{
		reg:    p.Registry,
		accent: lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		muted:  lipgloss.NewStyle().Foreground(t.Muted),
		ok:     lipgloss.NewStyle().Foreground(t.Ok),
		warn:   lipgloss.NewStyle().Foreground(t.Warn).Bold(true),
	}, nil
}

type model struct {
	reg     func() *registry.Registry
	sel     int
	w, h    int
	confirm string // exec plugin id awaiting removal confirmation

	accent, muted, ok, warn lipgloss.Style
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) entries() []*registry.Entry {
	es := m.reg().Entries
	m.sel = min(max(m.sel, 0), max(len(es)-1, 0))
	return es
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) tea.Cmd {
	es := m.entries()
	if len(es) == 0 {
		return nil
	}
	e := es[m.sel]
	id := e.Manifest.ID

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

	switch k {
	case "up", "k":
		m.sel--
	case "down", "j":
		m.sel++
	case "g", "home":
		m.sel = 0
	case "G", "end":
		m.sel = len(es) - 1
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
			return plugin.Notify("only exec plugins can be removed; disable native plugins instead")
		}
		m.confirm = id
	case "r":
		return reload("config reloaded")
	case "q", "esc":
		return plugin.Close()
	}
	m.entries() // clamp
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
	return []plugin.KeyHelp{
		{Key: "enter", Desc: "open"}, {Key: "s", Desc: "split"}, {Key: "space", Desc: "enable/disable"},
		{Key: "d", Desc: "remove exec"}, {Key: "r", Desc: "reload"}, {Key: "q", Desc: "close"},
	}
}

func (m *model) View() string {
	es := m.entries()
	var b strings.Builder
	b.WriteString(m.accent.Render("Plugins") + m.muted.Render(fmt.Sprintf("  %d installed · config %s", len(es), config.Path())) + "\n\n")

	nameW := 4
	for _, e := range es {
		nameW = max(nameW, lipgloss.Width(e.Manifest.Name))
	}
	b.WriteString(m.muted.Render(fmt.Sprintf("    %-*s  %-6s  %-8s  %-10s", nameW, "NAME", "KIND", "DISPLAY", "LIFECYCLE")) + "\n")
	for i, e := range es {
		dot := m.ok.Render("●")
		if !e.Enabled {
			dot = m.muted.Render("○")
		}
		row := fmt.Sprintf("%-*s  %-6s  %-8s  %-10s", nameW, e.Manifest.Name, e.Kind, e.Manifest.Display, e.Manifest.Lifecycle)
		if i == m.sel {
			row = m.accent.Render("› ") + dot + " " + m.accent.Render(row)
		} else {
			row = "  " + dot + " " + row
		}
		b.WriteString(row + "\n")
	}

	if len(es) > 0 {
		e := es[m.sel]
		mf := e.Manifest
		b.WriteString("\n" + m.accent.Render(mf.Name) + "  " + m.muted.Render(mf.ID) + "\n")
		field := func(k, v string) {
			if v != "" {
				b.WriteString(m.muted.Render(fmt.Sprintf("  %-10s ", k)) + v + "\n")
			}
		}
		field("about", mf.Description)
		field("version", mf.Version)
		status := "enabled"
		if !e.Enabled {
			status = "disabled"
		}
		field("status", status)
		multi := "single instance"
		if mf.Multi {
			multi = "multiple instances"
		}
		field("runs as", fmt.Sprintf("%s · %s · %s", mf.Display, mf.Lifecycle, multi))
		if mf.MinWidth > 0 || mf.MinHeight > 0 {
			field("min size", fmt.Sprintf("%dx%d", mf.MinWidth, mf.MinHeight))
		}
		if e.Exec != nil {
			field("command", strings.Join(e.Exec.Command, " "))
			field("dir", e.Exec.Dir)
			if e.Exec.Pause {
				field("pause", "waits for Enter after exit")
			}
		}
		field("data", config.PluginDataDir(mf.ID))
		field("settings", config.PluginConfigDir(mf.ID))
	}

	if m.confirm != "" {
		b.WriteString("\n" + m.warn.Render(fmt.Sprintf("Remove exec plugin %s from the config? y/N", m.confirm)) + "\n")
	} else {
		b.WriteString("\n" + m.muted.Render("Add an external program:  dock plugin add <id> -- <command> [args]") + "\n")
	}
	return b.String()
}
