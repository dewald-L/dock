// Package plugins lists the native plugins compiled into dock. To add one,
// import its package and append it here (see docs/PLUGINS.md).
package plugins

import (
	lazy1on1 "github.com/dewald-L/lazy1on1/dockplugin"

	"github.com/dewald-L/dock/plugin"
	"github.com/dewald-L/dock/plugins/scratch"
)

// Native returns every compiled-in plugin, in launcher order.
func Native() []plugin.Plugin {
	return []plugin.Plugin{
		lazy1on1.Plugin{},
		scratch.Plugin{},
	}
}
