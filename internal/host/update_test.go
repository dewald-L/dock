package host

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/config"
)

func newUpdateApp(t *testing.T, w, h int, check func() string) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "cfg"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	a := New(Options{Config: &config.Config{}, Version: "0.2.0", CheckUpdates: check})
	run(a, a.Init())
	send(a, tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

func TestUpdateNotice(t *testing.T) {
	var calls atomic.Int32
	a := newUpdateApp(t, 120, 40, func() string { calls.Add(1); return "dock, lazy1on1" })
	if calls.Load() != 1 {
		t.Fatalf("checked %d times", calls.Load())
	}
	f := frameAt(t, a, 120, 40)
	if !strings.Contains(f, "update available: dock, lazy1on1 · ctrl+g : update dock") {
		t.Fatalf("no status notice:\n%s", f)
	}
	if !strings.Contains(f, "0.2.0 · update available") {
		t.Fatalf("no home marker:\n%s", f)
	}
	// it stays on the home screen after the status clears
	send(a, clearStatusMsg{a.statusSeq})
	f = frameAt(t, a, 120, 40)
	if strings.Contains(f, "ctrl+g : update dock") || !strings.Contains(f, "update available") {
		t.Fatalf("after the status cleared:\n%s", f)
	}
	for _, size := range [][2]int{{60, 20}, {30, 8}} {
		send(a, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frameAt(t, a, size[0], size[1]) // fits
	}
}

func TestNoUpdateNoNotice(t *testing.T) {
	a := newUpdateApp(t, 120, 40, func() string { return "" })
	if f := frameAt(t, a, 120, 40); strings.Contains(f, "update") {
		t.Fatalf("notice without an update:\n%s", f)
	}
}

func TestUpdateCheckPanicIsHarmless(t *testing.T) {
	a := newUpdateApp(t, 120, 40, func() string { panic("boom") })
	if f := frameAt(t, a, 120, 40); strings.Contains(f, "update available") {
		t.Fatalf("%s", f)
	}
}

func TestNoCheckWithoutOption(t *testing.T) {
	a := newUpdateApp(t, 120, 40, nil)
	if a.updates != "" {
		t.Fatal("checked without CheckUpdates")
	}
}

func TestUpdatePaletteItem(t *testing.T) {
	a := newUpdateApp(t, 120, 40, func() string { return "lazy1on1" })
	var found bool
	for _, it := range a.paletteItems() {
		if it.label == "update dock" {
			found = true
			if it.hint != "available: lazy1on1" {
				t.Fatalf("hint %q", it.hint)
			}
		}
	}
	if !found {
		t.Fatal("no update dock palette item")
	}
	send(a, updateDoneMsg{})
	if a.updates != "" || !strings.Contains(a.status, "restart dock") {
		t.Fatalf("after the update: %q %q", a.updates, a.status)
	}
	send(a, updateDoneMsg{errors.New("exit status 1")})
	if !a.statusErr || !strings.Contains(a.status, "update failed") {
		t.Fatalf("failure: %q", a.status)
	}
}
