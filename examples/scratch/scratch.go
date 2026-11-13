// Package scratch is an example plugin, not built into dock by default:
// install it with `dock plugin install github.com/dewald-L/dock/examples/scratch`.
// It is a minimal reference plugin: a notepad that saves to its data
// directory. It shows every part of the plugin contract in a
// small space: sizing, focus, saving on blur/close, status bar hints,
// palette commands and notifications.
package scratch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dewald-L/dock/plugin"
)

type Plugin struct{}

// Importing the package compiles the plugin into dock.
func init() { plugin.Register(Plugin{}) }

func (Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "scratch", Name: "Scratch", Version: "0.1.0",
		Description: "A notepad that saves itself",
		Display:     plugin.DisplayTile, Lifecycle: plugin.Persistent,
		MinWidth: 20, MinHeight: 4,
	}
}

// Summary implements plugin.Summarizer: the home screen shows the pad's
// first line and its size. It only reads the saved file, so it is cheap,
// safe to call concurrently and works whether or not the pad is open.
func (Plugin) Summary(ctx plugin.Context) (plugin.Summary, error) {
	b, err := os.ReadFile(filepath.Join(ctx.DataDir, "scratch.md"))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && strings.TrimSpace(string(b)) == "") {
		return plugin.Summary{Lines: []string{"empty"}}, nil
	}
	if err != nil {
		return plugin.Summary{}, err
	}
	text := string(b)
	first := ""
	for _, l := range strings.Split(text, "\n") {
		if first = strings.TrimSpace(l); first != "" {
			break
		}
	}
	lines := strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
	return plugin.Summary{Lines: []string{
		first,
		fmt.Sprintf("%d lines · %d words", lines, len(strings.Fields(text))),
	}}, nil
}

func (Plugin) New(ctx plugin.Context) (tea.Model, error) {
	path := filepath.Join(ctx.DataDir, "scratch.md")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Placeholder = "Type anything. Saved when you leave the pane or press ctrl+s."
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.Prompt = ""
	ta.SetValue(string(b))
	return &model{ta: ta, path: path, saved: string(b),
		muted: lipgloss.NewStyle().Foreground(ctx.Theme.Muted)}, nil
}

type (
	clearMsg struct{}
	saveMsg  struct{}
)

type model struct {
	ta    textarea.Model
	path  string
	saved string
	muted lipgloss.Style
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) save() error {
	v := m.ta.Value()
	if v == m.saved {
		return nil
	}
	if err := os.WriteFile(m.path, []byte(v), 0o644); err != nil {
		return err
	}
	m.saved = v
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ta.SetWidth(msg.Width)
		m.ta.SetHeight(max(msg.Height-1, 1)) // one line for the footer
		return m, nil
	case plugin.FocusMsg:
		return m, m.ta.Focus()
	case plugin.BlurMsg:
		m.ta.Blur()
		return m, plugin.Error(m.save())
	case saveMsg:
		if err := m.save(); err != nil {
			return m, plugin.Error(err)
		}
		return m, plugin.Notify("scratch saved")
	case clearMsg:
		m.ta.Reset()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+s" {
			return m.Update(saveMsg{})
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *model) View() string {
	state := "saved"
	if m.ta.Value() != m.saved {
		state = "modified"
	}
	return m.ta.View() + "\n" + m.muted.Render(state+" · "+m.path)
}

// Close implements plugin.Closer.
func (m *model) Close() error { return m.save() }

// Help implements plugin.Helper.
func (m *model) Help() []plugin.KeyHelp {
	return []plugin.KeyHelp{{Key: "ctrl+s", Desc: "save"}}
}

// Commands implements plugin.Commander.
func (m *model) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "save", Desc: "write to disk now", Msg: saveMsg{}},
		{Name: "clear", Desc: "empty the pad", Msg: clearMsg{}},
	}
}
