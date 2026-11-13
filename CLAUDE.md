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
- `internal/manager/`: the built-in "plugins" system plugin. It edits the config and sends
  `ReloadMsg`.
- `internal/registry/`: merges system, native and exec plugins. `internal/config/`: the
  JSON config and XDG paths. `internal/execplugin/`: exec env vars, result file and the
  pause wrapper.
- **dock depends on no plugin and no other repo.** Native plugins call
  `plugin.Register` in `init()`. `internal/build` (xcaddy-style) generates
  `~/.cache/dock/build/main.go`, which blank-imports dock plus the config's `native`
  packages and calls `cli.Main()`, then compiles it to `~/.local/share/dock/bin/dock`.
  `ResolveDock` picks the dock source in this order: config `source`, the checkout next to
  the running binary, a dock dependency's replace path (inside a built binary), then the
  version. Go stamps local builds with a VCS pseudo-version, so checkouts take priority.
- `cli/`: the public command line (`cli.Main`); `main.go` is just `cli.Main()`.
  `install` and `remove` restore the config if the build fails. `dock plugin packages`
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
- The host only intercepts the leader key and ctrl+c. Don't add other global keys.

## Testing
`go test ./...` (internal/host/host_test.go drives the model with a fake runtime) and
`go vet ./...`.
Never test against the real config. Set `DOCK_CONFIG_DIR`, `DOCK_DATA_DIR` and
`XDG_CACHE_HOME` (the build dir) to temp dirs. A pty harness must answer the startup `OSC 11` and `CSI 6n` queries, or dock waits
on them.
