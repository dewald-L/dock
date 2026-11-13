package host

import (
	"fmt"
	"go/token"
	"reflect"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/manager"
	"github.com/dewald-L/dock/plugin"
)

// Every command a plugin returns is wrapped so its result comes back as an
// envelope addressed to that instance. That is how async results (ticks,
// I/O) reach the right plugin even when it is hidden or not focused.
type envelope struct {
	id  int
	msg tea.Msg
}

func wrap(id int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		m := cmd()
		if m == nil {
			return nil
		}
		return envelope{id, m}
	}
}

const teaPkg = "github.com/charmbracelet/bubbletea"

var cmdType = reflect.TypeOf((*tea.Cmd)(nil)).Elem()

// unpackCmds opens tea.Batch / tea.Sequence results so each inner command
// can be wrapped too. Sequence's message type is unexported, hence reflect.
func unpackCmds(m tea.Msg) (cmds []tea.Cmd, sequence, ok bool) {
	if b, isBatch := m.(tea.BatchMsg); isBatch {
		return b, false, true
	}
	t := reflect.TypeOf(m)
	if t.PkgPath() != teaPkg || t.Kind() != reflect.Slice || t.Elem() != cmdType {
		return nil, false, false
	}
	v := reflect.ValueOf(m)
	cmds = make([]tea.Cmd, v.Len())
	for i := range cmds {
		cmds[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return cmds, true, true
}

// runtimeMsg reports whether m is one of Bubble Tea's unexported messages
// the runtime itself acts on (window title, cursor, exec...). Those are
// handed back to the runtime instead of to the plugin.
func runtimeMsg(m tea.Msg) bool {
	t := reflect.TypeOf(m)
	return t.PkgPath() == teaPkg && !token.IsExported(t.Name())
}

func (a *App) routeEnvelope(e envelope) tea.Cmd {
	in := a.inst[e.id]
	if in == nil || in.dead {
		return nil // instance closed while its command was running
	}
	switch m := e.msg.(type) {
	case tea.QuitMsg, plugin.CloseMsg:
		return a.closeInstance(e.id)
	case plugin.NotifyMsg:
		return a.notify(m.Text, m.Error)
	case plugin.OpenMsg:
		return a.open(m.ID, m.Split)
	case plugin.TitleMsg:
		in.title = m.Title
		return nil
	case plugin.ExecMsg:
		id, fn := e.id, m.Fn
		return tea.ExecProcess(m.Cmd, func(err error) tea.Msg {
			if fn == nil {
				return nil
			}
			if r := fn(err); r != nil {
				return envelope{id, r}
			}
			return nil
		})
	case manager.ReloadMsg:
		return a.reload(m.Note)
	}
	if cmds, seq, ok := unpackCmds(e.msg); ok {
		for i, c := range cmds {
			cmds[i] = wrap(e.id, c)
		}
		if seq {
			return tea.Sequence(cmds...)
		}
		return tea.Batch(cmds...)
	}
	if runtimeMsg(e.msg) {
		m := e.msg
		return func() tea.Msg { return m }
	}
	return a.deliver(in, e.msg)
}

// ---- panic-safe calls into plugin code ----

func crashInfo(r any) any {
	return fmt.Sprintf("%v\n%s", r, debug.Stack())
}

func (a *App) deliver(in *instance, msg tea.Msg) (cmd tea.Cmd) {
	if in == nil || in.dead {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			in.dead, in.crash, cmd = true, crashInfo(r), nil
		}
	}()
	m, c := in.model.Update(msg)
	if m != nil {
		in.model = m
	}
	return wrap(in.id, c)
}

func (a *App) initInstance(in *instance) (cmd tea.Cmd) {
	defer func() {
		if r := recover(); r != nil {
			in.dead, in.crash, cmd = true, crashInfo(r), nil
		}
	}()
	return wrap(in.id, in.model.Init())
}

func viewOf(in *instance) (s string) {
	defer func() {
		if r := recover(); r != nil {
			in.dead, in.crash, s = true, crashInfo(r), ""
		}
	}()
	return in.model.View()
}

func safeNew(p plugin.Plugin, ctx plugin.Context) (m tea.Model, err error) {
	defer func() {
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("panic in New: %v", r)
		}
	}()
	m, err = p.New(ctx)
	if err == nil && m == nil {
		err = fmt.Errorf("New returned no model")
	}
	return m, err
}

func safeClose(in *instance) (err error) {
	c, ok := in.model.(plugin.Closer)
	if !ok {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in Close: %v", r)
		}
	}()
	return c.Close()
}

func helpOf(in *instance) (h []plugin.KeyHelp) {
	hp, ok := in.model.(plugin.Helper)
	if !ok {
		return nil
	}
	defer func() {
		if recover() != nil {
			h = nil
		}
	}()
	return hp.Help()
}

func commandsOf(in *instance) (c []plugin.Command) {
	cm, ok := in.model.(plugin.Commander)
	if !ok {
		return nil
	}
	defer func() {
		if recover() != nil {
			c = nil
		}
	}()
	return cm.Commands()
}
