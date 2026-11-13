# dock

A terminal workspace for your own tools. dock hosts **plugins**:

- **Native plugins** are Go (Bubble Tea) models compiled into dock. They live in tabs,
  can tile side by side, and keep their state while you work in another tab.
- **Exec plugins** are any program on your `PATH`. dock hands the terminal over while the
  program runs, then comes back.

## Install

```bash
go build -o dock . && ln -sf "$PWD/dock" ~/.local/bin/dock
```

Native plugins from other repos are pulled in through `replace` directives
in `go.mod`. For now that is only [lazy1on1](../lazy1on1), at `../lazy1on1`,
so keep it checked out next to this repo.

## Included plugins

| ID | Kind | What it is |
|---|---|---|
| `lazy1on1` | native, full | lazygit-style continuous 1:1 notes (its own repo; data in `~/.onetoones`) |
| `scratch` | native, tile | a notepad that saves itself; the reference plugin |
| `plugins` | system | the plugin manager (`ctrl+g m`) |

## Use

```bash
dock                 # open the launcher (or the plugins in config "startup")
dock scratch         # start with plugins already open, one tab each
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

## Manage plugins

```bash
dock plugin list
dock plugin info <id>
dock plugin enable|disable <id>
dock plugin add --pause --name OSheet --desc "Log timesheet entries" osheet -- osheet
dock plugin remove osheet        # exec plugins only
dock config                      # print the config path
```

The `Plugins` screen inside dock (`ctrl+g m`) does the same interactively.

## Config

The config file is `$XDG_CONFIG_HOME/dock/config.json`:

```json
{
  "leader": "ctrl+g",
  "startup": ["scratch"],
  "disabled": [],
  "exec": [
    { "id": "osheet", "name": "OSheet", "command": ["osheet"], "pause": true }
  ]
}
```

Plugin data is stored in `$XDG_DATA_HOME/dock/<id>/` and plugin settings in
`$XDG_CONFIG_HOME/dock/plugins/<id>/`.

## Write a plugin

See **[docs/PLUGINS.md](docs/PLUGINS.md)**. It covers choosing native or exec, display
modes (tile, full, takeover), lifecycle (persistent, transient, one-shot), keys, storage,
host commands and a checklist. `plugins/scratch` is a small reference plugin.
