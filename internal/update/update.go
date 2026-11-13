// Package update checks for and applies updates to dock and its native
// plugins.
//
// dock itself can only be updated when install.sh installed it: its source
// then lives in $DATA/src/dock with the commit in .dock-version, and an
// update downloads the latest main commit's tarball there. A dock built from
// a checkout (or the config's "source") is the user's to update with git.
//
// Native plugins are updated by rebuilding: unpinned ones are fetched at
// @latest. Plugins with a pinned version or a local path are left alone.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dewald-L/dock/internal/build"
	"github.com/dewald-L/dock/internal/config"
)

// VersionFile, inside the source dir, holds the commit install.sh or the
// last update downloaded.
const VersionFile = ".dock-version"

// MaxAge is how long a check's result is reused (dock checks at most once
// a day on its own).
const MaxAge = 24 * time.Hour

// Timeout bounds the GitHub request; GoTimeout bounds one `go list`.
var (
	Timeout   = 5 * time.Second
	GoTimeout = 20 * time.Second
)

// Hooks replaced in tests.
var (
	now            = time.Now
	resolveDock    = build.ResolveDock
	readBuildInfo  = buildinfo.ReadFile
	latestModule   = goLatest
	maxTarballSize = int64(64 << 20)
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func bases() (api, codeload string) {
	if s := strings.TrimRight(os.Getenv("DOCK_UPDATE_SERVER"), "/"); s != "" {
		return s, s
	}
	return "https://api.github.com", "https://codeload.github.com"
}

// CommitURL returns the URL that answers main's commit id.
func CommitURL() string {
	api, _ := bases()
	return api + "/repos/dewald-L/dock/commits/main"
}

// TarballURL returns the source tarball URL for commit sha.
func TarballURL(sha string) string {
	_, cl := bases()
	return cl + "/dewald-L/dock/tar.gz/" + sha
}

// SourceDir is where install.sh keeps dock's source.
func SourceDir() string { return filepath.Join(config.DataRoot(), "src", "dock") }

// Disabled reports whether background checks are off: config
// "update_check": false or DOCK_NO_UPDATE_CHECK set.
func Disabled(cfg *config.Config) bool {
	return os.Getenv("DOCK_NO_UPDATE_CHECK") != "" || (cfg.UpdateCheck != nil && !*cfg.UpdateCheck)
}

// ---- status ----

// Item is one thing with a newer version.
type Item struct {
	Name    string // "dock", or a plugin's module path
	Current string
	Latest  string
}

// Status is the result of a check.
type Status struct {
	Dock     *Item
	DockNote string // why dock itself isn't checked, if it isn't
	Plugins  []Item
	Errors   []string // checks that failed
	Checked  time.Time
}

// Available reports whether anything can be updated.
func (s *Status) Available() bool { return s.Dock != nil || len(s.Plugins) > 0 }

// Summary names what can be updated, e.g. "dock, lazy1on1", or "".
func (s *Status) Summary() string {
	var names []string
	if s.Dock != nil {
		names = append(names, "dock")
	}
	for _, p := range s.Plugins {
		names = append(names, path.Base(p.Name))
	}
	return strings.Join(names, ", ")
}

// Report is a human-readable description of s.
func (s *Status) Report() string {
	var b strings.Builder
	if s.Dock != nil {
		fmt.Fprintf(&b, "dock: %s → %s\n", short(s.Dock.Current), short(s.Dock.Latest))
	} else if s.DockNote != "" {
		fmt.Fprintf(&b, "dock: %s\n", s.DockNote)
	} else {
		fmt.Fprintln(&b, "dock: up to date")
	}
	for _, p := range s.Plugins {
		fmt.Fprintf(&b, "%s: %s → %s\n", p.Name, p.Current, p.Latest)
	}
	for _, e := range s.Errors {
		fmt.Fprintf(&b, "could not check %s\n", e)
	}
	return b.String()
}

func short(v string) string {
	if shaRe.MatchString(v) {
		return v[:12]
	}
	return v
}

// ---- latest versions and the cache ----

// latest is what the network said; it is cached.
type latest struct {
	Checked time.Time         `json:"checked"`
	Dock    string            `json:"dock,omitempty"`    // main's commit
	Plugins map[string]string `json:"plugins,omitempty"` // module path → latest version
	Errors  []string          `json:"errors,omitempty"`
}

// CachePath is $XDG_CACHE_HOME/dock/update.json.
func CachePath() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "dock", "update.json")
}

