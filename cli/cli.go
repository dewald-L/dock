// Package cli is dock's command line and entry point. The dock command is
// just cli.Main(); `dock build` generates a main that also imports the
// configured native plugin packages and calls the same cli.Main().
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/build"
	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/host"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
)

// Version of dock.
const Version = "0.2.0"

const usage = `dock - a terminal workspace for plugins

Usage:
  dock [plugin-id...]          start, opening the given plugins (default: config "startup")

  dock plugin list             list plugins
  dock plugin info <id>        show one plugin
  dock plugin enable <id>      enable a plugin
  dock plugin disable <id>     disable a plugin

  dock plugin install [--path DIR] <go-package>[@version]
                               add a native (Go) plugin and rebuild dock with it;
                               --path builds from a local checkout of its module
  dock plugin add [flags] <id> [--] <command> [args...]
                               register an external program as an exec plugin
      --name NAME              display name (default: id)
      --desc TEXT              one-line description
      --dir DIR                working directory (default: home)
      --pause                  wait for Enter after it exits, so its output stays readable
  dock plugin remove <id>      exec: unregister; native: uninstall and rebuild

  dock build [--out PATH]      rebuild dock with the configured native plugins
                               (default output: ~/.local/share/dock/bin/dock)
  dock config                  print the config file path
  dock version

Inside dock every host shortcut starts with the leader key (ctrl+g by
default); ctrl+g ? shows them all.
`

// Main runs the dock command line and exits.
func Main() {
	args := os.Args[1:]
	var err error
	if len(args) > 0 {
		switch args[0] {
		case "plugin", "plugins":
			err = pluginCmd(args[1:])
		case "build":
			err = buildCmd(args[1:])
		case "config":
			fmt.Println(config.Path())
		case "version", "--version", "-v":
			fmt.Println("dock", Version)
		case "help", "--help", "-h":
			fmt.Print(usage)
		default:
			err = run(args)
		}
	} else {
		err = run(nil)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dock:", err)
		os.Exit(1)
	}
}

func load() (*config.Config, *registry.Registry, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	reg, errs := registry.Build(cfg, host.SystemPlugins(nil), plugin.Registered())
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "dock: warning:", e)
	}
	return cfg, reg, nil
}

func run(ids []string) error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "-") {
			return fmt.Errorf("unknown flag %s (see dock help)", id)
		}
		if reg.Get(id) == nil {
			return fmt.Errorf("unknown plugin %q (see dock plugin list)", id)
		}
	}
	app := host.New(host.Options{Config: cfg, Native: plugin.Registered(), Open: ids, Version: Version})
	_, err = tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func pluginCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list", "ls":
		return pluginList()
	case "info":
		return withID(args, pluginInfo)
	case "enable":
		return withID(args, func(id string) error { return setEnabled(id, true) })
	case "disable":
		return withID(args, func(id string) error { return setEnabled(id, false) })
	case "add":
		return pluginAdd(args[1:])
	case "install":
		return pluginInstall(args[1:])
	case "remove", "rm", "uninstall":
		return withID(args, pluginRemove)
	case "packages": // internal: used by install to verify a build
		for _, p := range plugin.Registered() {
			fmt.Println(plugin.PackageOf(p))
		}
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", "plugin "+args[0], usage)
}

func withID(args []string, f func(string) error) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dock plugin %s <id>", args[0])
	}
	return f(args[1])
}

// ---- list / info / enable ----

func pluginList() error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tDISPLAY\tLIFECYCLE\tSTATUS")
	built := map[string]bool{}
	for _, e := range reg.Entries {
		st := "enabled"
		if !e.Enabled {
			st = "disabled"
		}
		built[e.Package] = true
		m := e.Manifest
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, e.Kind, m.Display, m.Lifecycle, st)
	}
	for _, n := range cfg.Native {
		if !built[n.Package] {
			fmt.Fprintf(tw, "?\t%s\tnative\t-\t-\tnot in this binary (run dock build)\n", n.Package)
		}
	}
	return tw.Flush()
}

func pluginInfo(id string) error {
	_, reg, err := load()
	if err != nil {
		return err
	}
	e := reg.Get(id)
	if e == nil {
		return fmt.Errorf("unknown plugin %q", id)
	}
	m := e.Manifest
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	row("id", m.ID)
	row("name", m.Name)
	row("description", m.Description)
	row("version", m.Version)
	row("kind", e.Kind.String())
	row("package", e.Package)
	row("enabled", fmt.Sprint(e.Enabled))
	row("display", m.Display.String())
	row("lifecycle", m.Lifecycle.String())
	row("multi", fmt.Sprint(m.Multi))
	if m.MinWidth > 0 || m.MinHeight > 0 {
		row("min size", fmt.Sprintf("%dx%d", m.MinWidth, m.MinHeight))
	}
	if e.Exec != nil {
		row("command", strings.Join(e.Exec.Command, " "))
		row("dir", e.Exec.Dir)
		row("pause", fmt.Sprint(e.Exec.Pause))
	}
	row("data dir", config.PluginDataDir(m.ID))
	row("config dir", config.PluginConfigDir(m.ID))
	return tw.Flush()
}

func setEnabled(id string, on bool) error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	e := reg.Get(id)
	switch {
	case e == nil:
		return fmt.Errorf("unknown plugin %q", id)
	case e.Kind == registry.System:
		return fmt.Errorf("%s is built in and always enabled", id)
	}
	cfg.SetEnabled(id, on)
	return cfg.Save()
}

// ---- exec plugins ----

