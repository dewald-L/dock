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
- `plugins/plugins.go`: the list of compiled-in native plugins. `plugins/scratch/` is the
  reference plugin. lazy1on1 lives in its own repo (`../lazy1on1`, package
  `dockplugin`) and is wired in with `replace github.com/dewald-L/lazy1on1 => ../lazy1on1`.
  lazy1on1 imports `dock/plugin` the same way, so a change to `plugin/` must still build
  there (`cd ../lazy1on1 && go build ./...`).

## Rules
- Bubble Tea v1 (bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0). Don't upgrade to v2
  without also porting plugins.
- Host-owned overlays (palette) must not emit unaddressed commands such as cursor blink,
  or they reach the focused plugin.
- The host only intercepts the leader key and ctrl+c. Don't add other global keys.

## Testing
`go test ./...` (internal/host/host_test.go drives the model with a fake runtime) and
`go vet ./...`.
Never test against the real config. Set `DOCK_CONFIG_DIR` and `DOCK_DATA_DIR` to temp
dirs. A pty harness must answer the startup `OSC 11` and `CSI 6n` queries, or dock waits
on them.
