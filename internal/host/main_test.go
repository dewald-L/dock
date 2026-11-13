package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps every host test away from the real cache, plugin index and
// update check: opening the Plugins screen loads the index in the
// background, which would otherwise fetch the real one and cache it in the
// real $XDG_CACHE_HOME.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dock-host-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	os.Setenv("DOCK_REGISTRY", filepath.Join(dir, "no-index.json"))
	os.Setenv("DOCK_UPDATE_SERVER", "http://127.0.0.1:1")
	os.Setenv("DOCK_NO_UPDATE_CHECK", "1")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
