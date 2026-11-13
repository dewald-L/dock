package plugin

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// RunStandalone runs a native plugin as its own full-screen program,
// outside dock. It translates the host commands so the same model works in
// both places: Close and tea.Quit exit, Exec runs the program, SetTitle sets
// the terminal title, Notify is printed after exit (the last one only), and
// Open is ignored. The model gets FocusMsg at start.
func RunStandalone(p Plugin, ctx Context) error {
	if ctx.Theme == (Theme{}) {
		ctx.Theme = DefaultTheme()
	}
	for _, d := range []string{ctx.DataDir, ctx.ConfigDir} {
		if d != "" {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
	}
	m, err := p.New(ctx)
	if err != nil {
		return err
	}
	s := &standalone{inner: m}
	_, err = tea.NewProgram(s, tea.WithAltScreen()).Run()
	if c, ok := s.inner.(Closer); ok {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	if s.last != "" {
		fmt.Fprintln(os.Stderr, s.last)
	}
	return err
}

type standalone struct {
	inner tea.Model
	last  string
}

func (s *standalone) Init() tea.Cmd {
	return tea.Batch(s.inner.Init(), func() tea.Msg { return FocusMsg{} })
}

func (s *standalone) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case CloseMsg:
		return s, tea.Quit
	case NotifyMsg:
		s.last = m.Text
		return s, nil
	case TitleMsg:
		return s, tea.SetWindowTitle(m.Title)
	case OpenMsg:
		return s, nil
	case ExecMsg:
		fn := m.Fn
		return s, tea.ExecProcess(m.Cmd, func(err error) tea.Msg {
			if fn == nil {
				return nil
			}
			return fn(err)
		})
	}
	var cmd tea.Cmd
	s.inner, cmd = s.inner.Update(msg)
	return s, cmd
}

func (s *standalone) View() string { return s.inner.View() }
