package plugin

import (
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// Commands a plugin returns from Init/Update to talk to the host. They are
// addressed automatically: the host knows which instance returned them.

// NotifyMsg shows a message in the host's status bar.
type NotifyMsg struct {
	Text  string
	Error bool
}

// CloseMsg asks the host to close the instance that returned it.
// Returning tea.Quit does the same thing: a plugin can never quit the host.
type CloseMsg struct{}

// OpenMsg asks the host to open (or focus) another plugin by ID.
type OpenMsg struct {
	ID    string
	Split bool // try to tile it next to the current pane
}

// TitleMsg sets the instance's title in the tab bar and pane border.
type TitleMsg struct {
	Title string
}

// ExecMsg suspends the host and runs an external program in the terminal.
// Fn's result is delivered back to the instance that asked.
type ExecMsg struct {
	Cmd *exec.Cmd
	Fn  func(error) tea.Msg
}

// Notify shows text in the status bar for a few seconds.
func Notify(text string) tea.Cmd {
	return func() tea.Msg { return NotifyMsg{Text: text} }
}

// Error shows err in the status bar, styled as an error.
func Error(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return func() tea.Msg { return NotifyMsg{Text: err.Error(), Error: true} }
}

// Close closes this instance.
func Close() tea.Cmd {
	return func() tea.Msg { return CloseMsg{} }
}

// Open opens or focuses another plugin.
func Open(id string, split bool) tea.Cmd {
	return func() tea.Msg { return OpenMsg{ID: id, Split: split} }
}

// SetTitle changes this instance's title.
func SetTitle(title string) tea.Cmd {
	return func() tea.Msg { return TitleMsg{Title: title} }
}

// Exec runs c with the terminal handed over (e.g. $EDITOR). Use this
// instead of tea.ExecProcess so the result reaches this instance even if
// the user's focus is elsewhere when it finishes.
func Exec(c *exec.Cmd, fn func(error) tea.Msg) tea.Cmd {
	return func() tea.Msg { return ExecMsg{Cmd: c, Fn: fn} }
}
