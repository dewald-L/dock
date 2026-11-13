package plugin

import "github.com/charmbracelet/lipgloss"

// Theme is the host's palette. Plugins should style with these instead of
// hard-coded colours so every plugin looks like part of one app. The
// defaults are ANSI colours, so they follow the terminal's own theme.
type Theme struct {
	Accent lipgloss.Color // focus, selection, headings
	Muted  lipgloss.Color // secondary text, hints
	Border lipgloss.Color // unfocused borders and separators
	Ok     lipgloss.Color
	Warn   lipgloss.Color
	Err    lipgloss.Color
}

// DefaultTheme is the theme the host uses unless configured otherwise.
func DefaultTheme() Theme {
	return Theme{
		Accent: lipgloss.Color("12"),
		Muted:  lipgloss.Color("8"),
		Border: lipgloss.Color("8"),
		Ok:     lipgloss.Color("2"),
		Warn:   lipgloss.Color("3"),
		Err:    lipgloss.Color("1"),
	}
}
