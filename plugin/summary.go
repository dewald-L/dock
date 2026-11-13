package plugin

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// MaxSummaryLines is the most summary lines the home screen shows. Extra
// lines are dropped.
const MaxSummaryLines = 3

// Level is an optional status for a Summary. The host picks the colour
// from its theme.
type Level int

const (
	LevelNone Level = iota // no status marker
	LevelOk
	LevelWarn
	LevelErr
)

func (l Level) String() string {
	switch l {
	case LevelOk:
		return "ok"
	case LevelWarn:
		return "warn"
	case LevelErr:
		return "err"
	}
	return "none"
}

// Summary is what a plugin shows in its block on dock's home screen: a few
// short lines of plain text (no ANSI codes, no layout) and an optional
// status level. Lines that don't fit the block are truncated.
type Summary struct {
	Lines []string // at most MaxSummaryLines; keep each under ~40 cells
	Level Level
}

// Summarizer is an optional interface on the Plugin value (not the model),
// so the home screen can show a summary while no instance is open.
//
// The host calls Summary off the UI goroutine when the home screen is
// shown, then about every 30 seconds while it stays visible. It may run
// while an instance of the same plugin is open, and calls for different
// plugins run in parallel. So Summary must:
//
//   - be read-only and not depend on a running instance;
//   - be safe to call concurrently (with itself and with your model);
//   - be cheap: aim for under 100 ms. After 2 seconds the host gives up and
//     shows the block as not responding; a call that never returns stops
//     further calls for that plugin.
//
// A returned error is shown, muted, in the plugin's block. Panics are
// recovered and shown the same way.
type Summarizer interface {
	Summary(ctx Context) (Summary, error)
}

// Clean returns s with control characters and ANSI codes removed, embedded
// newlines split into separate lines, blank trailing lines dropped and at
// most MaxSummaryLines lines kept. The host applies it to every summary.
func (s Summary) Clean() Summary {
	var lines []string
	for _, l := range s.Lines {
		for _, part := range strings.Split(l, "\n") {
			lines = append(lines, cleanLine(part))
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > MaxSummaryLines {
		lines = lines[:MaxSummaryLines]
	}
	if s.Level < LevelNone || s.Level > LevelErr {
		s.Level = LevelNone
	}
	return Summary{Lines: lines, Level: s.Level}
}

func cleanLine(l string) string {
	l = ansi.Strip(l)
	l = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, l)
	return strings.TrimRight(l, " ")
}