// Check asks the network for the latest versions (and caches them).
func Check(cfg *config.Config) *Status {
	l := fetchLatest(cfg)
	saveCache(l)
	return evaluate(cfg, l)
}

// Cached is Check, but reuses a result younger than MaxAge. Only the latest
// versions are cached: what is installed is always read fresh, so the
// result is right right after an update.
func Cached(cfg *config.Config) *Status {
	if l := loadCache(); l != nil && now().Sub(l.Checked) < MaxAge && now().Sub(l.Checked) >= 0 {
		return evaluate(cfg, l)
	}
	return Check(cfg)
}

func loadCache() *latest {
	b, err := os.ReadFile(CachePath())
	if err != nil {
		return nil
	}
	var l latest
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	return &l
}

func saveCache(l *latest) {
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	p := CachePath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p)
	}
}

// managedDock returns whether dock's source is install.sh's, and the commit
// it holds ("" when unknown). note explains an unmanaged dock.
func managedDock(cfg *config.Config) (managed bool, current, note string) {
	d := resolveDock(cfg)
	src := SourceDir()
	if d.Path == "" {
		return false, "", "not installed with install.sh; reinstall it to get updates"
	}
	if filepath.Clean(d.Path) != filepath.Clean(src) {
		return false, "", "built from " + d.Path + "; update it there (git pull), then run dock build"
	}
	b, _ := os.ReadFile(filepath.Join(src, VersionFile))
	return true, strings.TrimSpace(string(b)), ""
}

// plugin is an installed native plugin that updates at @latest.
type plugin struct {
	module, current string
}

// updatable returns the unpinned, non-local native plugins and the module
// version the installed dock binary was built with.
func updatable(cfg *config.Config) []plugin {
	bi, err := readBuildInfo(config.BinPath())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []plugin
	for _, n := range cfg.Native {
		if n.Path != "" || (n.Version != "" && n.Version != "latest") {
			continue
		}
		if n.Package == build.DockModule || strings.HasPrefix(n.Package, build.DockModule+"/") {
			continue
		}
		dep := moduleOf(bi.Deps, n.Package)
		if dep == nil || dep.Replace != nil || seen[dep.Path] {
			continue
		}
		seen[dep.Path] = true
		out = append(out, plugin{dep.Path, dep.Version})
	}
	return out
}

func moduleOf(deps []*debug.Module, pkg string) *debug.Module {
	var best *debug.Module
	for _, d := range deps {
		if (pkg == d.Path || strings.HasPrefix(pkg, d.Path+"/")) && (best == nil || len(d.Path) > len(best.Path)) {
			best = d
		}
	}
	return best
}

func fetchLatest(cfg *config.Config) *latest {
	l := &latest{Checked: now().UTC(), Plugins: map[string]string{}}
	if managed, _, _ := managedDock(cfg); managed {
		sha, err := LatestDock()
		if err != nil {
			l.Errors = append(l.Errors, "dock: "+err.Error())
		}
		l.Dock = sha
	}
	for _, p := range updatable(cfg) {
		v, err := latestModule(p.module)
		if err != nil {
			l.Errors = append(l.Errors, p.module+": "+err.Error())
			continue
		}
		l.Plugins[p.module] = v
	}
	return l
}

func evaluate(cfg *config.Config, l *latest) *Status {
	s := &Status{Checked: l.Checked, Errors: l.Errors}
	managed, current, note := managedDock(cfg)
	switch {
	case !managed:
		s.DockNote = note
	case l.Dock != "" && l.Dock != current:
		if current == "" {
			current = "unknown"
		}
		s.Dock = &Item{Name: "dock", Current: current, Latest: l.Dock}
	}
	for _, p := range updatable(cfg) {
		if v := l.Plugins[p.module]; v != "" && Newer(v, p.current) {
			s.Plugins = append(s.Plugins, Item{Name: p.module, Current: p.current, Latest: v})
		}
	}
	sort.Slice(s.Plugins, func(i, j int) bool { return s.Plugins[i].Name < s.Plugins[j].Name })
	return s
}

