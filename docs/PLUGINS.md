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
at the bottom. A plugin can add live status to its entry, from a couple of text lines to small
charts, so the home screen works as a dashboard:

```
dock 0.2.0
 ╭─ [1] lazysheet ──────────────────────────────────────────── ● ─╮
 │ Timesheets: log, browse, edit and total hours                  │
 │ Today 8h15 · this week 41h00 / 40h                             │
 │ ███████  ▆▆▆▆▆▆▆  ███████  ███████  ███████                    │
 │ ███████  ███████  ███████  ███████  ███████                    │
 │ ███████  ███████  ███████  ███████  ███████                    │
 │ ███████  ███████  ███████  ███████  ███████                    │
 │    Mo       Tu       We       Th       Fr       Sa       Su    │
 │   8h00     7h30     9h00     8h15     8h15      ·        ·     │
 ╰──────────────────────────────────────────────── native · full ─╯
 ╭─ [2] Scratch ──────────────────────────────────────────────────╮
 │ A notepad that saves itself                                    │
 ╰──────────────────────────────────────────────── native · tile ─╯
                                       1-9 open · j/k move · J/K reorder · ? help
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

- **Plain text in `Lines`.** Up to 3 short lines (`plugin.MaxSummaryLines`); the host
  truncates lines that don't fit the entry, and strips ANSI codes and control
  characters. No layout, no colour: `Level` (`LevelOk`, `LevelWarn`, `LevelErr`, or
  the default `LevelNone`) puts a dot in the theme's colour on the entry's border.
  A summary with only `Lines` is complete and always works.
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

### Rich summaries: `Widgets`

Set `Summary.Widgets` to show structured data. dock draws it at the entry's width, in
the theme's colours (`Level` colours, `Focus` uses the accent), so a plugin never deals
with ANSI codes, terminal width or a no-colour terminal. Plugins don't learn the width:
the same data is drawn at 60 or 160 columns, and no refetch is needed on resize.

| Constructor | Draws | Rows |
|---|---|---|
| `plugin.Text(text, level)` | one line of text | 1 |
| `plugin.Progress(label, value, max, caption, level)` | `label ████▍░░░░ 51%` (an empty caption shows the percentage) | 1 |
| `plugin.Sparkline(label, level, values...)` | `cpu ▁▂▄█▅`, oldest value first, scaled to the largest value | 1 |
| `plugin.BarChart(height, max, bars...)` | vertical bars `height` rows tall (1–6, default 4), then a label row, then a caption row if any bar has a caption | height + 1 or 2 |
| `plugin.Columns(height, cells...)` | 1–4 side-by-side cells sharing the width: a title row with a count, then `height` note rows (1–4, default 3) | 1 + height |

A `plugin.Bar` has a `Label`, a `Caption` (e.g. its value), a `Value`, a `Level` (colour)
and `Focus` (accent colour and bold label, e.g. for today). `max` is the value of a
full bar (0 means the largest bar); bigger values are clamped, so give a bar past the
target a `LevelWarn` to show it. Bars share the width (up to 14 cells each); heights
have eighth-cell resolution.

A `plugin.Cell` has a `Title` (16 runes), a `Count` (the items it stands for, shown after
the title; at least `len(Lines)`), `Lines` (the notes, most important first, 64 runes each,
plain text) and a `Level` (colours the title and count; the default is the accent).
Cells get equal widths with a 2 cell gap, and notes are cut with `…`. A cell with more
items than rows ends in `+N more`: it shows `height-1` notes, then the rest as a
count. An empty cell shows a muted `—`. Every cell is `height` note rows tall, so
cells in a row line up. A cell needs at least 16 cells of width
(`plugin.MinCellWidth`), otherwise the host falls back to `Lines`. Stack two `Columns`
for a 2x2 matrix (lazytodo's quadrants):

```go
plugin.Columns(2,
    plugin.Cell{Title: "Do now", Count: 3, Level: plugin.LevelWarn, Lines: []string{"! ship the release", "reply to Sam", "water plants"}},
    plugin.Cell{Title: "Schedule", Count: 1, Lines: []string{"plan Q1"}}),
plugin.Columns(2,
    plugin.Cell{Title: "Delegate"},
    plugin.Cell{Title: "Drop", Count: 2, Lines: []string{"tidy desk", "old inbox"}}),
