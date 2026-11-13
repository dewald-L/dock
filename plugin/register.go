package plugin

import (
	"reflect"
	"sync"
)

var (
	regMu      sync.Mutex
	registered []Plugin
)

// Register makes a native plugin available to dock. Call it from your
// plugin package's init function:
//
//	func init() { plugin.Register(Plugin{}) }
//
// dock compiles a plugin in by importing its package for side effects;
// `dock plugin install <package>` generates that import and rebuilds dock.
func Register(p Plugin) {
	regMu.Lock()
	defer regMu.Unlock()
	registered = append(registered, p)
}

// Registered returns the registered plugins in registration order.
func Registered() []Plugin {
	regMu.Lock()
	defer regMu.Unlock()
	return append([]Plugin(nil), registered...)
}

// PackageOf returns the Go import path of the package that defines p.
func PackageOf(p Plugin) string {
	t := reflect.TypeOf(p)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	return t.PkgPath()
}
