# dock

A Go / Bubble Tea terminal host for plugins. User docs are in README.md, and the plugin
contract is in docs/PLUGINS.md. Keep both in sync with any change to keys, the CLI or the
plugin API.

## Layout
- `plugin/`: the **public** plugin API (Manifest, Context, Theme, optional interfaces,
  host commands, RunStandalone). This is the only dock package plugins may import. Treat
  changes here as API changes.
- `internal/host/`: the root model.
  - `app.go`: state, open/close, `sync()`. `sync()` runs after every Update and reaps
    crashed and transient instances, sends `WindowSizeMsg` on size changes, and sends
    Focus/Blur.
  - `route.go`: wraps every plugin command so its result returns as `envelope{id,msg}`.
    `tea.BatchMsg` and tea's unexported `sequenceMsg` (via reflect) are unpacked and
    re-wrapped. Unexported tea messages go back to the runtime. Calls into plugin code are
    panic-safe.
  - `update.go`: keys and the leader. `view.go`: layout and rendering (every line is
    padded or clipped to the exact width). `palette.go`: the command palette.
  - `home.go`: the home screen (no tab open): `renderHome` draws a centred modal
    (≤ 64 wide) with a vertical list of bordered entries that scrolls inside it, or
    `renderHomeList`, a plain list, when the tallest entry doesn't fit or the width is
    under 40. `borderLine` draws labelled borders. `homeKey` (digits, j/k, g/G, enter).
    `homeIDs` fixes the order and numbers (installed plugins first, system last) each
    time home appears.
  - `summary.go`: `plugin.Summarizer` and exec `summary` commands. `syncHome()` (called
    from `sync()`) fetches when home appears and bumps `homeGen`; `summaryTickMsg{gen}`
    refreshes every 30 s and is dropped once home is hidden. Each fetch is its own
    `tea.Cmd` with a timeout and recover; `sumState.busy` stops a hung call from being
    repeated. Never call Summary from Update or View.
- `internal/manager/`: the built-in "plugins" system plugin. It edits the config and sends
  `ReloadMsg`.
  It also shows the "Available" section: index entries not in the registry, loaded by
  `fetchIndex` in a `tea.Cmd` (`availMsg{gen}`; stale generations are dropped). `i` runs
  `<os.Executable()> plugin install <id>` via `plugin.Exec` with the pause wrapper
  (`execplugin.Paused`); a native install only takes effect after a restart. `View`
  returns exactly w×h (`fitBlock`) and scrolls the list to the selection.