plugin.Text("2 to sort", plugin.LevelNone),
```

```
 ╭─ [1] lazytodo ──────────────────────────────────────────── ● ─╮
 │ Quick todo capture                                            │
 │ Do now 3                      Schedule 1                      │
 │ ! ship the release            plan Q1                         │
 │ +2 more                                                       │
 │ Delegate                      Drop 2                          │
 │ —                             tidy desk                       │
 │                               old inbox                       │
 │ 2 to sort                                                     │
 ╰─────────────────────────────────────────────── native · tile ─╯
```

```go
func (Plugin) Summary(ctx plugin.Context) (plugin.Summary, error) {
    head := fmt.Sprintf("Today %s · this week %s / 40h", today, week)
    return plugin.Summary{
        Lines: []string{head, "Mo█ Tu▆ We▃ Th· Fr· Sa· Su·"}, // the fallback
        Level: plugin.LevelWarn,
        Widgets: []plugin.Widget{
            plugin.Text(head, plugin.LevelNone),
            plugin.BarChart(4, 480, bars...), // 480 = a full 8h bar
        },
    }, nil
}
```

- **`Lines` is the fallback.** Always set it to a text version of the same status. dock
  shows it instead of the widgets when the entry would be too tall for the list
  (the terminal is short), when a widget doesn't fit the width (a bar chart needs
  one cell per bar plus one between bars; a `Columns` cell needs 16), and in the plain-list mode on tiny
  terminals (width under 40, which shows the first line). If `Lines` is empty, the text
  widgets stand in for it.
- **Limits.** All widgets together take at most 8 rows (`plugin.MaxSummaryRows`); a
  widget that doesn't fit the budget is dropped, along with the ones after it. Also
  at most 8 widgets, 31 bars, 512 sparkline values (the newest are kept), 4 cells per
  `Columns` and 4 note rows per cell (extra notes are dropped but still counted), counts up
  to 9999, 64 runes per text, label or note, and 16 per caption, bar label or cell
  title. Order the widgets by importance.
- **Data is validated.** Text is cleaned like `Lines`; NaN, infinities and negative
  numbers count as 0; an unknown widget kind, a chart without bars or a `columns` without cells is skipped.
  `Summary.Clean` does this, and the host applies it for you.
- **Same guarantees.** Timeouts, panics and refresh behave exactly as for plain
  summaries.

**Exec plugins** can have a summary too: a command whose stdout is shown. Set it with
`dock plugin add --summary 'df -h --output=avail,pcent / | tail -1' ...` (run with
`sh -c`) or the config's `"summary": ["prog", "arg"]`. It gets the plugin's usual
environment plus `DOCK_SUMMARY=1`, runs under the same 2 second timeout, and a
non-zero exit shows the first line of stderr. The stdout is either:

- **plain text**: the first 3 lines are shown; or
- **JSON**: output that starts with `{` and parses as one JSON object is the structured
  form, with the same fields as `plugin.Summary`:

```json
{"lines": ["Today 8h15 · this week 41h / 40h"],
 "level": "warn",
 "widgets": [
   {"type": "text", "text": "Today 8h15 · this week 41h / 40h"},
   {"type": "progress", "label": "week", "value": 41, "max": 40, "caption": "41h", "level": "warn"},
   {"type": "sparkline", "label": "cpu", "values": [1, 4, 2, 8]},
   {"type": "barchart", "height": 3, "max": 8,
    "bars": [{"label": "Mo", "value": 8, "caption": "8h"},
             {"label": "Tu", "value": 9, "caption": "9h", "level": "warn", "focus": true}]},
   {"type": "columns", "height": 2,
    "cells": [{"title": "Do now", "count": 3, "level": "warn", "lines": ["ship it", "reply to Sam"]},
              {"title": "Drop"}]}
 ]}
```

`level` is `"none"`, `"ok"`, `"warn"` or `"err"`; unknown names count as none. Output
that starts with `{` but isn't valid JSON is shown as plain text. The same limits and
validation as for native plugins apply. Stdout is read up to 16 KiB.

Home screen keys: `1`–`9` open the plugin with that number, `j`/`k` (or arrows)
move, `enter`/`o` open the selected one, and `J`/`K` (or shift+arrows) move the
selected entry down/up. The order is saved in the config as `home_order`. Numbers follow
the plugin order (default: built-in plugins last), stay fixed while the home screen is
open apart from reordering, and stop at 9. These keys only exist on the home screen: inside a tab, digits go to the plugin
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
      `Lines` as plain text (at most 3), and `Widgets` only on top of them; read-only, safe to call concurrently, under 100 ms,
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
