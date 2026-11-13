# dock

A terminal workspace for your own tools. dock hosts **plugins**:

- **Native plugins** are Go (Bubble Tea) packages. dock compiles itself with the ones you
  install. They live in tabs, can tile side by side, and keep their state while you work
  in another tab.
- **Exec plugins** are any program on your `PATH`. dock hands the terminal over while the
  program runs, then comes back.

dock ships with no plugins other than its plugin manager. Everything else is installed.

## Install

```bash
go build -o dock . && ln -sf "$PWD/dock" ~/.local/bin/dock
```

The first time you install a native plugin, dock builds a new binary at
`~/.local/share/dock/bin/dock`. Point your `PATH` at that file instead (dock prints the
`ln -sf` command). Building needs the Go toolchain.

## Plugins

```bash
# native (Go) plugins: dock rebuilds itself
dock plugin install github.com/dewald-L/lazy1on1/dockplugin
dock plugin install --path ~/Projects/lazy1on1 github.com/dewald-L/lazy1on1/dockplugin  # local checkout
dock plugin install github.com/dewald-L/dock/examples/scratch                            # the example
dock build                       # rebuild with the configured plugins

# exec plugins: any program
dock plugin add --pause --name OSheet --desc "Log timesheet entries" osheet -- osheet

dock plugin list
dock plugin info <id>
dock plugin enable|disable <id>
dock plugin remove <id>          # exec: unregister; native: uninstall and rebuild
dock config                      # print the config path
```

The `Plugins` screen inside dock (`ctrl+g m`) lists, opens, enables and disables plugins.

## Use

```bash
dock                 # open the launcher (or the plugins in config "startup")
dock lazy1on1        # start with plugins already open, one tab each
```

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
| `ctrl+g m` | manage plugins (enable, disable, inspect, remove) |
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
`"source"` setting points builds at a local dock checkout.

Plugin data is stored in `$XDG_DATA_HOME/dock/<id>/` and plugin settings in
`$XDG_CONFIG_HOME/dock/plugins/<id>/`.

## Write a plugin

See **[docs/PLUGINS.md](docs/PLUGINS.md)**. It covers choosing native or exec, display
modes (tile, full, takeover), lifecycle (persistent, transient, one-shot), keys, storage,
host commands and a checklist. `examples/scratch` is a small reference plugin.
