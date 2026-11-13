package host

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

const paletteRows = 10

type palItem struct {
	label, hint string
	run         func() tea.Cmd
}

type palette struct {
	input    textinput.Model
	items    []palItem
	filtered []int
	sel      int
}

func (p *palette) refilter() {
	terms := strings.Fields(strings.ToLower(p.input.Value()))
	p.filtered = p.filtered[:0]
	for i, it := range p.items {
		hay := strings.ToLower(it.label + " " + it.hint)
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			p.filtered = append(p.filtered, i)
		}
	}
	p.sel = min(p.sel, max(len(p.filtered)-1, 0))
}

func (a *App) openPalette(prefill string) tea.Cmd {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "type to filter commands"
	in.Cursor.SetMode(cursor.CursorStatic) // no blink: its ticks would be unaddressed
	in.SetValue(prefill)
	in.CursorEnd()
	in.Focus()
	a.pal = palette{input: in, items: a.paletteItems()}
	a.pal.refilter()
	a.overlay = ovPalette
	return nil
}

func (a *App) paletteKey(k tea.KeyMsg) tea.Cmd {
	p := &a.pal
	switch k.String() {
	case "esc":
		a.overlay = ovNone
	case "enter":
		a.overlay = ovNone
		if p.sel < len(p.filtered) {
			return p.items[p.filtered[p.sel]].run()
		}
	case "up", "ctrl+p", "ctrl+k":
		p.sel = max(p.sel-1, 0)
	case "down", "ctrl+n", "ctrl+j":
		p.sel = min(p.sel+1, max(len(p.filtered)-1, 0))
	default:
		p.input, _ = p.input.Update(k)
		p.sel = 0
		p.refilter()
	}
	return nil
}

func (a *App) paletteItems() []palItem {
	var items []palItem
	add := func(label, hint string, run func() tea.Cmd) {
		items = append(items, palItem{label, hint, run})
	}
	t := a.curTab()

	// focused plugin's own commands first
	if in := a.focused(); in != nil {
		id := in.id
		for _, c := range commandsOf(in) {
			msg := c.Msg
			add(in.entry.Manifest.Name+": "+c.Name, c.Desc, func() tea.Cmd {
				return a.deliver(a.inst[id], msg)
			})
		}
	}
	for _, e := range a.reg.Enabled() {
		id, m := e.Manifest.ID, e.Manifest
		add("open "+m.Name, kindHint(e), func() tea.Cmd { return a.open(id, false) })
	}
	if t != nil {
		for _, e := range a.reg.Enabled() {
			id, m := e.Manifest.ID, e.Manifest
			if e.Kind != registry.Exec && a.canTile(t, m) {
				add("split "+m.Name, "tile next to the current pane", func() tea.Cmd { return a.open(id, true) })
			}
		}
		add("close pane", "ctrl+g x", func() tea.Cmd { return a.closeInstance(a.focusedID()) })
		if len(t.panes) > 1 {
			add("zoom pane", "ctrl+g z", func() tea.Cmd { a.toggleZoom(); return nil })
			add("rotate split", "ctrl+g r", func() tea.Cmd { t.rows = !t.rows; return nil })
		}
		if len(a.tabs) > 1 {
			add("next tab", "ctrl+g ]", func() tea.Cmd { a.cycleTab(1); return nil })
			add("previous tab", "ctrl+g [", func() tea.Cmd { a.cycleTab(-1); return nil })
		}
	}
	add("manage plugins", "ctrl+g m", func() tea.Cmd { return a.open("plugins", false) })
	add("reload config", "re-read "+shortPath(), func() tea.Cmd { return a.reload("config reloaded") })
	add("help", "ctrl+g ?", func() tea.Cmd { a.overlay = ovHelp; return nil })
	add("quit dock", "ctrl+g q", func() tea.Cmd { return a.quit() })
	return items
}

func kindHint(e *registry.Entry) string {
	switch {
	case e.Kind == registry.Exec:
		return "exec · takes over the terminal"
	case e.Manifest.Display == plugin.DisplayTile:
		return e.Kind.String() + " · tile"
	}
	return e.Kind.String() + " · " + e.Manifest.Display.String()
}
