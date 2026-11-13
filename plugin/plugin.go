// Package plugin is the public API between dock (the host) and its native
// plugins. A native plugin is an ordinary Bubble Tea model plus a Manifest
// that tells the host how to display it and how long it lives.
//
// See docs/PLUGINS.md for the full guideline.
package plugin

import (
	"fmt"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

// Display says how much screen a plugin needs.
type Display int

const (
	// DisplayTile plugins render in any rectangle the host gives them
	// (down to MinWidth x MinHeight) and can share a tab with other tiles.
	DisplayTile Display = iota
	// DisplayFull plugins always get the whole content area of their own
	// tab. The host's tab bar and status bar stay visible.
	DisplayFull
	// DisplayTakeover plugins own the entire terminal while they run; the
	// host suspends itself. This is how exec plugins run.
	DisplayTakeover
)

func (d Display) String() string {
	switch d {
	case DisplayTile:
		return "tile"
	case DisplayFull:
		return "full"
	case DisplayTakeover:
		return "takeover"
	}
	return fmt.Sprintf("display(%d)", int(d))
}

// Lifecycle says what happens to a plugin instance when it is not visible.
type Lifecycle int

const (
	// Persistent instances stay alive in the background, keep their state
	// and keep receiving their own async messages (ticks, I/O results).
	Persistent Lifecycle = iota
	// Transient instances are closed as soon as their tab is left.
	// Use this for quick forms and pickers that should start fresh.
	Transient
	// OneShot plugins run to completion and exit (exec plugins).
	OneShot
)

func (l Lifecycle) String() string {
	switch l {
	case Persistent:
		return "persistent"
	case Transient:
		return "transient"
	case OneShot:
		return "oneshot"
	}
	return fmt.Sprintf("lifecycle(%d)", int(l))
}

// Manifest describes a plugin to the host.
type Manifest struct {
	// ID is stable and unique: lowercase letters, digits and dashes. It
	// names the plugin's data and config directories, so never change it.
	ID          string
	Name        string // human name shown in tabs, home screen and palette
	Description string // one line
	Version     string

	Display   Display
	Lifecycle Lifecycle
	// Multi allows several instances at once. When false, opening the
	// plugin again focuses the existing instance.
	Multi bool
	// Smallest usable size. The host shows a placeholder instead of calling
	// View when the pane is smaller. Zero means no minimum.
	MinWidth, MinHeight int
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidID reports whether id is a valid plugin ID.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Validate checks the manifest for mistakes the host refuses to load.
func (m Manifest) Validate() error {
	if !ValidID(m.ID) {
		return fmt.Errorf("plugin id %q: use lowercase letters, digits and dashes", m.ID)
	}
	if m.Name == "" {
		return fmt.Errorf("plugin %s: Name is empty", m.ID)
	}
	return nil
}

// Plugin is implemented by every native plugin. Keep it cheap: Manifest is
// called often, New is called once per opened instance.
type Plugin interface {
	Manifest() Manifest
	// New builds a fresh instance. Do setup that can fail here (opening
	// stores, creating files) and return an error instead of panicking.
	New(ctx Context) (tea.Model, error)
}

// Context is what the host hands each new instance.
type Context struct {
	// DataDir is $XDG_DATA_HOME/dock/<id>. It exists. Put user data here.
	DataDir string
	// ConfigDir is $XDG_CONFIG_HOME/dock/plugins/<id>. It exists. Put
	// settings here.
	ConfigDir string
	Theme     Theme
}

// ---- optional interfaces a plugin's model may implement ----

// Closer is called once when the instance is closed or the host quits.
// Flush unsaved state here. It must not block for long.
type Closer interface {
	Close() error
}

// Helper returns the few most useful keys for the status bar. Called on
// every render while the instance is focused, so keep it cheap and short
// (3-6 entries); put the full list behind your own "?" screen.
type Helper interface {
	Help() []KeyHelp
}

// KeyHelp is one status bar hint.
type KeyHelp struct {
	Key, Desc string
}

// Commander contributes entries to the host's command palette while the
// instance is focused. When the user picks one, Msg is delivered to the
// instance's Update.
type Commander interface {
	Commands() []Command
}

// Command is one palette entry.
type Command struct {
	Name string // e.g. "New meeting"
	Desc string
	Msg  tea.Msg
}

// ---- messages the host sends to plugins ----

// FocusMsg is sent when the instance becomes the focused pane.
type FocusMsg struct{}

// BlurMsg is sent when the instance stops being the focused pane.
type BlurMsg struct{}

// tea.WindowSizeMsg is sent with the size of the plugin's pane (not the
// terminal) whenever it changes, including once before the first View.