func pluginAdd(args []string) error {
	fs := flag.NewFlagSet("dock plugin add", flag.ContinueOnError)
	name := fs.String("name", "", "display name")
	desc := fs.String("desc", "", "description")
	dir := fs.String("dir", "", "working directory")
	pause := fs.Bool("pause", false, "wait for Enter after exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) < 2 {
		return errors.New("usage: dock plugin add [flags] <id> [--] <command> [args...]")
	}
	id, command := rest[0], rest[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return errors.New("missing command")
	}
	if !plugin.ValidID(id) {
		return fmt.Errorf("invalid id %q: use lowercase letters, digits and dashes", id)
	}
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	if reg.Get(id) != nil || cfg.FindExec(id) != nil {
		return fmt.Errorf("a plugin with id %q already exists", id)
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		fmt.Fprintf(os.Stderr, "dock: warning: %s is not on PATH right now\n", command[0])
	}
	cfg.Exec = append(cfg.Exec, config.ExecPlugin{
		ID: id, Name: *name, Description: *desc, Command: command, Dir: expand(*dir), Pause: *pause,
	})
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("added %s: %s\n", id, strings.Join(command, " "))
	return nil
}

// ---- native plugins ----

func pluginInstall(args []string) error {
	fs := flag.NewFlagSet("dock plugin install", flag.ContinueOnError)
	path := fs.String("path", "", "local checkout of the plugin's module")
	if err := fs.Parse(args); err != nil {
		return err
	}
	np := config.NativePlugin{Path: expand(*path)}
	switch fs.NArg() {
	case 0:
		if np.Path == "" {
			return errors.New("usage: dock plugin install [--path DIR] <go-package>[@version]")
		}
		mod, err := build.ModulePath(np.Path)
		if err != nil {
			return err
		}
		np.Package = mod
	case 1:
		np.Package, np.Version, _ = strings.Cut(fs.Arg(0), "@")
	default:
		return errors.New("install one package at a time")
	}
	if np.Path != "" {
		abs, err := filepath.Abs(np.Path)
		if err != nil {
			return err
		}
		np.Path, np.Version = abs, ""
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	before := slices.Clone(cfg.Native)
	cfg.RemoveNative(np.Package)
	cfg.Native = append(cfg.Native, np)
	if err := rebuild(cfg, before, config.BinPath()); err != nil {
		return err
	}
	if !binaryHas(config.BinPath(), np.Package) {
		fmt.Fprintf(os.Stderr, "dock: warning: %s built, but it registered no plugin. Does it call plugin.Register in an init function?\n", np.Package)
	}
	return nil
}

func pluginRemove(arg string) error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	if cfg.FindExec(arg) != nil {
		cfg.RemoveExec(arg)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("removed", arg)
		return nil
	}
	pkg := arg
	if e := reg.Get(arg); e != nil {
		switch e.Kind {
		case registry.System:
			return fmt.Errorf("%s is built in", arg)
		case registry.Native:
			pkg = e.Package
		}
	}
	if cfg.FindNative(pkg) == nil {
		return fmt.Errorf("%q is not an installed plugin (see dock plugin list)", arg)
	}
	before := slices.Clone(cfg.Native)
	cfg.RemoveNative(pkg)
	// forget per-plugin settings for every plugin that package provided
	for _, e := range reg.Entries {
		if e.Package == pkg {
			cfg.SetEnabled(e.Manifest.ID, true)
			cfg.Startup = slices.DeleteFunc(cfg.Startup, func(s string) bool { return s == e.Manifest.ID })
		}
	}
	return rebuild(cfg, before, config.BinPath())
}

func buildCmd(args []string) error {
	fs := flag.NewFlagSet("dock build", flag.ContinueOnError)
	out := fs.String("out", config.BinPath(), "output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return rebuild(cfg, cfg.Native, expand(*out))
}

// rebuild saves cfg, builds dock with cfg.Native into out, and restores
// the previous native list if the build fails.
func rebuild(cfg *config.Config, previous []config.NativePlugin, out string) error {
	if err := cfg.Save(); err != nil {
		return err
	}
	dock := build.ResolveDock(cfg)
	fmt.Printf("building dock with %d native plugin(s)\n", len(cfg.Native))
	if err := build.Build(dock, cfg.Native, out, prefixWriter{}); err != nil {
		cfg.Native = previous
		if serr := cfg.Save(); serr != nil {
			return fmt.Errorf("%v (and restoring the config failed: %v)", err, serr)
		}
		return fmt.Errorf("build failed, config left unchanged:\n%v", err)
	}
	fmt.Println("built", out)
	pathHint(out)
	return nil
}

type prefixWriter struct{}

func (prefixWriter) Write(b []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		fmt.Println("  " + l)
	}
	return len(b), nil
}

// binaryHas reports whether the dock binary at bin registers plugins from pkg.
func binaryHas(bin, pkg string) bool {
	b, err := exec.Command(bin, "plugin", "packages").Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l == pkg || strings.HasPrefix(l, pkg+"/") {
			return true
		}
	}
	return false
}

// pathHint tells the user when `dock` on PATH is not the binary just built.
func pathHint(out string) {
	onPath, err := exec.LookPath("dock")
	if err != nil {
		fmt.Printf("add it to your PATH, e.g.: ln -s %s ~/.local/bin/dock\n", out)
		return
	}
	resolved, _ := filepath.EvalSymlinks(onPath)
	want, _ := filepath.EvalSymlinks(out)
	if resolved != want {
		fmt.Printf("note: `dock` on your PATH is %s, not this build. To use it:\n  ln -sf %s %s\n", onPath, out, onPath)
	}
}

func expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
