package index

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dewald-L/dock/internal/config"
)

const good = `{
  "version": 1,
  "plugins": [
    {"id": "lazy1on1", "name": "lazy1on1", "description": "1:1 notes", "kind": "native",
     "package": "example.com/lazy1on1/dockplugin", "default": true, "tags": ["notes"]},
    {"id": "osheet", "name": "OSheet", "description": "Log timesheet entries", "kind": "exec",
     "command": ["osheet"], "summary": ["osheet", "--today"], "requires": "needs osheet on PATH"}
  ]
}`

// sandbox points every dock path at temp dirs, so no test touches the
// real config, cache or index.
func sandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCK_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("DOCK_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("DOCK_REGISTRY", filepath.Join(dir, "no-such-index.json"))
	return dir
}

func TestParseGood(t *testing.T) {
	ix, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Plugins) != 2 || ix.Get("lazy1on1").Package != "example.com/lazy1on1/dockplugin" {
		t.Fatalf("got %+v", ix)
	}
	x := ix.Get("osheet")
	if x.Kind != KindExec || x.Command[0] != "osheet" || x.Requires == "" || len(x.Summary) != 2 {
		t.Fatalf("exec entry: %+v", x)
	}
	if d := ix.Defaults(); len(d) != 1 || d[0].ID != "lazy1on1" {
		t.Fatalf("defaults: %+v", d)
	}
	if ix.Get("nope") != nil {
		t.Fatal("Get of a missing id")
	}
}

func TestParseIgnoresUnknownFields(t *testing.T) {
	src := `{"version": 1, "future": {"x": 1}, "plugins": [
	  {"id": "a", "kind": "native", "package": "example.com/a", "stars": 5, "screenshots": ["x.png"]}]}`
	ix, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if ix.Get("a") == nil {
		t.Fatal("entry lost")
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		src  string
		want string
	}{
		"bad json":         {`{"version": 1, "plugins": [`, "plugin index"},
		"no version":       {`{"plugins": []}`, `missing or invalid "version"`},
		"string version":   {`{"version": "1", "plugins": []}`, `missing or invalid "version"`},
		"newer version":    {`{"version": 2, "plugins": [{"totally": "different"}]}`, "update dock"},
		"newer, new shape": {`{"version": 2, "plugins": {"a": 1}}`, "update dock"},
		"bad id":           {`{"version": 1, "plugins": [{"id": "Bad ID", "kind": "native", "package": "x"}]}`, "invalid id"},
		"duplicate id":     {`{"version": 1, "plugins": [{"id": "a", "kind": "exec", "command": ["a"]}, {"id": "a", "kind": "exec", "command": ["b"]}]}`, "twice"},
		"native no pkg":    {`{"version": 1, "plugins": [{"id": "a", "kind": "native"}]}`, "no package"},
		"exec no command":  {`{"version": 1, "plugins": [{"id": "a", "kind": "exec"}]}`, "no command"},
		"unknown kind":     {`{"version": 1, "plugins": [{"id": "a", "kind": "wasm"}]}`, "unknown kind"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	_, err := Parse([]byte(`{"version": 2, "plugins": []}`))
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("newer format is not ErrNewerFormat: %v", err)
	}
}

func TestParseOversized(t *testing.T) {
	big := `{"version": 1, "plugins": [], "pad": "` + strings.Repeat("x", MaxSize) + `"}`
	if _, err := Parse([]byte(big)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearch(t *testing.T) {
	ix, _ := Parse([]byte(good))
	ids := func(es []Entry) string {
		var s []string
		for _, e := range es {
			s = append(s, e.ID)
		}
		return strings.Join(s, ",")
	}
	for term, want := range map[string]string{
		"": "lazy1on1,osheet", "NOTES": "lazy1on1", "timesheet": "osheet", "zzz": "",
	} {
		if got := ids(ix.Search(term)); got != want {
			t.Errorf("Search(%q) = %q, want %q", term, got, want)
		}
	}
}

func TestInstalled(t *testing.T) {
	ix, _ := Parse([]byte(good))
	cfg := &config.Config{}
	n, x := *ix.Get("lazy1on1"), *ix.Get("osheet")
	if n.Installed(cfg, nil) || x.Installed(cfg, nil) {
		t.Fatal("nothing is installed yet")
	}
	cfg.Native = []config.NativePlugin{{Package: n.Package}}
	cfg.Exec = []config.ExecPlugin{{ID: "osheet", Command: []string{"osheet"}}}
	if !n.Installed(cfg, nil) || !x.Installed(cfg, nil) {
		t.Fatal("config entries count as installed")
	}
	if !n.Installed(&config.Config{}, []string{"lazy1on1"}) {
		t.Fatal("a registered id counts as installed")
	}
}

func TestSource(t *testing.T) {
	sandbox(t)
	t.Setenv("DOCK_REGISTRY", "")
	if s := Source(&config.Config{}); s != DefaultURL {
		t.Fatalf("default = %s", s)
	}
	if s := Source(&config.Config{Registry: "/tmp/x.json"}); s != "/tmp/x.json" {
		t.Fatalf("config = %s", s)
	}
	t.Setenv("DOCK_REGISTRY", "file:///env.json")
	if s := Source(&config.Config{Registry: "/tmp/x.json"}); s != "file:///env.json" {
		t.Fatalf("env = %s", s)
	}
}

func TestLoadLocal(t *testing.T) {
	dir := sandbox(t)
	path := filepath.Join(dir, "plugins.json")
	os.WriteFile(path, []byte(good), 0o644)
	for _, src := range []string{path, "file://" + path} {
		r, err := Load(src, false)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if r.Cached || r.Get("osheet") == nil {
			t.Fatalf("%s: %+v", src, r)
		}
	}
	if _, err := os.Stat(CachePath()); !os.IsNotExist(err) {
		t.Fatal("local indexes are not cached")
	}
	if _, err := Load(filepath.Join(dir, "missing.json"), false); err == nil {
		t.Fatal("missing file")
	}
	if _, err := Load("ftp://example.com/x", false); err == nil {
		t.Fatal("unsupported scheme")
	}
}

// server serves body (switchable) and counts requests.
type server struct {
	*httptest.Server
	hits atomic.Int32
	body atomic.Value
	down atomic.Bool
}

func newServer(t *testing.T, body string) *server {
	s := &server{}
	s.body.Store(body)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		if s.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(s.body.Load().(string)))
	}))
	t.Cleanup(s.Close)
	return s
}

func setNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

func TestCacheFresh(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	setNow(t, t0)

	r, err := Load(srv.URL, false)
	if err != nil || r.Cached || srv.hits.Load() != 1 {
		t.Fatalf("first load: %+v %v hits=%d", r, err, srv.hits.Load())
	}
	if _, err := os.Stat(CachePath()); err != nil {
		t.Fatal("not cached:", err)
	}

	setNow(t, t0.Add(10*time.Minute))
	r, err = Load(srv.URL, false)
	if err != nil || !r.Cached || r.Stale || srv.hits.Load() != 1 {
		t.Fatalf("fresh cache not reused: %+v %v hits=%d", r, err, srv.hits.Load())
	}
	if r.Note() != "(cached, 10m)" {
		t.Fatalf("note = %q", r.Note())
	}

	// refresh bypasses the cache
	r, err = Load(srv.URL, true)
	if err != nil || r.Cached || srv.hits.Load() != 2 {
		t.Fatalf("refresh: %+v %v hits=%d", r, err, srv.hits.Load())
	}
}

func TestCacheStale(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	setNow(t, t0)
	if _, err := Load(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	srv.body.Store(`{"version": 1, "plugins": [{"id": "new", "kind": "exec", "command": ["new"]}]}`)

	setNow(t, t0.Add(TTL+time.Minute))
	r, err := Load(srv.URL, false)
	if err != nil || r.Cached || srv.hits.Load() != 2 || r.Get("new") == nil {
		t.Fatalf("stale cache not refetched: %+v %v hits=%d", r, err, srv.hits.Load())
	}
}

func TestCacheOfflineFallback(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	setNow(t, t0)
	if _, err := Load(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	url := srv.URL
	srv.Close() // offline
	Timeout = time.Second
	t.Cleanup(func() { Timeout = 5 * time.Second })

	setNow(t, t0.Add(3*time.Hour))
	for _, refresh := range []bool{false, true} {
		r, err := Load(url, refresh)
		if err != nil {
			t.Fatalf("refresh=%v: %v", refresh, err)
		}
		if !r.Cached || !r.Stale || r.Err == nil || r.Get("lazy1on1") == nil {
			t.Fatalf("refresh=%v: %+v", refresh, r)
		}
		if r.Note() != "(cached, 3h)" {
			t.Fatalf("note = %q", r.Note())
		}
	}
}

func TestCacheServerErrorFallback(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	if _, err := Load(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	srv.down.Store(true)
	r, err := Load(srv.URL, true)
	if err != nil || !r.Stale || !strings.Contains(r.Err.Error(), "503") {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestNoCacheOffline(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	url := srv.URL
	srv.Close()
	_, err := Load(url, false)
	if err == nil || !strings.Contains(err.Error(), "fetching the plugin index") {
		t.Fatalf("err = %v", err)
	}
}

func TestCacheIgnoresOtherSource(t *testing.T) {
	sandbox(t)
	a := newServer(t, good)
	if _, err := Load(a.URL, false); err != nil {
		t.Fatal(err)
	}
	b := newServer(t, `{"version": 1, "plugins": []}`)
	r, err := Load(b.URL, false)
	if err != nil || r.Cached || len(r.Plugins) != 0 || b.hits.Load() != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestNewerFormatIsNotMaskedByCache(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	if _, err := Load(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	srv.body.Store(`{"version": 2, "plugins": []}`)
	_, err := Load(srv.URL, true)
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("err = %v", err)
	}
}

func TestOversizedResponse(t *testing.T) {
	sandbox(t)
	srv := newServer(t, `{"version": 1, "plugins": [], "pad": "`+strings.Repeat("x", MaxSize+10)+`"}`)
	_, err := Load(srv.URL, false)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	sandbox(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	Timeout = 200 * time.Millisecond
	t.Cleanup(func() { Timeout = 5 * time.Second })
	start := time.Now()
	if _, err := Load(srv.URL, false); err == nil {
		t.Fatal("hung server must fail")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCacheFileShape(t *testing.T) {
	sandbox(t)
	srv := newServer(t, good)
	if _, err := Load(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(CachePath())
	var c cacheFile
	if err := json.Unmarshal(b, &c); err != nil || c.Source != srv.URL || c.Fetched.IsZero() {
		t.Fatalf("%s %v", b, err)
	}
	if !strings.HasPrefix(CachePath(), os.Getenv("XDG_CACHE_HOME")) {
		t.Fatal("cache outside XDG_CACHE_HOME")
	}
}
