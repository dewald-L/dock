// Package config loads and saves dock's config file and resolves the
// directories plugins use.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// DefaultLeader is the key that prefixes every host shortcut.
const DefaultLeader = "ctrl+g"

// Config is dock's config file ($XDG_CONFIG_HOME/dock/config.json).
type Config struct {
	// Leader prefixes host shortcuts. Default ctrl+g.
	Leader string `json:"leader,omitempty"`
	// Startup lists plugin IDs to open, one tab each, when dock starts
	// without arguments.
	Startup []string `json:"startup,omitempty"`
	// Disabled lists plugin IDs hidden from the home screen and palette.
	Disabled []string `json:"disabled,omitempty"`
	// Exec lists external programs registered as plugins.
	Exec []ExecPlugin `json:"exec,omitempty"`
	// Native lists Go plugin packages compiled into dock by `dock build`.
	Native []NativePlugin `json:"native,omitempty"`
	// Source is a local dock checkout to build against. Empty means the
	// released version this binary was built from (or the latest).
	Source string `json:"source,omitempty"`
}

// NativePlugin is a Go package that registers one or more plugins.
type NativePlugin struct {
	Package string `json:"package"`
	// Version to build (e.g. v1.2.0, a commit, "latest"). Ignored with Path.
	Version string `json:"version,omitempty"`
	// Path is a local checkout of the package's module, used instead of
	// downloading it.
	Path string `json:"path,omitempty"`
}

// FindNative returns the native plugin entry for pkg, or nil.
func (c *Config) FindNative(pkg string) *NativePlugin {
	for i := range c.Native {
		if c.Native[i].Package == pkg {
			return &c.Native[i]
		}
	}
	return nil
}

// RemoveNative deletes the native plugin entry for pkg.
func (c *Config) RemoveNative(pkg string) bool {
	n := len(c.Native)
	c.Native = slices.DeleteFunc(c.Native, func(p NativePlugin) bool { return p.Package == pkg })
	return len(c.Native) != n
}

// ExecPlugin is an external program run with the terminal handed over.
type ExecPlugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Command     []string `json:"command"`
	// Dir is the working directory. Default: the user's home.
	Dir string `json:"dir,omitempty"`
	// Pause waits for Enter after the program exits so its final output
	// can be read before dock redraws.
	Pause bool `json:"pause,omitempty"`
	// Summary is an optional command whose stdout (first 3 lines) is shown
	// in the plugin's block on the home screen.
	Summary []string `json:"summary,omitempty"`
}

// LeaderKey returns the configured leader or the default.
func (c *Config) LeaderKey() string {
	if c.Leader == "" {
		return DefaultLeader
	}
	return c.Leader
}

// IsDisabled reports whether id is disabled.
func (c *Config) IsDisabled(id string) bool { return slices.Contains(c.Disabled, id) }

// SetEnabled enables or disables id.
func (c *Config) SetEnabled(id string, on bool) {
	c.Disabled = slices.DeleteFunc(c.Disabled, func(s string) bool { return s == id })
	if !on {
		c.Disabled = append(c.Disabled, id)
		slices.Sort(c.Disabled)
	}
}

// FindExec returns the exec plugin with id, or nil.
func (c *Config) FindExec(id string) *ExecPlugin {
	for i := range c.Exec {
		if c.Exec[i].ID == id {
			return &c.Exec[i]
		}
	}
	return nil
}

// RemoveExec deletes the exec plugin with id. It reports whether it existed.
func (c *Config) RemoveExec(id string) bool {
	n := len(c.Exec)
	c.Exec = slices.DeleteFunc(c.Exec, func(e ExecPlugin) bool { return e.ID == id })
	c.SetEnabled(id, true)
	c.Startup = slices.DeleteFunc(c.Startup, func(s string) bool { return s == id })
	return len(c.Exec) != n
}

// ---- paths ----

func xdg(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, fallback)
}

// Dir is dock's config directory. DOCK_CONFIG_DIR overrides it (for tests).
func Dir() string {
	if d := os.Getenv("DOCK_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "dock")
}

// DataRoot is dock's data directory. DOCK_DATA_DIR overrides it.
func DataRoot() string {
	if d := os.Getenv("DOCK_DATA_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "dock")
}

// BinPath is where `dock build` installs the dock binary it compiles.
func BinPath() string { return filepath.Join(DataRoot(), "bin", "dock") }

// Path is the config file path.
func Path() string { return filepath.Join(Dir(), "config.json") }

// PluginDataDir is where plugin id keeps its data.
func PluginDataDir(id string) string { return filepath.Join(DataRoot(), id) }

// PluginConfigDir is where plugin id keeps its settings.
func PluginConfigDir(id string) string { return filepath.Join(Dir(), "plugins", id) }

// EnsurePluginDirs creates both plugin directories and returns them.
func EnsurePluginDirs(id string) (data, conf string, err error) {
	data, conf = PluginDataDir(id), PluginConfigDir(id)
	for _, d := range []string{data, conf} {
		if err = os.MkdirAll(d, 0o755); err != nil {
			return "", "", err
		}
	}
	return data, conf, nil
}

// ---- load / save ----

// Load reads the config file. A missing file is an empty config.
func Load() (*Config, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(), err)
	}
	return &c, nil
}

// Save writes the config file atomically.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(Dir(), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), Path())
}