- `internal/index/`: the plugin index (dock-plugins' `plugins.json`). `Source` is
  `DOCK_REGISTRY` > config `registry` > `DefaultURL`; local paths and `file://` are read
  directly, URLs are fetched (5 s timeout, 1 MiB cap) and cached in
  `$XDG_CACHE_HOME/dock/index.json` for an hour, with any-age fallback when offline
  (`Result.Stale`). `"version"` above `FormatVersion` is `ErrNewerFormat` ("update
  dock"), never masked by the cache. Unknown fields are ignored. Never fetch from
  Update/View or at startup.
- `internal/update/`: update checks and `dock update`. dock is "managed" only when
  `ResolveDock` gives `$DATA/src/dock` (install.sh's tree); its commit is in
  `.dock-version` (install.sh writes it from GitHub's commit API). Latest = main's sha
  (`Accept: application/vnd.github.sha`) and `go list -m mod@latest` for unpinned,
  non-local plugins, whose current version comes from the build info of
  `config.BinPath()`. Only latest versions are cached (`$XDG_CACHE_HOME/dock/update.json`,
  24 h); installed versions are read fresh. `ReplaceSource` swaps in the new tree and
  keeps `.old` until `Commit`/`Restore`. `DOCK_UPDATE_SERVER` replaces both GitHub hosts
  (tests); `DOCK_NO_UPDATE_CHECK` or config `update_check: false` turns the background
  check off. The host runs `Options.CheckUpdates` once in a `tea.Cmd` after Init, never
  when it is nil (host tests leave it nil).
- `internal/registry/`: merges system, native and exec plugins. `internal/config/`: the
  JSON config and XDG paths. `internal/execplugin/`: exec env vars, result file, the
  pause wrapper and the summary command runner.
- `go.mod`'s `replace` lines point `golang.org/x/*` at GitHub mirrors because some
  networks get 403 from proxy.golang.org for those. Go only applies the main module's
  replaces, so `internal/build` copies dock's (and local plugins') into the generated
  build module (`replaceArgs`). Don't remove them.
- **dock depends on no plugin and no other repo.** Native plugins call
  `plugin.Register` in `init()`. `internal/build` (xcaddy-style) generates
  `~/.cache/dock/build/main.go`, which blank-imports dock plus the config's `native`
  packages and calls `cli.Main()`, then compiles it to `~/.local/share/dock/bin/dock`.
  `ResolveDock` picks the dock source in this order: config `source`, the checkout next to
  the running binary, a dock dependency's replace path (inside a built binary), then the
  version. Go stamps local builds with a VCS pseudo-version, so checkouts take priority.
- `install.sh`: the curl-pipe installer (POSIX sh, macOS and Linux, no sudo). It installs
  Go into ~/.local/go when there is no Go 1.21+, downloads the main tarball into
  ~/.local/share/dock/src/dock and builds there (`go install dock@latest` refuses
  dock's go.mod replace directives), installs plugins (`dock plugin install --defaults`
  from the index, falling back to a plain `dock build`; arguments are ids or packages,
  `none` means none), links
  ~/.local/bin/dock and edits the shell rc once. It sets `GOPRIVATE=github.com/dewald-L/*` so
  plugins' `@latest` comes from git, not a stale proxy cache. Check it with `shfmt -p` and a run
  in a sandbox HOME.
- `cli/`: the public command line (`cli.Main`); `main.go` is just `cli.Main()`.
  `install` and `remove` restore the config if the build fails. `install <arg>` treats
  an argument without `/` as an index id; `--defaults` installs all default entries with
  one rebuild. `buildDock` is a var so tests can stub the compile. `dock plugin packages`
  (hidden) prints the registered packages and is used to verify an install.
- `examples/scratch/`: the reference plugin. It is not built in, but can be installed by
  its package path.
- External plugins (e.g. lazy1on1 at ../lazy1on1) import `dock/plugin`. After changing
  `plugin/`, check they still build.

## Rules
- Bubble Tea v1 (bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0). Don't upgrade to v2
  without also porting plugins.
- Host-owned overlays (palette) must not emit unaddressed commands such as cursor blink,
  or they reach the focused plugin.
- The host only intercepts the leader key and ctrl+c. Don't add other global keys. The
  home screen's own keys (digits, hjkl...) only apply while no tab is open.
- `plugin.Summarizer` is public API: keep it backwards compatible (optional interface,
  plain-text lines, ≤ 3 lines).

## Testing
`go test ./...` (internal/host/host_test.go drives the model with a fake runtime;
home_test.go covers the home grid, number keys and summary fetching) and `go vet ./...`.
The fake runtime drops commands slower than 50 ms, so home tests use `newHomeApp`, which
shortens the summary timeout.
Never test against the real config or the real index. Set `DOCK_CONFIG_DIR`,
`DOCK_DATA_DIR` and `XDG_CACHE_HOME` (the build dir and index cache) to temp dirs, and
`DOCK_REGISTRY` to a fixture file or an `httptest` server. Update tests set
`DOCK_UPDATE_SERVER` to a fake GitHub; e2e sandboxes set `DOCK_NO_UPDATE_CHECK=1`
except the update scenario.
`python3 -I e2e/run.py [--show] [--only cli,tui,offline,install]` runs the pty and
install.sh end-to-end tests in sandboxes (`e2e/harness.py` is a copy of lazysheet's);
`--show` prints the Plugins screen at 60x20 and 120x40. A pty harness must answer the startup `OSC 11` and `CSI 6n` queries, or dock waits
on them.
