package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/execplugin"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

// Home screen summaries. They are fetched in tea.Cmds (never in Update or
// View) when the home screen is shown, then every summaryEvery while it
// stays visible. Each plugin's call runs in its own command with its own
// timeout and panic recovery, so a slow or broken plugin only affects its
// own block.

const (
	summaryEvery   = 30 * time.Second
	summaryTimeout = 2 * time.Second
)

var (
	errSummaryTimeout = errors.New("timed out")
	errNotResponding  = errors.New("not responding")
)

// sumState is what the home screen knows about one plugin's summary.
type sumState struct {
	sum     plugin.Summary // last good summary (valid when ok)
	ok      bool
	err     string // last failure; cleared by a success
	loading bool   // a fetch command is in flight
	// busy is set while the plugin's Summary call is running. A call that
	// outlives its timeout keeps it set, so no second call piles up.
	busy *atomic.Bool
}

// summaryMsg is the result of one fetch.
type summaryMsg struct {
	id  string
	sum plugin.Summary
	err error
}

// summaryTickMsg triggers a refresh; gen ties it to one showing of the
// home screen so ticks from an earlier showing are ignored.
type summaryTickMsg struct{ gen int }

type summaryFunc func(context.Context) (plugin.Summary, error)

// summarizer returns how to fetch e's summary, or nil if it has none.
func (a *App) summarizer(e *registry.Entry) summaryFunc {
	id, theme := e.Manifest.ID, a.theme
	switch {
	case e.Kind == registry.Exec:
		x := e.Exec
		if x == nil || len(x.Summary) == 0 {
			return nil
		}
		return func(ctx context.Context) (plugin.Summary, error) {
			lines, err := execplugin.Summary(ctx, x)
			return plugin.Summary{Lines: lines}, err
		}
	case e.Plugin != nil:
		s, ok := e.Plugin.(plugin.Summarizer)
		if !ok {
			return nil
		}
		return func(context.Context) (plugin.Summary, error) {
			data, conf, err := config.EnsurePluginDirs(id)
			if err != nil {
				return plugin.Summary{}, err
			}
			return s.Summary(plugin.Context{DataDir: data, ConfigDir: conf, Theme: theme})
		}
	}
	return nil
}

func (a *App) sumState(id string) *sumState {
	st := a.sums[id]
	if st == nil {
		st = &sumState{busy: new(atomic.Bool)}
		a.sums[id] = st
	}
	return st
}

// fetchSummaries starts a fetch for every home plugin that has a
// summary and isn't already being fetched.
func (a *App) fetchSummaries() tea.Cmd {
	var cmds []tea.Cmd
	for _, he := range a.homeEntries() {
		fn := a.summarizer(he.e)
		if fn == nil {
			continue
		}
		st := a.sumState(he.e.Manifest.ID)
		if st.loading {
			continue
		}
		st.loading = true
		cmds = append(cmds, fetchSummary(he.e.Manifest.ID, fn, st.busy, a.sumTimeout))
	}
	return tea.Batch(cmds...)
}

// fetchSummary calls fn in its own goroutine and returns its result, or a
// timeout after d. Panics become errors.
func fetchSummary(id string, fn summaryFunc, busy *atomic.Bool, d time.Duration) tea.Cmd {
	return func() tea.Msg {
		if !busy.CompareAndSwap(false, true) {
			return summaryMsg{id: id, err: errNotResponding}
		}
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		ch := make(chan summaryMsg, 1)
		go func() {
			defer busy.Store(false)
			defer func() {
				if r := recover(); r != nil {
					ch <- summaryMsg{id: id, err: fmt.Errorf("panic: %v", r)}
				}
			}()
			s, err := fn(ctx)
			ch <- summaryMsg{id: id, sum: s, err: err}
		}()
		select {
		case m := <-ch:
			return m
		case <-ctx.Done():
			return summaryMsg{id: id, err: errSummaryTimeout}
		}
	}
}

func (a *App) gotSummary(m summaryMsg) {
	st := a.sumState(m.id)
	st.loading = false
	if m.err != nil {
		msg := strings.SplitN(m.err.Error(), "\n", 2)[0]
		if errors.Is(m.err, context.DeadlineExceeded) {
			msg = errSummaryTimeout.Error()
		}
		st.err = msg
		return
	}
	st.sum, st.ok, st.err = m.sum.Clean(), true, ""
}

func (a *App) summaryTick() tea.Cmd {
	gen := a.homeGen
	return tea.Tick(a.sumEvery, func(time.Time) tea.Msg { return summaryTickMsg{gen} })
}

func (a *App) onSummaryTick(m summaryTickMsg) tea.Cmd {
	if m.gen != a.homeGen || !a.homeOn {
		return nil // the home screen was hidden since: stop refreshing
	}
	return tea.Batch(a.fetchSummaries(), a.summaryTick())
}

// syncHome notices the home screen appearing or disappearing. When it
// appears it fixes the plugin numbers, fetches summaries and starts the
// refresh timer; when it disappears the timer's generation is retired.
func (a *App) syncHome() tea.Cmd {
	visible := len(a.tabs) == 0
	if visible == a.homeOn {
		return nil
	}
	a.homeOn = visible
	a.homeGen++
	if !visible {
		return nil
	}
	a.homeIDs = nil
	a.refreshHomeIDs()
	a.homeSel = min(a.homeSel, max(len(a.homeEntries())-1, 0))
	return tea.Batch(a.fetchSummaries(), a.summaryTick())
}
