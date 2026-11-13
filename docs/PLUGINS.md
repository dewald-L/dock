# Writing dock plugins

This is the contract between dock (the host) and its plugins. Read sections 1–3 before
starting a plugin. Use the rest as a reference, and finish with the checklist at the end.

`examples/scratch` is a complete reference plugin of about 160 lines, including a home
screen `Summary`. Copy it as a starting point.

dock itself contains no plugins. Each native plugin lives in its own Go package, usually
in its own repo, and dock compiles it in when you run `dock plugin install`.

---

## 1. Native or exec?

| | Native | Exec |
|---|---|---|
| What it is | A Go package compiled into dock | Any program on your `PATH`, registered in the config |
| How it runs | A Bubble Tea model inside a tab or pane | dock suspends and the program owns the terminal until it exits |
| Can tile next to other plugins | Yes, if `DisplayTile` | No |
| Keeps state when you switch away | Yes (`Persistent`) | No. It runs once, then it's gone |
| Uses the host theme, status bar and palette | Yes | No |
| Adding one | `dock plugin install <go-package>` (dock rebuilds itself with it) | `dock plugin add <id> -- <command>` |
| From the index | `dock plugin install <id>` | `dock plugin install <id>` (registers it; the program itself must be installed separately) |

**Pick native** for anything you keep open, switch back to, or want beside something
else. **Pick exec** for one-shot tools (log an entry, run a report), for tools in other
languages, and to try a tool out before porting it.

---

## 2. Choosing display and lifecycle

Every native plugin declares two things in its manifest. Answer these questions in order.

### Display: how much screen does it need?

| If the plugin... | Use | Effect |
|---|---|---|
| works in a narrow column or short strip (lists, notes, logs, timers) | `DisplayTile` | Opens in its own tab, and `ctrl+g v` can put it next to other tiles, up to 4 panes per tab. Set `MinWidth`/`MinHeight` to the size where it stops being usable. |
| has its own multi-panel layout, or needs the full width to make sense (lazygit-style apps, dashboards, editors) | `DisplayFull` | Always gets a whole tab to itself. Splitting it opens a new tab instead. The tab bar and status bar stay visible. |
| has to own the raw terminal (its own alt screen, mouse, inline output in the scrollback) | an **exec** plugin | Exec plugins are always `DisplayTakeover`. A native plugin can't use takeover. |

Prefer `DisplayTile` when you can, because it is the more flexible choice. Only use
`DisplayFull` when the plugin would look broken at half width.

### Lifecycle: what happens when it's not on screen?

| If the plugin... | Use | Effect |
|---|---|---|
| holds work in progress you'll come back to (most plugins) | `Persistent` | Stays alive in the background with its state intact. Its timers and I/O results keep arriving while it is hidden. |
| is a quick form or picker that should start fresh every time | `Transient` | Closed (with `Close()` called) as soon as you leave its tab after it has been shown. |
| runs once and exits | exec plugin (`OneShot`) | Set automatically for exec plugins. |

### Instances

`Multi: false` (the default) means opening the plugin again focuses the instance that is
already open. Set `Multi: true` only when two copies make sense side by side, such as two
log viewers. Two instances share the same `DataDir`, so a `Multi` plugin must not keep
mutable global state.

---

## 3. Anatomy of a native plugin

```go
package myplugin

import (
    tea "github.com/charmbracelet/bubbletea"
    "github.com/dewald-L/dock/plugin"
)

type Plugin struct{}

// Importing the package registers the plugin. This is the only wiring dock needs.
func init() { plugin.Register(Plugin{}) }

func (Plugin) Manifest() plugin.Manifest {
    return plugin.Manifest{
        ID: "myplugin", Name: "My Plugin", Version: "0.1.0",
        Description: "One line that says what it's for",
        Display:     plugin.DisplayTile,
        Lifecycle:   plugin.Persistent,
        MinWidth: 30, MinHeight: 8,
    }
}

func (Plugin) New(ctx plugin.Context) (tea.Model, error) {
    // open stores, read files: return errors, don't panic
    return &model{dir: ctx.DataDir, theme: ctx.Theme}, nil
}

type model struct { /* ... */ }

func (m *model) Init() tea.Cmd                           { return nil }
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) { /* ... */ }
func (m *model) View() string                            { /* ... */ }
```

