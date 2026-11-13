// Package cli is dock's command line and entry point. The dock command is
// just cli.Main(); `dock build` generates a main that also imports the
// configured native plugin packages and calls the same cli.Main().
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/dewald-L/dock/internal/index"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/internal/update"
	"github.com/dewald-L/dock/plugin"
)

// Version of dock.
const Version = "0.2.0"

const usage = `dock - a terminal workspace for plugins

Usage:
  dock [plugin-id...]          start, opening the given plugins (default: config "startup")

  dock plugin list             list plugins
  dock plugin search [term]    list plugins in the index (config "registry")
  dock plugin info <id>        show one plugin, installed or in the index
  dock plugin enable <id>      enable a plugin
  dock plugin disable <id>     disable a plugin

  dock plugin install [--path DIR] <id>|<go-package>[@version]
                               install a plugin from the index by id, or a native
                               (Go) package, and rebuild dock with it; --path
                               builds from a local checkout of its module
  dock plugin install --defaults
                               install every plugin the index marks as default
  dock plugin add [flags] <id> [--] <command> [args...]
                               register an external program as an exec plugin
      --name NAME              display name (default: id)
      --desc TEXT              one-line description
      --dir DIR                working directory (default: home)
      --pause                  wait for Enter after it exits, so its output stays readable
      --summary CMD            shell command whose output (3 lines) shows on the home screen
  dock plugin remove <id>      exec: unregister; native: uninstall and rebuild

  dock build [--out PATH]      rebuild dock with the configured native plugins
                               (default output: ~/.local/share/dock/bin/dock)
  dock update [--check]        update dock and its native plugins and rebuild;
                               --check only shows what is new
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
		case "update", "upgrade":
			err = updateCmd(os.Stdout, args[1:])
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
	opts := host.Options{Config: cfg, Native: plugin.Registered(), Open: ids, Version: Version}
	if !update.Disabled(cfg) {
		opts.CheckUpdates = func() string { return update.Cached(cfg).Summary() }
	}
	app := host.New(opts)
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
	case "search":
		if len(args) > 2 {
			return errors.New("usage: dock plugin search [term]")
		}
		return pluginSearch(os.Stdout, strings.Join(args[1:], " "))
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
		return indexInfo(id)
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
		row("summary", strings.Join(e.Exec.Summary, " "))
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
	summary := fs.String("summary", "", "home screen summary command (run with sh -c)")
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
	x := config.ExecPlugin{
		ID: id, Name: *name, Description: *desc, Command: command, Dir: expand(*dir), Pause: *pause,
	}
	if *summary != "" {
		x.Summary = []string{"sh", "-c", *summary}
	}
	cfg.Exec = append(cfg.Exec, x)
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
	defaults := fs.Bool("defaults", false, "install the index's default plugins")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *defaults {
		if fs.NArg() > 0 || *path != "" {
			return errors.New("--defaults takes no other arguments")
		}
		return installDefaults()
	}
	np := config.NativePlugin{Path: expand(*path)}
	switch fs.NArg() {
	case 0:
		if np.Path == "" {
			return errors.New("usage: dock plugin install [--path DIR] <id>|<go-package>[@version]")
		}
		mod, err := build.ModulePath(np.Path)
		if err != nil {
			return err
		}
		np.Package = mod
	case 1:
		arg, version, _ := strings.Cut(fs.Arg(0), "@")
		if strings.Contains(arg, "/") {
			np.Package, np.Version = arg, version
			break
		}
		// an id: look it up in the index
		e, err := lookupIndex(arg)
		if err != nil {
			return err
		}
		if e.Kind == index.KindExec {
			if np.Path != "" || version != "" {
				return fmt.Errorf("%s is an exec plugin: --path and @version don't apply", arg)
			}
			return installExec(e)
		}
		np.Package, np.Version = nativeFromIndex(e, version)
		fmt.Printf("%s: %s\n", e.ID, pkgVersion(np.Package, np.Version))
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
	if err := buildDock(dock, cfg.Native, out, prefixWriter{}); err != nil {
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

// buildDock compiles dock; tests replace it.
var buildDock = build.Build

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
// install.sh sets DOCK_NO_PATH_HINT: it links the binary itself afterwards.
func pathHint(out string) {
	if os.Getenv("DOCK_NO_PATH_HINT") != "" {
		return
	}
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

// ---- the plugin index ----

// loadIndex reads the configured plugin index, noting on stderr when it
// came from the cache because the fetch failed.
func loadIndex(cfg *config.Config) (*index.Result, error) {
	r, err := index.Load(index.Source(cfg), false)
	if err != nil {
		return nil, err
	}
	if r.Stale {
		fmt.Fprintf(os.Stderr, "dock: using the cached plugin index %s: %v\n", r.Note(), r.Err)
	}
	return r, nil
}

// lookupIndex finds id in the plugin index.
func lookupIndex(id string) (index.Entry, error) {
	cfg, err := config.Load()
	if err != nil {
		return index.Entry{}, err
	}
	r, err := loadIndex(cfg)
	if err != nil {
		return index.Entry{}, err
	}
	e := r.Get(id)
	if e == nil {
		return index.Entry{}, fmt.Errorf("no plugin %q in the index (see dock plugin search)", id)
	}
	return *e, nil
}

// nativeFromIndex returns the package and version to install for a native
// index entry. A version given on the command line overrides the pin.
func nativeFromIndex(e index.Entry, override string) (pkg, version string) {
	if override != "" {
		return e.Package, override
	}
	return e.Package, e.Version
}

// execFromIndex is the config entry for an exec index entry.
func execFromIndex(e index.Entry) config.ExecPlugin {
	return config.ExecPlugin{
		ID: e.ID, Name: e.Name, Description: e.Description,
		Command: slices.Clone(e.Command), Summary: slices.Clone(e.Summary),
	}
}

func pkgVersion(pkg, version string) string {
	if version == "" {
		return pkg + " (latest)"
	}
	return pkg + "@" + version
}

// installExec registers an exec index entry. dock can't install the program
// itself, so it prints the entry's requirements.
func installExec(e index.Entry) error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	if added, err := addExec(cfg, reg, e); err != nil || !added {
		return err
	}
	return cfg.Save()
}

// addExec adds e to cfg unless it is already there. It reports whether it
// changed cfg.
func addExec(cfg *config.Config, reg *registry.Registry, e index.Entry) (bool, error) {
	if cfg.FindExec(e.ID) != nil {
		fmt.Printf("%s is already added\n", e.ID)
		return false, nil
	}
	if reg.Get(e.ID) != nil {
		return false, fmt.Errorf("a plugin with id %q already exists", e.ID)
	}
	cfg.Exec = append(cfg.Exec, execFromIndex(e))
	fmt.Printf("added %s: %s\n", e.ID, strings.Join(e.Command, " "))
	if e.Requires != "" {
		fmt.Printf("  requires: %s\n", e.Requires)
	}
	if _, err := exec.LookPath(e.Command[0]); err != nil {
		fmt.Fprintf(os.Stderr, "dock: warning: %s is not on PATH right now\n", e.Command[0])
	}
	return true, nil
}

// installDefaults installs every default entry of the index with a single
// rebuild. It always rebuilds, so a dock binary exists afterwards even
// when the index has no native defaults.
func installDefaults() error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	r, err := loadIndex(cfg)
	if err != nil {
		return err
	}
	defs := r.Defaults()
	if len(defs) == 0 {
		fmt.Println("the plugin index has no default plugins")
	}
	before := slices.Clone(cfg.Native)
	var pkgs []string
	for _, e := range defs {
		switch e.Kind {
		case index.KindExec:
			if _, err := addExec(cfg, reg, e); err != nil {
				return err
			}
		case index.KindNative:
			pkg, version := nativeFromIndex(e, "")
			fmt.Printf("%s: %s\n", e.ID, pkgVersion(pkg, version))
			cfg.RemoveNative(pkg)
			cfg.Native = append(cfg.Native, config.NativePlugin{Package: pkg, Version: version})
			pkgs = append(pkgs, pkg)
		}
	}
	if err := rebuild(cfg, before, config.BinPath()); err != nil {
		return err
	}
	for _, pkg := range pkgs {
		if !binaryHas(config.BinPath(), pkg) {
			fmt.Fprintf(os.Stderr, "dock: warning: %s built, but it registered no plugin\n", pkg)
		}
	}
	return nil
}

// pluginSearch prints the index entries matching term.
func pluginSearch(w io.Writer, term string) error {
	cfg, reg, err := load()
	if err != nil {
		return err
	}
	r, err := loadIndex(cfg)
	if err != nil {
		return err
	}
	es := r.Search(term)
	if len(es) == 0 {
		if term == "" {
			fmt.Fprintln(w, "the plugin index is empty")
		} else {
			fmt.Fprintf(w, "no plugins match %q\n", term)
		}
		return nil
	}
	known := registeredIDs(reg)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tDESCRIPTION\tSTATUS")
	for _, e := range es {
		st := ""
		if e.Installed(cfg, known) {
			st = "installed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.DisplayName(), e.Kind, e.Description, st)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !r.Stale && r.Cached {
		fmt.Fprintf(os.Stderr, "dock: plugin index %s\n", r.Note())
	}
	return nil
}

func registeredIDs(reg *registry.Registry) []string {
	ids := make([]string, 0, len(reg.Entries))
	for _, e := range reg.Entries {
		ids = append(ids, e.Manifest.ID)
	}
	return ids
}

// indexInfo describes a plugin that is in the index but not installed.
func indexInfo(id string) error {
	e, err := lookupIndex(id)
	if err != nil {
		return fmt.Errorf("unknown plugin %q: not installed, and %v", id, err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	row("id", e.ID)
	row("name", e.DisplayName())
	row("description", e.Description)
	row("kind", e.Kind)
	if e.Kind == index.KindNative {
		row("package", e.Package)
		v := e.Version
		if v == "" {
			v = "latest"
		}
		row("version", v)
	} else {
		row("command", strings.Join(e.Command, " "))
		row("summary", strings.Join(e.Summary, " "))
	}
	row("requires", e.Requires)
	row("repo", e.Repo)
	row("tags", strings.Join(e.Tags, ", "))
	row("status", "not installed (dock plugin install "+e.ID+")")
	return tw.Flush()
}

// ---- update ----

// updateCmd checks for newer versions of dock and its unpinned native
// plugins and, unless --check, downloads dock's source and rebuilds.
func updateCmd(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("dock update", flag.ContinueOnError)
	check := fs.Bool("check", false, "only show what can be updated")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: dock update [--check]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "checking for updates…")
	st := update.Check(cfg)
	fmt.Fprint(w, st.Report())
	if *check {
		if st.Available() {
			fmt.Fprintln(w, "run `dock update` to install them")
		}
		return nil
	}
	if !st.Available() {
		if len(st.Errors) > 0 {
			return errors.New("could not check every update; nothing changed")
		}
		fmt.Fprintln(w, "everything is up to date")
		return nil
	}
	var swap *update.Swap
	if st.Dock != nil {
		fmt.Fprintf(w, "downloading dock %s\n", st.Dock.Latest)
		if swap, err = update.ReplaceSource(st.Dock.Latest); err != nil {
			return fmt.Errorf("downloading dock: %w", err)
		}
	}
	if err := rebuild(cfg, cfg.Native, config.BinPath()); err != nil {
		if swap != nil {
			if rerr := swap.Restore(); rerr != nil {
				return fmt.Errorf("%v (and restoring the old source failed: %v)", err, rerr)
			}
		}
		return err
	}
	if swap != nil {
		swap.Commit()
	}
	fmt.Fprintf(w, "updated %s; restart dock to use it\n", st.Summary())
	return nil
}
