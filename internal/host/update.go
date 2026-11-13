package host

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles a message, then re-syncs layout and focus.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.update(msg)
	return a, tea.Batch(cmd, a.sync())
}

func (a *App) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		return nil // sync resizes the panes
	case envelope:
		return a.routeEnvelope(m)
	case clearStatusMsg:
		if m.seq == a.statusSeq {
			a.status = ""
		}
		return nil
	case execDoneMsg:
		return a.execDone(m)
	case tea.KeyMsg:
		return a.key(m)
	case tea.MouseMsg:
		return nil
	}
	// Unaddressed message (e.g. a raw tea.ExecProcess callback): best guess
	// is the focused plugin.
	return a.deliver(a.focused(), msg)
}

func (a *App) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		return a.quit()
	}
	switch a.overlay {
	case ovPalette:
		return a.paletteKey(k)
	case ovHelp:
		a.overlay = ovNone
		return nil
	}
	if a.leader {
		a.leader = false
		return a.leaderKey(k)
	}
	if s == a.cfg.LeaderKey() {
		a.leader = true
		return nil
	}
	if len(a.tabs) == 0 {
		return a.homeKey(s)
	}
	return a.deliver(a.focused(), k)
}

// leaderKey runs the host shortcut that follows the leader key.
func (a *App) leaderKey(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	t := a.curTab()
	switch s {
	case ":", "p", " ":
		return a.openPalette("")
	case "o":
		return a.openPalette("open ")
	case "v":
		return a.openPalette("split ")
	case "m":
		return a.open("plugins", false)
	case "x":
		if id := a.focusedID(); id != 0 {
			return a.closeInstance(id)
		}
	case "z":
		a.toggleZoom()
	case "r":
		if t != nil {
			t.rows = !t.rows
		}
	case "tab", "l", "j", "right", "down":
		a.cyclePane(1)
	case "shift+tab", "h", "k", "left", "up":
		a.cyclePane(-1)
	case "]", "n":
		a.cycleTab(1)
	case "[", "b":
		a.cycleTab(-1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(s[0] - '1'); i < len(a.tabs) {
			a.active = i
		}
	case "?":
		a.overlay = ovHelp
	case "q":
		return a.quit()
	case "esc":
	case a.cfg.LeaderKey():
		// leader twice sends the leader key itself to the plugin
		return a.deliver(a.focused(), k)
	default:
		return a.notify("no shortcut "+a.cfg.LeaderKey()+" "+s+" ("+a.cfg.LeaderKey()+" ? for help)", false)
	}
	return nil
}

func (a *App) toggleZoom() {
	if t := a.curTab(); t != nil && len(t.panes) > 1 {
		t.zoom = !t.zoom
	}
}

func (a *App) cyclePane(d int) {
	t := a.curTab()
	if t == nil || len(t.panes) < 2 {
		return
	}
	t.focus = (t.focus + d + len(t.panes)) % len(t.panes)
}

func (a *App) cycleTab(d int) {
	if n := len(a.tabs); n > 0 {
		a.active = (a.active + d + n) % n
	}
}

// homeKey handles the launcher shown when no tab is open.
func (a *App) homeKey(s string) tea.Cmd {
	entries := a.reg.Enabled()
	switch s {
	case "up", "k":
		a.homeSel = max(a.homeSel-1, 0)
	case "down", "j":
		a.homeSel = min(a.homeSel+1, len(entries)-1)
	case "enter", "o":
		if a.homeSel < len(entries) {
			return a.open(entries[a.homeSel].Manifest.ID, false)
		}
	case ":", "p":
		return a.openPalette("")
	case "m":
		return a.open("plugins", false)
	case "?":
		a.overlay = ovHelp
	case "q":
		return a.quit()
	}
	return nil
}
