// Command dock is a terminal workspace that hosts plugins: native Go
// plugins that live in tabs and tiled panes, and external programs that
// take over the terminal while they run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/internal/host"
	"github.com/dewald-L/dock/internal/registry"
	"github.com/dewald-L/dock/plugin"
	"github.com/dewald-L/dock/plugins"
)

const version = "0.1.0"

const usage = `dock - a terminal workspace for plugins

Usage:
  dock [plugin-id...]          start, opening the given plugins (default: config "startup")
  dock plugin list             list plugins
  dock plugin info <id>        show one plugin
  dock plugin enable <id>      enable a plugin
  dock plugin disable <id>     disable a plugin
  dock plugin add [flags] <id> [--] <command> [args...]
                               register an external program as an exec plugin
      --name NAME              display name (default: id)
      --desc TEXT              one-line description
      --dir DIR                working directory (default: home)
      --pause                  wait for Enter after it exits, so its output stays readable
  dock plugin remove <id>      unregister an exec plugin
  dock config                  print the config file path
  dock version

Inside dock every host shortcut starts with the leader key (ctrl+g by
default); ctrl+g ? shows them all.
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "plugin", "plugins":
			os.Exit(pluginCmd(args[1:]))
		case "config":
			fmt.Println(config.Path())
			return
		case "version", "--version", "-v":
			fmt.Println("dock", version)
			return
		case "help", "--help", "-h":
			fmt.Print(usage)
			return
		}
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "dock:", err)
		os.Exit(1)
	}
}

func load() (*config.Config, *registry.Registry, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	reg, errs := registry.Build(cfg, host.SystemPlugins(nil), plugins.Native())
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
	app := host.New(host.Options{Config: cfg, Native: plugins.Native(), Open: ids, Version: version})
	_, err = tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

func pluginCmd(args []string) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	var err error
	switch args[0] {
	case "list", "ls":
		err = pluginList()
	case "info":
		err = withID(args, pluginInfo)
	case "enable":
		err = withID(args, func(id string) error { return setEnabled(id, true) })
	case "disable":
		err = withID(args, func(id string) error { return setEnabled(id, false) })
	case "add":
		err = pluginAdd(args[1:])
	case "remove", "rm":
		err = withID(args, pluginRemove)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", "plugin "+args[0], usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dock:", err)
		return 1
	}
	return 0
}

func withID(args []string, f func(string) error) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dock plugin %s <id>", args[0])
	}
	return f(args[1])
}

func pluginList() error {
	_, reg, err := load()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tDISPLAY\tLIFECYCLE\tSTATUS")
	for _, e := range reg.Entries {
		st := "enabled"
		if !e.Enabled {
			st = "disabled"
		}
		m := e.Manifest
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, e.Kind, m.Display, m.Lifecycle, st)
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
		ID: id, Name: *name, Description: *desc, Command: command, Dir: *dir, Pause: *pause,
	})
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("added %s: %s\n", id, strings.Join(command, " "))
	return nil
}

func pluginRemove(id string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.RemoveExec(id) {
		return fmt.Errorf("%q is not an exec plugin (native plugins can only be disabled)", id)
	}
	return cfg.Save()
}
