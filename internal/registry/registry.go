// Package registry merges native plugins and configured exec plugins into
// one list the host and the CLI both use.
package registry

import (
	"fmt"
	"strings"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
)

// Kind is where a plugin comes from.
type Kind int

const (
	System Kind = iota // built into the host, cannot be disabled
	Native             // Go package compiled into dock
	Exec               // external program from the config file
)

func (k Kind) String() string {
	return [...]string{"system", "native", "exec"}[k]
}

// Entry is one known plugin.
type Entry struct {
	Manifest plugin.Manifest
	Kind     Kind
	Enabled  bool
	Plugin   plugin.Plugin      // System and Native
	Exec     *config.ExecPlugin // Exec
}

// Registry is the ordered plugin list.
type Registry struct {
	Entries []*Entry
}

// Build validates and merges system, native and exec plugins. Invalid or
// duplicate plugins are skipped and reported in errs; later ones lose.
func Build(cfg *config.Config, system, native []plugin.Plugin) (r *Registry, errs []error) {
	r = &Registry{}
	seen := map[string]bool{}
	add := func(e *Entry) {
		if err := e.Manifest.Validate(); err != nil {
			errs = append(errs, err)
			return
		}
		if seen[e.Manifest.ID] {
			errs = append(errs, fmt.Errorf("plugin id %q is used twice; skipping the %s one", e.Manifest.ID, e.Kind))
			return
		}
		seen[e.Manifest.ID] = true
		r.Entries = append(r.Entries, e)
	}
	for _, p := range system {
		add(&Entry{Manifest: p.Manifest(), Kind: System, Enabled: true, Plugin: p})
	}
	for _, p := range native {
		m := p.Manifest()
		add(&Entry{Manifest: m, Kind: Native, Enabled: !cfg.IsDisabled(m.ID), Plugin: p})
	}
	for i := range cfg.Exec {
		x := &cfg.Exec[i]
		if len(x.Command) == 0 {
			errs = append(errs, fmt.Errorf("exec plugin %q has no command", x.ID))
			continue
		}
		add(&Entry{Manifest: ExecManifest(x), Kind: Exec, Enabled: !cfg.IsDisabled(x.ID), Exec: x})
	}
	return r, errs
}

// ExecManifest derives a manifest for an exec plugin.
func ExecManifest(x *config.ExecPlugin) plugin.Manifest {
	name := x.Name
	if name == "" {
		name = x.ID
	}
	desc := x.Description
	if desc == "" {
		desc = strings.Join(x.Command, " ")
	}
	return plugin.Manifest{
		ID: x.ID, Name: name, Description: desc,
		Display: plugin.DisplayTakeover, Lifecycle: plugin.OneShot,
	}
}

// Get returns the entry with id, or nil.
func (r *Registry) Get(id string) *Entry {
	for _, e := range r.Entries {
		if e.Manifest.ID == id {
			return e
		}
	}
	return nil
}

// Enabled returns the enabled entries in order.
func (r *Registry) Enabled() []*Entry {
	var out []*Entry
	for _, e := range r.Entries {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return out
}
