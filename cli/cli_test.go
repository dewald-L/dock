package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dewald-L/dock/internal/build"
	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/index"
)

const fixture = `{
  "version": 1,
  "plugins": [
    {"id": "lazy1on1", "name": "lazy1on1", "description": "Continuous 1:1 notes", "kind": "native",
     "package": "example.com/lazy1on1/dockplugin", "default": true, "tags": ["notes"]},
    {"id": "pinned", "name": "Pinned", "description": "A pinned plugin", "kind": "native",
     "package": "example.com/pinned", "version": "v1.2.3"},
    {"id": "osheet", "name": "OSheet", "description": "Log timesheet entries", "kind": "exec",
     "command": ["osheet"], "summary": ["osheet", "--today"], "requires": "needs osheet on PATH",
     "default": true}
  ]
}`

// sandbox points dock's config, data, cache and index at temp dirs and
// replaces the build with a recorder.
func sandbox(t *testing.T) *[][]config.NativePlugin {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	reg := filepath.Join(dir, "plugins.json")
	if err := os.WriteFile(reg, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCK_REGISTRY", reg)

	var builds [][]config.NativePlugin
	old := buildDock
	buildDock = func(_ build.Dock, ps []config.NativePlugin, out string, _ io.Writer) error {
		builds = append(builds, ps)
		os.MkdirAll(filepath.Dir(out), 0o755)
		return os.WriteFile(out, []byte("#!/bin/sh\n"), 0o755)
	}
	t.Cleanup(func() { buildDock = old })
	return &builds
}

// capture returns what f writes to stdout.
func capture(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	err := f()
	w.Close()
	os.Stdout = old
	return <-done, err
}

func TestInstallByIDNative(t *testing.T) {
	builds := sandbox(t)
	out, err := capture(t, func() error { return pluginInstall([]string{"lazy1on1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lazy1on1: example.com/lazy1on1/dockplugin (latest)") {
		t.Fatalf("output:\n%s", out)
	}
	cfg, _ := config.Load()
	if n := cfg.FindNative("example.com/lazy1on1/dockplugin"); n == nil || n.Version != "" {
		t.Fatalf("config: %+v", cfg.Native)
	}
	if len(*builds) != 1 || (*builds)[0][0].Package != "example.com/lazy1on1/dockplugin" {
		t.Fatalf("builds: %+v", *builds)
	}
}

func TestInstallByIDPinnedVersion(t *testing.T) {
	builds := sandbox(t)
	if _, err := capture(t, func() error { return pluginInstall([]string{"pinned"}) }); err != nil {
		t.Fatal(err)
	}
	if b := (*builds)[0][0]; b.Package != "example.com/pinned" || b.Version != "v1.2.3" {
		t.Fatalf("built %+v", b)
	}
	// @version on the command line overrides the pin
	if _, err := capture(t, func() error { return pluginInstall([]string{"pinned@v2.0.0"}) }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	if len(cfg.Native) != 1 || cfg.Native[0].Version != "v2.0.0" {
		t.Fatalf("config: %+v", cfg.Native)
	}
}

func TestInstallByIDExec(t *testing.T) {
	builds := sandbox(t)
	out, err := capture(t, func() error { return pluginInstall([]string{"osheet"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "added osheet: osheet") || !strings.Contains(out, "requires: needs osheet on PATH") {
		t.Fatalf("output:\n%s", out)
	}
	cfg, _ := config.Load()
	x := cfg.FindExec("osheet")
	if x == nil || x.Name != "OSheet" || x.Command[0] != "osheet" || strings.Join(x.Summary, " ") != "osheet --today" {
		t.Fatalf("config: %+v", cfg.Exec)
	}
	if len(*builds) != 0 {
		t.Fatal("exec plugins don't rebuild dock")
	}
	// again: no error, no duplicate
	out, err = capture(t, func() error { return pluginInstall([]string{"osheet"}) })
	if err != nil || !strings.Contains(out, "already added") {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ = config.Load()
	if len(cfg.Exec) != 1 {
		t.Fatalf("duplicated: %+v", cfg.Exec)
	}
	if err := pluginInstall([]string{"osheet@v1"}); err == nil {
		t.Fatal("@version on an exec plugin")
	}
}

func TestInstallUnknownID(t *testing.T) {
	builds := sandbox(t)
	err := pluginInstall([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), `no plugin "nope" in the index`) {
		t.Fatalf("err = %v", err)
	}
	if len(*builds) != 0 {
		t.Fatal("built anyway")
	}
	if _, err := os.Stat(config.Path()); !os.IsNotExist(err) {
		t.Fatal("config written")
	}
}

func TestInstallPackagePathSkipsIndex(t *testing.T) {
	builds := sandbox(t)
	t.Setenv("DOCK_REGISTRY", filepath.Join(t.TempDir(), "missing.json")) // unreadable index
	if _, err := capture(t, func() error { return pluginInstall([]string{"example.com/x/dockplugin@v0.1.0"}) }); err != nil {
		t.Fatal(err)
	}
	if b := (*builds)[0][0]; b.Package != "example.com/x/dockplugin" || b.Version != "v0.1.0" {
		t.Fatalf("built %+v", b)
	}
}

func TestInstallDefaults(t *testing.T) {
	builds := sandbox(t)
	out, err := capture(t, func() error { return pluginInstall([]string{"--defaults"}) })
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	if len(cfg.Native) != 1 || cfg.Native[0].Package != "example.com/lazy1on1/dockplugin" {
		t.Fatalf("native: %+v", cfg.Native)
	}
	if cfg.FindExec("osheet") == nil || cfg.FindNative("example.com/pinned") != nil {
		t.Fatalf("config: %+v", cfg)
	}
	if len(*builds) != 1 {
		t.Fatalf("want one build, got %d", len(*builds))
	}
	if !strings.Contains(out, "requires: needs osheet on PATH") {
		t.Fatalf("output:\n%s", out)
	}
	// rerun (an update): still one native entry, one exec entry
	if _, err := capture(t, func() error { return pluginInstall([]string{"--defaults"}) }); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load()
	if len(cfg.Native) != 1 || len(cfg.Exec) != 1 || len(*builds) != 2 {
		t.Fatalf("rerun: %+v builds=%d", cfg, len(*builds))
	}
	if err := pluginInstall([]string{"--defaults", "lazy1on1"}); err == nil {
		t.Fatal("--defaults with an argument")
	}
}

func TestInstallDefaultsFailedBuildRestoresConfig(t *testing.T) {
	sandbox(t)
	buildDock = func(build.Dock, []config.NativePlugin, string, io.Writer) error { return io.ErrUnexpectedEOF }
	if _, err := capture(t, func() error { return pluginInstall([]string{"--defaults"}) }); err == nil {
		t.Fatal("want the build error")
	}
	cfg, _ := config.Load()
	if len(cfg.Native) != 0 {
		t.Fatalf("native not restored: %+v", cfg.Native)
	}
}

func TestSearch(t *testing.T) {
	sandbox(t)
	cfg := &config.Config{Exec: []config.ExecPlugin{{ID: "osheet", Command: []string{"osheet"}}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := pluginSearch(&b, ""); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("output:\n%s", b.String())
	}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		installed := f[len(f)-1] == "installed"
		if installed != (f[0] == "osheet") {
			t.Fatalf("installed marker wrong in %q", l)
		}
	}
	if !strings.Contains(lines[1], "lazy1on1") || !strings.Contains(lines[1], "Continuous 1:1 notes") {
		t.Fatalf("row: %q", lines[1])
	}

	b.Reset()
	if err := pluginSearch(&b, "timesheet"); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "osheet") || strings.Contains(out, "lazy1on1") {
		t.Fatalf("filtered output:\n%s", out)
	}
	b.Reset()
	pluginSearch(&b, "zzz")
	if !strings.Contains(b.String(), `no plugins match "zzz"`) {
		t.Fatalf("no match output: %s", b.String())
	}
}

func TestSearchNoIndex(t *testing.T) {
	sandbox(t)
	t.Setenv("DOCK_REGISTRY", filepath.Join(t.TempDir(), "missing.json"))
	if err := pluginSearch(io.Discard, ""); err == nil || !strings.Contains(err.Error(), "plugin index") {
		t.Fatalf("err = %v", err)
	}
}

func TestInfoNotInstalled(t *testing.T) {
	sandbox(t)
	out, err := capture(t, func() error { return pluginInfo("pinned") })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/pinned", "v1.2.3", "not installed (dock plugin install pinned)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	out, _ = capture(t, func() error { return pluginInfo("osheet") })
	if !strings.Contains(out, "needs osheet on PATH") || !strings.Contains(out, "osheet --today") {
		t.Fatalf("exec info:\n%s", out)
	}
	if _, err := capture(t, func() error { return pluginInfo("nope") }); err == nil {
		t.Fatal("unknown id")
	}
	// installed plugins don't touch the index
	t.Setenv("DOCK_REGISTRY", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := capture(t, func() error { return pluginInfo("plugins") }); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultRegistry(t *testing.T) {
	sandbox(t)
	t.Setenv("DOCK_REGISTRY", "")
	if index.Source(&config.Config{}) != index.DefaultURL {
		t.Fatal("default registry")
	}
}
