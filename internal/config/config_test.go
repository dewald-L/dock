package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/x/cache")
	if d := CacheDir(); d != "/x/cache/dock" {
		t.Fatalf("with XDG_CACHE_HOME: %s", d)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	if d := CacheDir(); d != filepath.Join(base, "dock") {
		t.Fatalf("default: %s", d)
	}
}