// LatestDock returns the commit id of dock's main branch.
func LatestDock() (string, error) {
	client := &http.Client{Timeout: Timeout}
	req, err := http.NewRequest(http.MethodGet, CommitURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	req.Header.Set("User-Agent", "dock")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", CommitURL(), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(b))
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("%s: not a commit id: %q", CommitURL(), sha)
	}
	return sha, nil
}

// goLatest asks the Go toolchain for a module's latest version.
func goLatest(module string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GoTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "go", "list", "-m", "-json", module+"@latest")
	c.Dir = os.TempDir()
	c.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	var m struct{ Version string }
	if err := json.Unmarshal(out, &m); err != nil || m.Version == "" {
		return "", fmt.Errorf("go list: no version for %s", module)
	}
	return m.Version, nil
}

// ---- versions ----

// Newer reports whether semver a is newer than b. Pseudo-versions compare
// by their timestamp, as in Go.
func Newer(a, b string) bool { return compare(a, b) > 0 }

func compare(a, b string) int {
	pa, preA := splitVersion(a)
	pb, preB := splitVersion(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	ia, ib := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < len(ia) && i < len(ib); i++ {
		if c := compareIdent(ia[i], ib[i]); c != 0 {
			return c
		}
	}
	return len(ia) - len(ib)
}

func splitVersion(v string) ([3]int, string) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	var n [3]int
	for i, p := range strings.SplitN(core, ".", 3) {
		n[i], _ = strconv.Atoi(p)
	}
	return n, pre
}

func compareIdent(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return na - nb
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// ---- replacing dock's source ----

// Swap is a downloaded source tree put in place of the old one. Commit
// deletes the old tree; Restore puts it back.
type Swap struct {
	src, old string
}

// ReplaceSource downloads dock at commit sha and puts it in SourceDir,
// keeping the old tree until Commit or Restore.
func ReplaceSource(sha string) (*Swap, error) {
	if !shaRe.MatchString(sha) {
		return nil, fmt.Errorf("not a commit id: %q", sha)
	}
	src := SourceDir()
	next, old := src+".new", src+".old"
	if err := os.RemoveAll(next); err != nil {
		return nil, err
	}
	if err := download(TarballURL(sha), next); err != nil {
		os.RemoveAll(next)
		return nil, err
	}
	if mod, err := build.ModulePath(next); err != nil || mod != build.DockModule {
		os.RemoveAll(next)
		return nil, fmt.Errorf("the downloaded source is not dock (%v)", err)
	}
	if err := os.WriteFile(filepath.Join(next, VersionFile), []byte(sha+"\n"), 0o644); err != nil {
		os.RemoveAll(next)
		return nil, err
	}
	os.RemoveAll(old)
	if err := os.Rename(src, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.RemoveAll(next)
		return nil, err
	}
	if err := os.Rename(next, src); err != nil {
		os.Rename(old, src)
		return nil, err
	}
	return &Swap{src: src, old: old}, nil
}

// Commit drops the old source tree.
func (s *Swap) Commit() { os.RemoveAll(s.old) }

// Restore puts the old source tree back.
func (s *Swap) Restore() error {
	if _, err := os.Stat(s.old); err != nil {
		return err
	}
	if err := os.RemoveAll(s.src); err != nil {
		return err
	}
	return os.Rename(s.old, s.src)
}

// download extracts a .tar.gz from url into dir, dropping the top-level
// directory GitHub tarballs have.
func download(url, dir string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "dock")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxTarballSize))
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	tr := tar.NewReader(gz)
	files := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", url, err)
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || rel == "" {
			continue
		}
		rel = filepath.FromSlash(rel)
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("%s: unsafe path %q", url, h.Name)
		}
		target := filepath.Join(dir, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			files++
		}
	}
	if files == 0 {
		return fmt.Errorf("%s: empty tarball", url)
	}
	return nil
}
