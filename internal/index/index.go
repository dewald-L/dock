// Package index reads the plugin index: a JSON file (plugins.json in the
// dock-plugins repo by default) listing plugins dock can install by id.
//
// Fetch never blocks for long: remote indexes are fetched with a short
// timeout and cached for an hour, and when the network is down the cache
// is used whatever its age.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
)

// DefaultURL is the index dock uses unless the config or DOCK_REGISTRY
// says otherwise.
const DefaultURL = "https://raw.githubusercontent.com/dewald-L/dock-plugins/main/plugins.json"

// FormatVersion is the index format this dock understands.
const FormatVersion = 1

// MaxSize caps how much of an index dock reads.
const MaxSize = 1 << 20

// TTL is how long a fetched index is reused without asking the network.
const TTL = time.Hour

// Timeout bounds one fetch. A variable so tests can shorten it.
var Timeout = 5 * time.Second

// Kinds of entries.
const (
	KindNative = "native"
	KindExec   = "exec"
)

// Index is a parsed plugins.json.
type Index struct {
	Version int     `json:"version"`
	Plugins []Entry `json:"plugins"`
}

// Entry is one plugin in the index. Unknown fields are ignored.
type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Package     string   `json:"package,omitempty"` // native: Go import path
	Version     string   `json:"version,omitempty"` // native: tag or commit; empty means latest
	Command     []string `json:"command,omitempty"` // exec: argv
	Summary     []string `json:"summary,omitempty"` // exec: home screen summary argv
	Requires    string   `json:"requires,omitempty"`
	Repo        string   `json:"repo,omitempty"`
	Default     bool     `json:"default,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ErrNewerFormat means the index uses a format this dock predates.
var ErrNewerFormat = errors.New("the plugin index uses a newer format than this dock understands: update dock")

// Parse decodes and checks an index. It rejects documents over MaxSize,
// bad JSON, unknown format versions and entries dock could not install.
func Parse(b []byte) (*Index, error) {
	if len(b) > MaxSize {
		return nil, fmt.Errorf("plugin index is larger than %d bytes", MaxSize)
	}
	// read the version alone first, so a future format with a different
	// shape still produces the "update dock" error, not a decode error
	var head struct {
		Version json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("plugin index: %w", err)
	}
	var v int
	if len(head.Version) == 0 || json.Unmarshal(head.Version, &v) != nil || v < 1 {
		return nil, errors.New("plugin index: missing or invalid \"version\"")
	}
	if v > FormatVersion {
		return nil, fmt.Errorf("%w (index format %d, this dock reads %d)", ErrNewerFormat, v, FormatVersion)
	}
	var ix Index
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, fmt.Errorf("plugin index: %w", err)
	}
	seen := map[string]bool{}
	for i, e := range ix.Plugins {
		if err := e.check(); err != nil {
			return nil, fmt.Errorf("plugin index: entry %d: %w", i+1, err)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("plugin index: id %q appears twice", e.ID)
		}
		seen[e.ID] = true
	}
	return &ix, nil
}

func (e Entry) check() error {
	if !plugin.ValidID(e.ID) {
		return fmt.Errorf("invalid id %q", e.ID)
	}
	switch e.Kind {
	case KindNative:
		if e.Package == "" {
			return fmt.Errorf("%s: native entry has no package", e.ID)
		}
	case KindExec:
		if len(e.Command) == 0 {
			return fmt.Errorf("%s: exec entry has no command", e.ID)
		}
	default:
		return fmt.Errorf("%s: unknown kind %q", e.ID, e.Kind)
	}
	return nil
}

// Get returns the entry with id, or nil.
func (ix *Index) Get(id string) *Entry {
	for i := range ix.Plugins {
		if ix.Plugins[i].ID == id {
			return &ix.Plugins[i]
		}
	}
	return nil
}

// Search returns the entries whose id, name, description or tags contain
// term (case-insensitive). An empty term matches everything.
func (ix *Index) Search(term string) []Entry {
	term = strings.ToLower(strings.TrimSpace(term))
	var out []Entry
	for _, e := range ix.Plugins {
		hay := strings.ToLower(strings.Join(append([]string{e.ID, e.Name, e.Description}, e.Tags...), "\n"))
		if term == "" || strings.Contains(hay, term) {
			out = append(out, e)
		}
	}
	return out
}

// Defaults returns the entries marked default.
func (ix *Index) Defaults() []Entry {
	var out []Entry
	for _, e := range ix.Plugins {
		if e.Default {
			out = append(out, e)
		}
	}
	return out
}

// DisplayName is the entry's name, or its id.
func (e Entry) DisplayName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}

// Installed reports whether e is already installed according to cfg and
// the ids of the plugins dock knows about (its registry).
func (e Entry) Installed(cfg *config.Config, known []string) bool {
	if slices.Contains(known, e.ID) {
		return true
	}
	if e.Kind == KindNative {
		return cfg.FindNative(e.Package) != nil
	}
	return cfg.FindExec(e.ID) != nil
}

// ---- source ----

// Source returns where the index comes from: DOCK_REGISTRY, else the
// config's "registry", else DefaultURL.
func Source(cfg *config.Config) string {
	if s := os.Getenv("DOCK_REGISTRY"); s != "" {
		return s
	}
	if cfg != nil && cfg.Registry != "" {
		return cfg.Registry
	}
	return DefaultURL
}

// localPath returns the file a source names, or "" for a URL.
func localPath(src string) (string, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return "", nil
	}
	if strings.HasPrefix(src, "file:") {
		u, err := url.Parse(src)
		if err != nil {
			return "", fmt.Errorf("registry %q: %w", src, err)
		}
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("registry %q: file URLs must be local", src)
		}
		return u.Path, nil
	}
	if strings.Contains(src, "://") {
		return "", fmt.Errorf("registry %q: use https://, file:// or a path", src)
	}
	if rest, ok := strings.CutPrefix(src, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	return src, nil
}

// ---- fetch and cache ----

// Result is a loaded index and where it came from.
type Result struct {
	*Index
	Source string
	Cached bool          // served from the cache rather than fetched now
	Age    time.Duration // age of the cached copy
	Stale  bool          // the fetch failed and an old cache was used
	Err    error         // with Stale: why the fetch failed
}

// Note is a short description of a cached result, e.g. "(cached, 5m)",
// or "" for a fresh one.
func (r *Result) Note() string {
	if !r.Cached {
		return ""
	}
	return "(cached, " + FormatAge(r.Age) + ")"
}

// FormatAge renders an age as 30s, 5m, 3h or 2d.
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// CachePath is $XDG_CACHE_HOME/dock/index.json.
func CachePath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "dock", "index.json")
}

// cacheFile is what CachePath holds: the raw index and where and when it
// was fetched.
type cacheFile struct {
	Source  string          `json:"source"`
	Fetched time.Time       `json:"fetched"`
	Index   json.RawMessage `json:"index"`
}

// now is replaced in tests.
var now = time.Now

// Load reads the index from src. Local files are read directly. For URLs
// a cached copy younger than TTL is used unless refresh is set; otherwise
// the index is fetched and cached, and if that fails an older cached copy
// is returned (Stale). With no network and no cache it returns an error.
func Load(src string, refresh bool) (*Result, error) {
	path, err := localPath(src)
	if err != nil {
		return nil, err
	}
	if path != "" {
		b, err := readCapped(path, MaxSize)
		if err != nil {
			return nil, fmt.Errorf("plugin index: %w", err)
		}
		ix, err := Parse(b)
		if err != nil {
			return nil, err
		}
		return &Result{Index: ix, Source: src}, nil
	}

	cached, cerr := readCache(src)
	if cached != nil && !refresh && cached.Age < TTL {
		return cached, nil
	}
	ix, raw, ferr := fetch(src)
	if ferr == nil {
		writeCache(src, raw) // best effort
		return &Result{Index: ix, Source: src}, nil
	}
	if cached != nil && !errors.Is(ferr, ErrNewerFormat) {
		cached.Stale, cached.Err = true, ferr
		return cached, nil
	}
	if cerr != nil && !errors.Is(cerr, os.ErrNotExist) {
		return nil, fmt.Errorf("%w (and the cache is unusable: %v)", ferr, cerr)
	}
	return nil, ferr
}

func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readAll(f, limit)
}

func readAll(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("plugin index is larger than %d bytes", MaxSize)
	}
	return b, nil
}

func fetch(src string) (*Index, []byte, error) {
	client := &http.Client{Timeout: Timeout}
	req, err := http.NewRequest(http.MethodGet, src, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "dock")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching the plugin index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetching the plugin index: %s: %s", src, resp.Status)
	}
	b, err := readAll(resp.Body, MaxSize)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching the plugin index: %w", err)
	}
	ix, err := Parse(b)
	if err != nil {
		return nil, nil, err
	}
	return ix, b, nil
}

// readCache returns the cached index for src, or nil. A cache for another
// source is ignored.
func readCache(src string) (*Result, error) {
	b, err := readCapped(CachePath(), 2*MaxSize) // the index plus a little metadata
	if err != nil {
		return nil, err
	}
	var c cacheFile
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Source != src {
		return nil, os.ErrNotExist
	}
	ix, err := Parse(c.Index)
	if err != nil {
		return nil, err
	}
	return &Result{Index: ix, Source: src, Cached: true, Age: max(now().Sub(c.Fetched), 0)}, nil
}

func writeCache(src string, raw []byte) error {
	b, err := json.Marshal(cacheFile{Source: src, Fetched: now().UTC(), Index: raw})
	if err != nil {
		return err
	}
	path := CachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
