package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dewald-L/dock/internal/build"
	"github.com/dewald-L/dock/internal/config"
)

const (
	shaOld = "1111111111111111111111111111111111111111"
	shaNew = "2222222222222222222222222222222222222222"
)

// fakeServer answers like GitHub: main's commit id and tarballs.
type fakeServer struct {
	*httptest.Server
	sha  atomic.Value
	tars map[string][]byte
	hits atomic.Int32
	down atomic.Bool
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{tars: map[string][]byte{}}
	f.sha.Store(shaNew)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		switch {
		case r.URL.Path == "/repos/dewald-L/dock/commits/main":
			if r.Header.Get("Accept") != "application/vnd.github.sha" {
				http.Error(w, "accept", http.StatusBadRequest)
				return
			}
			w.Write([]byte(f.sha.Load().(string)))
		case strings.HasPrefix(r.URL.Path, "/dewald-L/dock/tar.gz/"):
			b, ok := f.tars[strings.TrimPrefix(r.URL.Path, "/dewald-L/dock/tar.gz/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	t.Setenv("DOCK_UPDATE_SERVER", f.URL)
	return f
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func dockTar(t *testing.T, marker string) []byte {
	return tarball(t, map[string]string{
		"dock-x/go.mod":  "module github.com/dewald-L/dock\n",
		"dock-x/main.go": "package main // " + marker + "\n",
	})
}

// sandbox points dock's dirs at temp dirs and installs a managed source
// tree at shaOld, with stub build info and module lookups.
func sandbox(t *testing.T) (deps *[]*debug.Module, latest map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("DOCK_UPDATE_SERVER", "http://127.0.0.1:1") // nothing listens
	src := SourceDir()
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "go.mod"), []byte("module github.com/dewald-L/dock\n"), 0o644)
	os.WriteFile(filepath.Join(src, VersionFile), []byte(shaOld+"\n"), 0o644)

	d := []*debug.Module{}
	latest = map[string]string{}
	oldResolve, oldRead, oldLatest := resolveDock, readBuildInfo, latestModule
	resolveDock = func(*config.Config) build.Dock { return build.Dock{Path: SourceDir()} }
	readBuildInfo = func(string) (*debug.BuildInfo, error) { return &debug.BuildInfo{Deps: d}, nil }
	latestModule = func(m string) (string, error) {
		if v, ok := latest[m]; ok {
			return v, nil
		}
		return "", errors.New("no such module")
	}
	t.Cleanup(func() { resolveDock, readBuildInfo, latestModule = oldResolve, oldRead, oldLatest })
	return &d, latest
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.10.0", "v1.9.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", false},
		{"v0.0.0-20261113073619-acac01d143f6", "v0.0.0-20261101000000-aaaaaaaaaaaa", true},
		{"v0.0.0-20261101000000-aaaaaaaaaaaa", "v0.0.0-20261113073619-acac01d143f6", false},
		{"v0.1.0", "v0.0.0-20261113073619-acac01d143f6", true},
		{"v2.0.0+incompatible", "v1.9.9", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestCheckDockAndPlugins(t *testing.T) {
	deps, latest := sandbox(t)
	f := newFake(t)
	*deps = []*debug.Module{
		{Path: "example.com/a", Version: "v1.0.0"},
		{Path: "example.com/pinned", Version: "v1.0.0"},
		{Path: "example.com/local", Version: "v0.0.0", Replace: &debug.Module{Path: "/src/local"}},
		{Path: "example.com/same", Version: "v2.0.0"},
	}
	latest["example.com/a"] = "v1.1.0"
	latest["example.com/pinned"] = "v9.0.0"
	latest["example.com/local"] = "v9.0.0"
	latest["example.com/same"] = "v2.0.0"
	cfg := &config.Config{Native: []config.NativePlugin{
		{Package: "example.com/a/dockplugin"},
		{Package: "example.com/pinned", Version: "v1.0.0"},
		{Package: "example.com/local", Path: "/src/local"},
		{Package: "example.com/same"},
		{Package: "github.com/dewald-L/dock/examples/scratch"},
		{Package: "example.com/missing"}, // not in the binary
	}}
	st := Check(cfg)
	if st.Dock == nil || st.Dock.Current != shaOld || st.Dock.Latest != shaNew {
		t.Fatalf("dock: %+v", st.Dock)
	}
	if len(st.Plugins) != 1 || st.Plugins[0].Name != "example.com/a" || st.Plugins[0].Latest != "v1.1.0" {
		t.Fatalf("plugins: %+v", st.Plugins)
	}
	if st.Summary() != "dock, a" || !st.Available() {
		t.Fatalf("summary %q", st.Summary())
	}
	rep := st.Report()
	if !strings.Contains(rep, "dock: 111111111111 → 222222222222") || !strings.Contains(rep, "example.com/a: v1.0.0 → v1.1.0") {
		t.Fatalf("report:\n%s", rep)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("hits = %d", f.hits.Load())
	}
}

func TestUpToDate(t *testing.T) {
	sandbox(t)
	f := newFake(t)
	f.sha.Store(shaOld)
	st := Check(&config.Config{})
	if st.Available() || st.Summary() != "" || !strings.Contains(st.Report(), "dock: up to date") {
		t.Fatalf("%+v", st)
	}
}

func TestUnknownCurrentIsAnUpdate(t *testing.T) {
	sandbox(t)
	newFake(t)
	os.Remove(filepath.Join(SourceDir(), VersionFile)) // installed before versions were recorded
	st := Check(&config.Config{})
	if st.Dock == nil || st.Dock.Current != "unknown" {
		t.Fatalf("%+v", st.Dock)
	}
}

func TestCheckoutIsNotUpdated(t *testing.T) {
	sandbox(t)
	f := newFake(t)
	resolveDock = func(*config.Config) build.Dock { return build.Dock{Path: "/home/me/Projects/dock"} }
	st := Check(&config.Config{})
	if st.Dock != nil || !strings.Contains(st.DockNote, "built from /home/me/Projects/dock") {
		t.Fatalf("%+v", st)
	}
	if f.hits.Load() != 0 {
		t.Fatal("asked GitHub about a checkout")
	}
}

func TestOfflineCheck(t *testing.T) {
	sandbox(t) // DOCK_UPDATE_SERVER points at a closed port
	Timeout = time.Second
	t.Cleanup(func() { Timeout = 5 * time.Second })
	st := Check(&config.Config{})
	if st.Available() || len(st.Errors) != 1 || !strings.HasPrefix(st.Errors[0], "dock: ") {
		t.Fatalf("%+v", st)
	}
}

func TestCachedReusesLatestButReadsCurrentFresh(t *testing.T) {
	sandbox(t)
	f := newFake(t)
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	now = func() time.Time { return t0 }
	t.Cleanup(func() { now = time.Now })

	if s := Cached(&config.Config{}).Summary(); s != "dock" || f.hits.Load() != 1 {
		t.Fatalf("first: %q hits=%d", s, f.hits.Load())
	}
	now = func() time.Time { return t0.Add(time.Hour) }
	if s := Cached(&config.Config{}).Summary(); s != "dock" || f.hits.Load() != 1 {
		t.Fatalf("cached: %q hits=%d", s, f.hits.Load())
	}
	// updated meanwhile (e.g. by install.sh): the cache must not nag
	os.WriteFile(filepath.Join(SourceDir(), VersionFile), []byte(shaNew+"\n"), 0o644)
	if s := Cached(&config.Config{}).Summary(); s != "" || f.hits.Load() != 1 {
		t.Fatalf("after update: %q hits=%d", s, f.hits.Load())
	}
	// a day later it asks again
	now = func() time.Time { return t0.Add(MaxAge + time.Minute) }
	Cached(&config.Config{})
	if f.hits.Load() != 2 {
		t.Fatalf("stale cache not refreshed: hits=%d", f.hits.Load())
	}
	if !strings.HasPrefix(CachePath(), os.Getenv("XDG_CACHE_HOME")) {
		t.Fatal("cache outside XDG_CACHE_HOME")
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("DOCK_NO_UPDATE_CHECK", "")
	off := false
	if Disabled(&config.Config{}) || !Disabled(&config.Config{UpdateCheck: &off}) {
		t.Fatal("config")
	}
	t.Setenv("DOCK_NO_UPDATE_CHECK", "1")
	if !Disabled(&config.Config{}) {
		t.Fatal("env")
	}
}

func TestReplaceSourceAndRestore(t *testing.T) {
	sandbox(t)
	f := newFake(t)
	f.tars[shaNew] = dockTar(t, "new")
	src := SourceDir()
	os.WriteFile(filepath.Join(src, "main.go"), []byte("package main // old\n"), 0o644)

	sw, err := ReplaceSource(shaNew)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(src, "main.go"))
	v, _ := os.ReadFile(filepath.Join(src, VersionFile))
	if !strings.Contains(string(b), "new") || strings.TrimSpace(string(v)) != shaNew {
		t.Fatalf("main.go %q version %q", b, v)
	}
	if err := sw.Restore(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(src, "main.go"))
	v, _ = os.ReadFile(filepath.Join(src, VersionFile))
	if !strings.Contains(string(b), "old") || strings.TrimSpace(string(v)) != shaOld {
		t.Fatalf("not restored: %q %q", b, v)
	}

	sw, err = ReplaceSource(shaNew)
	if err != nil {
		t.Fatal(err)
	}
	sw.Commit()
	if _, err := os.Stat(src + ".old"); !os.IsNotExist(err) {
		t.Fatal("old tree left behind")
	}
}

func TestReplaceSourceRejects(t *testing.T) {
	sandbox(t)
	f := newFake(t)
	f.tars[shaNew] = tarball(t, map[string]string{"x/go.mod": "module example.com/other\n"})
	f.tars["3333333333333333333333333333333333333333"] = tarball(t, map[string]string{"x/../../evil": "x"})
	for sha, want := range map[string]string{
		shaNew: "not dock",
		"3333333333333333333333333333333333333333": "unsafe path",
		"4444444444444444444444444444444444444444": "404",
		"nope": "not a commit id",
	} {
		_, err := ReplaceSource(sha)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", sha, err, want)
		}
		// the installed source is untouched
		v, _ := os.ReadFile(filepath.Join(SourceDir(), VersionFile))
		if strings.TrimSpace(string(v)) != shaOld {
			t.Fatalf("%s: source changed", sha)
		}
	}
	if _, err := os.Stat(SourceDir() + ".new"); !os.IsNotExist(err) {
		t.Fatal("partial download left behind")
	}
}
