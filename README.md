# dock

A terminal workspace for your own tools. dock hosts **plugins**:

- **Native plugins** are Go (Bubble Tea) packages. dock compiles itself with the ones you
  install. They live in tabs, can tile side by side, and keep their state while you work
  in another tab.
- **Exec plugins** are any program on your `PATH`. dock hands the terminal over while the
  program runs, then comes back.

dock ships with no plugins other than its plugin manager. Everything else is installed.

## Install

On macOS or Linux, one command installs dock (and Go into `~/.local/go` if you don't
have Go 1.21+). It needs no sudo. Run it again to update:

```bash
curl -fsSL https://raw.githubusercontent.com/dewald-L/dock/main/install.sh | sh
```

It installs no plugins. Pick them from the
[plugin index](https://github.com/dewald-L/dock-plugins) afterwards (`dock plugin search`,
or the Plugins screen, `ctrl+g m`), or name them, by id or Go package, when installing:

```bash
curl -fsSL https://raw.githubusercontent.com/dewald-L/dock/main/install.sh | sh -s -- lazy1on1 github.com/you/plugin/dockplugin
```

It downloads dock's source to `~/.local/share/dock/src/dock`, builds it, installs the
plugins, links `~/.local/bin/dock`, and adds `~/.local/bin` to your shell's PATH. Open a
new terminal afterwards.

### Updates

Once a day dock checks in the background whether dock (installed with `install.sh`) or
any native plugin has a newer version. If so, the status bar says so at startup, the home
screen shows `update available` next to the version, and the palette's **update dock**
(`ctrl+g :`) runs the update with its output on screen.

```bash
dock update --check   # show what's new, change nothing
dock update           # download dock's latest main, rebuild with plugins at @latest
```