Install it:

```bash
dock plugin install github.com/you/myplugin                    # latest published version
dock plugin install github.com/you/myplugin@v1.2.0             # a specific version
dock plugin install --path ~/Projects/myplugin github.com/you/myplugin   # local checkout
```

`install` records the package in the config and generates a small main module in
`~/.cache/dock/build` that imports dock and every installed plugin package. It then
compiles that module to `~/.local/share/dock/bin/dock`. Point `dock` on your `PATH` at
that file. `dock build` rebuilds the same thing, for example after you change a plugin,
and `dock plugin remove <id>` uninstalls and rebuilds. If a build fails, the config is
left as it was.

How dock finds its own source: the config's `"source"` (a local checkout) wins. Next is
the checkout the running binary was built in. Then the dock version the binary was built
from. Finally, the latest release.

Rules for the plugin's module:

- Import only `github.com/dewald-L/dock/plugin` from dock, never anything under
  `internal/` (Go won't allow it anyway).
- `require` a real, published dock version in `go.mod`. A local `replace` is fine for
  developing the plugin, but builds ignore it: dock's builder supplies dock itself.
- One package may register several plugins.

### Manifest fields

| Field | Rule |
|---|---|
| `ID` | Lowercase letters, digits and `-`. **Never change it after release**, because it names the plugin's data and config directories. |
| `Name` | Shown in tabs, pane borders, the home screen and the palette. Keep it short (one or two words). |
| `Description` | One line, shown in its home screen entry and `dock plugin list`. |
| `Version` | Free-form; semver recommended. |
| `Display`, `Lifecycle`, `Multi` | See section 2. |
| `MinWidth`, `MinHeight` | Below this size the host shows a placeholder instead of calling `View`. |

---

## 4. The model contract

These are the rules for `Init`, `Update` and `View`. The host relies on all of them.

1. **Size comes from `tea.WindowSizeMsg`.** It holds the size of *your pane*, not the
   terminal. It arrives before your first `View` and again whenever the pane changes size
   (split, zoom, rotate, terminal resize). Never call `term.GetSize` and never assume the
   pane is full-screen.
2. **`View` must be cheap, and it should fit.** It runs on every frame. Return exactly the
   width and height you were given. The host clips anything larger and pads anything
   smaller, but clipped output usually looks broken.
3. **Don't draw an outer border.** The host draws borders and titles for panes in a split.
   When the plugin is alone in a tab it gets the bare area. Inner borders and panels are
   fine.
4. **`Update` must never block.** File and network I/O, and anything slow, belongs in a
   `tea.Cmd`. A blocked `Update` freezes every plugin.
5. **Don't touch the terminal.** No `tea.NewProgram`, no `tea.EnterAltScreen`, no mouse
   modes, no writes to stdout or stderr, no `os.Exit`. The host owns the terminal.
6. **No goroutines that modify the model.** Do background work in commands and return
   results as messages.
7. **Quitting closes only you.** Returning `tea.Quit`, or `plugin.Close()`, closes this
   instance. The host keeps running.
8. **Handle focus.** `plugin.FocusMsg` and `plugin.BlurMsg` arrive when your pane gains or
   loses focus. Show the cursor and caret only while focused. Blur is the right moment to
   autosave. In a split, the unfocused pane is still visible, so keep it readable.
9. **Panics are caught, but don't rely on that.** If `Init`, `Update` or `View` panics, the
   host closes that instance, shows the error in the status bar, and does not call
   `Close()`. Return errors from `New` and show runtime errors with `plugin.Error`.

---

## 5. Keys

### Reserved by the host

| Key | Meaning |
|---|---|
| `ctrl+g` (the leader, configurable) | Starts every host shortcut. The plugin never sees it, except when the user presses it twice. |
| `ctrl+c` | Quits dock and closes every plugin cleanly. |

Every other key goes to the focused plugin, so bind freely. Avoid `ctrl+g` and `ctrl+c`.

### Conventions

Following these makes all plugins feel like one app:

| Key | Use |
|---|---|
| `q` / `esc` | Back out of the current view. At the top level, close the plugin (`tea.Quit` or `plugin.Close()`). |
| `?` | Your own full help screen. |
| `j`/`k`, `↑`/`↓` | Move. `g`/`G` for top and bottom. |
| `enter` | Open or confirm. |
| `tab` / `shift+tab` | Move between your own panels. |
| `/` | Filter or search. |
| `ctrl+s` | Save, in editors and forms. |

**Text entry:** while a text field has focus, send every printable key to the field,
including `q` and `?`. Only `esc` (and `enter` or `ctrl+s` where it makes sense) should
leave the field.

### Status bar hints (`plugin.Helper`)

```go
func (m *model) Help() []plugin.KeyHelp {
    return []plugin.KeyHelp{{Key: "n", Desc: "new"}, {Key: "?", Desc: "help"}}
}
```

Show 3–6 keys that fit the current mode. It runs on every render, so keep it cheap.

---

## 6. Talking to the host

Return these commands from `Init` or `Update`. The host knows which instance sent them.

| Command | Effect |
|---|---|
| `plugin.Notify("saved")` | Status bar message for a few seconds |
| `plugin.Error(err)` | Status bar error (does nothing when `err` is nil) |
| `plugin.Close()` | Close this instance (same as `tea.Quit`) |
| `plugin.Open("other-id", split)` | Open or focus another plugin. With `split`, tile it next to this pane when possible. |
| `plugin.SetTitle("1:1 · Ana")` | Change this instance's tab and border title |
| `plugin.Exec(cmd, fn)` | Hand the terminal to `cmd` (e.g. `$EDITOR`). `fn(err)` comes back to this instance. |

**Use `plugin.Exec`, not `tea.ExecProcess`.** The host can't tell which instance started a
raw `tea.ExecProcess`, so its result goes to whichever pane is focused when it finishes.

### Palette commands (`plugin.Commander`)

```go
func (m *model) Commands() []plugin.Command {
    return []plugin.Command{{Name: "new meeting", Desc: "start a 1:1 now", Msg: newMeetingMsg{}}}
}
```

While your plugin is focused, these appear in the palette (`ctrl+g :`) as
`My Plugin: new meeting`. When the user picks one, `Msg` arrives in your `Update`. Expose
the actions people might forget the key for.

---

## 7. Async work and message routing

The host wraps every command you return, so its result comes back **to your instance**
even when you are hidden or not focused. That includes `tea.Batch`, `tea.Sequence`,
`tea.Tick` and plain functions. In practice:

- Timers and pollers keep running while a `Persistent` plugin is hidden. Stop scheduling
  them on `BlurMsg` when they are only useful on screen.
- Define your own message types (`type loadedMsg struct{...}`) rather than reusing generic
  ones, so they can't be mistaken for anything else.
- `tea.WindowSize()`, `tea.ClearScreen` and other runtime commands are passed to Bubble
  Tea, not to you. You don't need them.

---

## 8. Storage

| Path | Use |
|---|---|
| `ctx.DataDir` = `$XDG_DATA_HOME/dock/<id>/` | User data |
| `ctx.ConfigDir` = `$XDG_CONFIG_HOME/dock/plugins/<id>/` | Settings |

Both exist before `New` is called.

- **Existing tools:** if a plugin already keeps its data somewhere (`~/.onetoones`, for
  example), keep using that location rather than migrating silently. Honour the tool's own
  env var or flag, and fall back to `DataDir` only for fresh installs. Say which you chose
  in the README.
- Save on `BlurMsg` and in `Close()`, and offer an explicit save (`ctrl+s` or a palette
  command). Don't rely only on `Close()`, because a crash skips it.
- Write files atomically (temp file plus rename) when a half-written file would corrupt
  data.
- **`Close()`** (`plugin.Closer`) is called once, when the instance is closed or dock
  quits. Flush there and return quickly.

---

## 9. Look and feel

- Style with `ctx.Theme` (`Accent`, `Muted`, `Border`, `Ok`, `Warn`, `Err`), not hard-coded
  colours. The defaults are ANSI colours, so plugins follow the terminal's theme.
- Don't paint a full background colour. Leave the terminal background showing.
- Put your keys in `Help()` rather than drawing a footer of shortcuts. The host's status
  bar is the footer. A one-line state footer ("saved", "3 items") is fine.
- Use the `Name` from your manifest consistently. Use `SetTitle` for context such as the
  person or file in view.

---

## 10. Home screen summaries

dock's home screen (shown while no tab is open) is a full-width vertical
list of entries, one per plugin: `[1] Name` in the title, the description, and the kind
at the bottom. A plugin can add a few lines of live status to its entry, so the home
screen works as a dashboard:

```
dock 0.2.0
 ╭─ [1] lazysheet ───────────────────────────────── ● ─╮
 │ Timesheets: log, browse, edit and total hours        │
 │ Today 8h15 · this week 18h15 / 40h                   │
 │ Mo█ Tu▆ We█ Th· Fr· Sa· Su·                          │
 ╰───────────────────────────────────── native · full ─╯
 ╭─ [2] Scratch ────────────────────────────────────────╮
 │ A notepad that saves itself                          │
 ╰───────────────────────────────────── native · tile ─╯
                               1-9 open · j/k move · ? help
```

**When to implement one:** when there is one or two numbers worth seeing without
opening the plugin (hours logged, open items, unread count, last run). Skip it when the
summary would only repeat the description.

Implement `plugin.Summarizer` on the **Plugin value**, not the model, so it works
while no instance is open:

```go
func (Plugin) Summary(ctx plugin.Context) (plugin.Summary, error) {
    n, err := countOpen(ctx.DataDir) // read files; no model, no shared state
    if err != nil {
        return plugin.Summary{}, err
    }
    s := plugin.Summary{Lines: []string{fmt.Sprintf("%d open items", n)}}
    if n > 10 {
        s.Level = plugin.LevelWarn
    }
    return s, nil
}
```

The rules:

- **Plain text only.** Up to 3 short lines (`plugin.MaxSummaryLines`); the host
  truncates lines that don't fit the entry, and strips ANSI codes and control
  characters. No layout, no colour: `Level` (`LevelOk`, `LevelWarn`, `LevelErr`, or
  the default `LevelNone`) puts a dot in the theme's colour on the entry's border.
- **Read-only, and independent of any instance.** `ctx` has the same `DataDir`,
  `ConfigDir` and `Theme` as `New` gets, but no model exists necessarily.
- **Runs off the UI goroutine, possibly while an instance of the same plugin is open,
  and concurrently with other plugins' summaries.** Don't touch globals your model
  mutates. Reading files your model writes atomically is fine.
- **Cheap: aim for under 100 ms.** dock fetches summaries when the home screen
  appears and every 30 seconds while it stays visible, and stops when it's hidden.
  After 2 seconds a call counts as timed out. A call that never returns blocks further
  calls for that plugin (its entry says "not responding"); it never blocks other
  entries or the UI.
- **Errors and panics** are shown, muted, in your entry only. After a failed refresh
  the last good summary stays, with a "refresh failed" note.
- Plugins without `Summarizer` keep working unchanged; their entry shows the
  description only.

**Exec plugins** can have a summary too: a command whose stdout (first 3 lines) is
shown. Set it with `dock plugin add --summary 'df -h --output=avail,pcent / | tail -1' ...` (run with
`sh -c`) or the config's `"summary": ["prog", "arg"]`. It gets the plugin's usual
environment plus `DOCK_SUMMARY=1`, runs under the same 2 second timeout, and a
non-zero exit shows the first line of stderr.

Home screen keys: `1`–`9` open the plugin with that number, `j`/`k` (or arrows)
move, `enter`/`o` open the selected one. Numbers follow the plugin
order (built-in plugins last), stay fixed while the home screen is open, and stop at
9. These keys only exist on the home screen: inside a tab, digits go to the plugin
as usual.

---

## 11. Running outside dock

A native plugin can stay usable as its own binary. Put the plugin in a package and add a
small `main`:

```go
func main() {
    err := plugin.RunStandalone(myplugin.Plugin{}, plugin.Context{
        DataDir: dataDir, ConfigDir: configDir,
    })
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
```

`RunStandalone` sends `FocusMsg` at start. It maps `Close` and `tea.Quit` to exit,
`Exec` to `tea.ExecProcess` and `SetTitle` to the terminal title. It ignores `Open` and
prints the last `Notify` on exit. The same model then runs unchanged in both places.

---

## 12. Exec plugins

Register them with:

```
dock plugin add [--name N] [--desc D] [--dir DIR] [--pause] [--summary CMD] <id> -- <command> [args...]
```

The program runs in the real terminal, with dock suspended (alt screen left, raw mode
off). It gets these environment variables:

| Variable | Value |
|---|---|
| `DOCK` | `1`. Lets a tool detect that it's running inside dock. |
| `DOCK_PLUGIN_ID` | Its id |
| `DOCK_DATA_DIR`, `DOCK_CONFIG_DIR` | Its directories (they exist) |
| `DOCK_RESULT_FILE` | Write one line here and dock shows it in the status bar when you return (e.g. `Logged 2h05 to ProjectX`). |

- **Exit status:** non-zero is shown as an error, together with the result line if there
  is one.
- **`--pause`:** waits for Enter after the program exits, so its final output stays on
  screen before dock redraws. Use it for tools that print a summary and exit (osheet).
- **Working directory:** your home directory, unless `--dir` is set.
- **`--summary CMD`:** a home screen summary command (section 10).
- Exec plugins can't tile and keep no state between runs. Port a tool to native when you
  want either.

---

## 13. Checklist

- [ ] `ID` is final, lowercase and unique; `Name` is short; `Description` is one line.
- [ ] `Display` chosen with section 2. If `Tile`, `MinWidth`/`MinHeight` are set and the
      plugin looks right at that size.
- [ ] `Lifecycle` chosen. If `Multi`, there is no shared mutable state.
- [ ] Layout uses only `tea.WindowSizeMsg`, and `View` fits it exactly.
- [ ] No terminal control, no stdout writes, no `os.Exit`, no blocking in `Update`.
- [ ] Focus and blur handled: cursor only while focused, autosave on blur.
- [ ] `Close()` flushes state. Errors go through `plugin.Error`.
- [ ] Doesn't bind `ctrl+g` or `ctrl+c`. Follows the key conventions. `Help()` returns 3–6
      keys.
- [ ] Useful actions exposed through `Commands()`.
- [ ] `plugin.Exec` used instead of `tea.ExecProcess`.
- [ ] Styled with `ctx.Theme`.
- [ ] Data in `ctx.DataDir`, or the tool's existing location, documented.
- [ ] If there is status worth a glance, `Summary` is implemented on the Plugin value:
      plain text, at most 3 lines, read-only, safe to call concurrently, under 100 ms,
      and unit-tested.
- [ ] `plugin.Register` called from `init`. `dock plugin install` builds it, and
      `dock plugin list` shows it.
- [ ] Tested in a tab, in a 2-pane split (when Tile), zoomed, and after a terminal
      resize.
- [ ] To share it, published in the plugin index (section 14).

---

## 14. Publishing a plugin

The [dock-plugins](https://github.com/dewald-L/dock-plugins) repo holds `plugins.json`,
the index dock reads for `dock plugin search`, `dock plugin install <id>`, the Plugins
screen's Available section. dock installs nothing from it on its own: users pick
plugins. To publish a plugin:

1. **Tag a version** in the plugin's repo (`git tag v0.1.0 && git push --tags`). Its
   `go.mod` must `require` a published dock version (section 3). An entry without a
   version follows the latest commit instead.
2. **Open a PR** in dock-plugins that adds an entry and a row to its README table:

   ```json
   {
     "id": "myplugin",
     "name": "My Plugin",
     "description": "One line that says what it's for",
     "kind": "native",
     "package": "github.com/you/myplugin/dockplugin",
     "version": "v0.1.0",
     "repo": "https://github.com/you/myplugin",
     "tags": ["notes"]
   }
   ```

   `id`, `name`, `description` and `package` must match the manifest and the import
   path. Users of an unpinned entry get your new releases with `dock update` (dock
   tells them when one exists); a pinned entry changes only when the index does. `version` is a tag or commit (empty: latest). Exec plugins use
   `"kind": "exec"`, a `command` argv, an optional `summary` argv, and `requires`, a
   one-line hint such as `"needs osheet on PATH"`: dock registers them but can't install
   the program.
3. **CI checks** the JSON shape (format version, no unknown fields), that ids are valid
   and unique, required fields, that native entries have a package and exec entries a
   command, and that each native package resolves with `go list -m` at its version. Run
   `go test ./...` in dock-plugins to check before pushing.

The file's top-level `"version"` is the index format. dock ignores unknown fields, so
new optional fields need no bump; a format dock can't read makes it ask the user to
update dock.