Plugins pinned to a version or installed with `--path` are left as they are. A dock built
from a checkout (or the config's `"source"`) is yours to update: `git pull`, then
`dock build`; `dock update` still updates its plugins. If the build fails, the old source
is put back. Set `"update_check": false` in the config to turn the background check off.

From a checkout instead:

```bash
go build -o dock . && ln -sf "$PWD/dock" ~/.local/bin/dock
```

The first time you install a native plugin, dock builds a new binary at
`~/.local/share/dock/bin/dock`. Point your `PATH` at that file instead (dock prints the
`ln -sf` command). Building needs the Go toolchain.

## Plugins

```bash
# from the plugin index
dock plugin search [term]        # list plugins you can install (marks installed ones)
dock plugin install lazy1on1     # install by id (native: rebuilds dock; exec: registers it)
dock plugin info lazy1on1        # works before it's installed too

# native (Go) plugins by package: dock rebuilds itself
dock plugin install github.com/dewald-L/lazy1on1/dockplugin
dock plugin install --path ~/Projects/lazy1on1 github.com/dewald-L/lazy1on1/dockplugin  # local checkout
dock plugin install github.com/dewald-L/dock/examples/scratch                            # the example
dock build                       # rebuild with the configured plugins

# exec plugins: any program
dock plugin add --pause --name OSheet --desc "Log timesheet entries" osheet -- osheet
dock plugin add --name Disk --summary 'df -h --output=avail,pcent / | tail -1' disk -- ncdu /   # --summary: home screen lines

dock plugin list
dock plugin info <id>
dock plugin enable|disable <id>
dock plugin remove <id>          # exec: unregister; native: uninstall and rebuild
dock config                      # print the config path
```

The `Plugins` screen inside dock (`ctrl+g m`) lists, opens, enables and disables plugins.
Below the installed plugins, **Available** lists the index's plugins that aren't
installed: `i` installs the selected one (the build runs in the terminal; native plugins
load after you restart dock), `r` refreshes the index, `?` shows the screen's keys.

### The plugin index

`dock plugin search`, install by id and the Available section read
[`plugins.json`](https://github.com/dewald-L/dock-plugins) from the
[dock-plugins](https://github.com/dewald-L/dock-plugins) repo. dock caches it in
`~/.cache/dock/index.json` (macOS: `~/Library/Caches/dock/`; `$XDG_CACHE_HOME/dock/` when
set) for an hour, so a newly published plugin can take up to an hour to appear: press
`r` on the Plugins screen to refresh now; offline, it uses the cache whatever its
age and says so, e.g. `(cached, 3h)`. Point dock at another index with the config's
`"registry"` (an https URL, a `file://` URL or a path) or the `DOCK_REGISTRY` env var,
which overrides it. To publish a plugin, see
[docs/PLUGINS.md](docs/PLUGINS.md#14-publishing-a-plugin).

## Use

```bash
dock                 # open the home screen (or the plugins in config "startup")
dock lazy1on1        # start with plugins already open, one tab each
```

### Home screen

While no tab is open, dock shows a vertical list of numbered,
bordered plugin entries, each as wide as the terminal. Each entry has the plugin's description, its kind and, for
plugins that provide one, a live summary such as today's hours or open action items.
The list scrolls inside the panel when it doesn't fit; on very small terminals it
becomes a plain one-line-per-plugin list. Summaries refresh every 30 seconds while the
home screen is visible; a slow or broken plugin only affects its own entry.

| Keys | Action |
|---|---|
| `1`–`9` | open the plugin with that number |
| `j k`, arrows | move (`g`/`G` first/last) |
| `enter`, `o` | open the selected plugin |
| `:` or `p` | command palette |
| `m` | manage plugins |
| `?` | help |
| `q` | quit |

Numbers follow the plugin order (built-in plugins last) and stay fixed while the home
screen is open. Plugins after the ninth have no number. Inside a tab, digits go to the
plugin; `ctrl+g 1`–`9` switches tabs.

### Leader keys

Every host shortcut starts with the **leader key, `ctrl+g`**. All other keys go to the
focused plugin.

| Keys | Action |
|---|---|
| `ctrl+g :` (or `p`, `space`) | command palette: every action, plus the focused plugin's commands |
| `ctrl+g o` | open a plugin in a new tab |
| `ctrl+g v` | open a tile plugin next to the current pane |
| `ctrl+g x` | close the focused pane |
| `ctrl+g tab` / `h j k l` | move between panes |
| `ctrl+g z` | zoom the focused pane |
| `ctrl+g r` | rotate the split (columns ↔ rows) |
| `ctrl+g ]` `[` `1`–`9` | switch tabs |
| `ctrl+g m` | manage plugins (enable, disable, inspect, remove, install) |
| `ctrl+g ?` | help |
| `ctrl+g q`, `ctrl+c` | quit |
| `ctrl+g ctrl+g` | send `ctrl+g` itself to the plugin |

## Config

The config file is `$XDG_CONFIG_HOME/dock/config.json`:

```json
{
  "leader": "ctrl+g",
  "startup": ["lazy1on1"],
  "disabled": [],
  "native": [
    { "package": "github.com/dewald-L/lazy1on1/dockplugin", "path": "/home/you/Projects/lazy1on1" }
  ],
  "exec": [
    { "id": "osheet", "name": "OSheet", "command": ["osheet"], "pause": true }
  ]
}
```

`native` entries take `package` plus either `version` or a local `path`. The optional
`"source"` setting points builds at a local dock checkout, `"update_check": false` turns
off the daily update check, and `"registry"` sets the
plugin index (default
`https://raw.githubusercontent.com/dewald-L/dock-plugins/main/plugins.json`).

Plugin data is stored in `$XDG_DATA_HOME/dock/<id>/` and plugin settings in
`$XDG_CONFIG_HOME/dock/plugins/<id>/`.

## Write a plugin

See **[docs/PLUGINS.md](docs/PLUGINS.md)**. It covers choosing native or exec, display
modes (tile, full, takeover), lifecycle (persistent, transient, one-shot), keys, storage,
host commands and a checklist. `examples/scratch` is a small reference plugin.
