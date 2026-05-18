# TUI Cookbook

Synthesised from bubbletea v1.3.10 (pinned via `replace ... => ./third_party/bubbletea` in `go.mod`) + lipgloss v1.1.1-pre (`v1.1.1-0.20250404203927-76690c660834`) + bubbles v0.21.0 (the last bubbletea-v1-compatible release), fetched 2026-05-03. Refresh by re-fetching the README and godoc URLs cited in the source list at the bottom.

This is a cookbook for the three Charm libraries we use in `bacio`. It assumes Go fluency and skips installation, marketing, and unrelated Charm tooling.

---

## 1. Mental model (Elm architecture in 5 lines)

- **Model** — your state, usually a struct.
- **Init() Cmd** — one-shot command to fire on startup (e.g. load data, start a tick).
- **Update(msg tea.Msg) (tea.Model, tea.Cmd)** — pure-ish: take a message, return new model + optional command.
- **View() string** — pure: render the model to a string.
- **Cmd** — `func() tea.Msg`. Bubble Tea runs it on a goroutine; whatever it returns gets fed back into `Update`. That is the *only* way I/O re-enters the model.

Messages flow: runtime → `Update(msg)` → returned `Cmd` runs async → its `Msg` flows back into `Update`. `View` is called after every `Update` that returns. Don't perform I/O in `Update` or `View`; wrap it in a `Cmd`.

---

## 2. Bubble Tea cookbook

### Pin: we are on v1.3.10

Imports use `github.com/charmbracelet/bubbletea` (NOT `charm.land/bubbletea/v2`). Some web examples are v2; the API differences that bite:

| v1 (us)                | v2 (skip)             |
|------------------------|-----------------------|
| `tea.KeyMsg`           | `tea.KeyPressMsg`     |
| `View() string`        | `View() tea.View`     |
| `Model.Update` returns `(tea.Model, tea.Cmd)` | same |

### Value vs pointer Model receivers

The `tea.Model` interface is satisfied by either. A value receiver is the idiomatic Elm style — `Update` returns a *new* model. A pointer receiver lets you mutate in place and just `return m, nil`. Both work; the rules are:

- If `Update` is on a value receiver, you must reassign fields and `return m, nil` — the runtime keeps the value you return, not the one you mutated.
- If `Update` is on a pointer receiver, the runtime stores the pointer once and reuses it. Mutating fields is fine.
- Don't mix: pick one per Model. Mini-kanban uses a `*Model` (pointer) at the shell, with sub-views holding their own state through pointer-receiver `Update` methods.

### Minimal program

```go
package main

import (
    "fmt"
    "os"
    tea "github.com/charmbracelet/bubbletea"
)

type model struct{ count int }

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "q", "ctrl+c":
            return m, tea.Quit
        case "+":
            m.count++
        }
    }
    return m, nil
}

func (m model) View() string { return fmt.Sprintf("count: %d (q to quit)", m.count) }

func main() {
    if _, err := tea.NewProgram(model{}).Run(); err != nil {
        fmt.Fprintln(os.Stderr, err); os.Exit(1)
    }
}
```

### Alt-screen + mouse + signal handling

```go
p := tea.NewProgram(initial,
    tea.WithAltScreen(),         // full-screen, restored on exit
    tea.WithMouseCellMotion(),   // click + drag (cheap)
    // tea.WithMouseAllMotion(), // also hover (expensive)
)
```

Other useful options: `WithContext(ctx)`, `WithFPS(60)`, `WithoutSignalHandler()` (you handle SIGINT/SIGTERM yourself), `WithFilter(func(Model, Msg) Msg)` to short-circuit messages globally.

### Window sizing — the canonical pattern

The runtime sends a `tea.WindowSizeMsg` once on startup and on every resize. Cache width/height on the model and pass them to children.

```go
type Model struct{ w, h int }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.w, m.h = msg.Width, msg.Height
        // Forward to children that care:
        m.list.SetSize(msg.Width, msg.Height-2)
    }
    return m, nil
}
```

### Key bindings — pragmatic style

For a small fixed key set, switch on `msg.String()`. For shared/discoverable bindings, use `github.com/charmbracelet/bubbles/key`:

```go
import "github.com/charmbracelet/bubbles/key"

var keys = struct{ Quit, Up, Down key.Binding }{
    Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
    Up:   key.NewBinding(key.WithKeys("up", "k"),     key.WithHelp("↑/k", "up")),
    Down: key.NewBinding(key.WithKeys("down", "j"),   key.WithHelp("↓/j", "down")),
}

case tea.KeyMsg:
    switch {
    case key.Matches(msg, keys.Quit): return m, tea.Quit
    case key.Matches(msg, keys.Up):   m.cursor--
    case key.Matches(msg, keys.Down): m.cursor++
    }
```

`msg.String()` produces e.g. `"a"`, `"shift+tab"`, `"ctrl+c"`, `"alt+enter"`, `"esc"`, `"up"`, `"pgdown"`. `msg.Type` gives a constant (`tea.KeyEnter`, `tea.KeyEsc`, etc).

### Built-in messages you'll handle

- `tea.KeyMsg` — keyboard.
- `tea.MouseMsg` — when mouse motion is enabled. `msg.Type` is `tea.MouseLeft`, `tea.MouseWheelUp`, etc; `msg.X`, `msg.Y` are 0-indexed cell coords.
- `tea.WindowSizeMsg` — resize.
- `tea.QuitMsg` — sent right before exit; rarely useful to intercept.
- `tea.FocusMsg` / `tea.BlurMsg` — terminal gained/lost focus (need `WithReportFocus()`).

### Dispatching commands

A `Cmd` is `func() tea.Msg`. Anything async is a Cmd:

```go
type itemsLoadedMsg struct{ items []Item; err error }

func loadItems(s *store.Store) tea.Cmd {
    return func() tea.Msg {
        items, err := s.List()
        return itemsLoadedMsg{items, err}
    }
}

func (m model) Init() tea.Cmd { return loadItems(m.store) }

case itemsLoadedMsg:
    if msg.err != nil { m.err = msg.err; return m, nil }
    m.items = msg.items
```

### Custom messages from outside

`Program.Send(msg)` injects a message from any goroutine — useful for file watchers, network listeners, etc. Hold a `*tea.Program` reference (return it from your bootstrap and stash it):

```go
p := tea.NewProgram(initial)
go func() {
    for ev := range watcher.Events {
        p.Send(fileChangedMsg{path: ev.Name})
    }
}()
p.Run()
```

### Batching and sequencing

```go
return m, tea.Batch(loadItems(m.store), tea.SetWindowTitle("bacio"))   // concurrent
return m, tea.Sequence(saveCmd, reloadCmd)                          // ordered
```

`Batch` is *concurrent* — order of resulting messages is not guaranteed. Use `Sequence` when one must complete before the next begins.

### Tickers

```go
type tickMsg time.Time

func tick() tea.Cmd {
    return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

case tickMsg:
    m.elapsed++
    return m, tick()   // re-arm
```

`tea.Tick` fires once. `tea.Every` fires repeatedly on a wall-clock cadence (good for clocks, bad for animations because it doesn't compensate for jitter).

### Leader-gated periodic work

For periodic side effects that must only run on the controlling UI (the
one currently holding the `ui_leader` lease — see CLAUDE.md's *UI leader
election* section), drive a separate `tea.Tick` and consult the shell's
cached leader state inside the handler. The ticker keeps running on
standby so a standby → leader promotion picks up the next tick on
schedule; only the side effect is gated. This is the pattern the
live-list `agent_sessions` prune uses
(`internal/tui/tui.go`'s `pruneTickMsg`):

```go
type pruneTickMsg time.Time

func pruneTick() tea.Cmd {
    return tea.Tick(store.UILeaderPruneInterval, func(t time.Time) tea.Msg {
        return pruneTickMsg(t)
    })
}

// Init (only when the elector is non-nil — the WASM demo has none):
cmds = append(cmds, pruneTick())

// Update:
case pruneTickMsg:
    if m.elector != nil && m.leaderState.AmLeader {
        if _, err := m.store.PruneEndedAgentSessions(
            store.AgentSessionLiveListRetention); err != nil {
            log.Printf("bacio: live agent-session prune failed: %v", err)
        }
    }
    if m.elector == nil {
        return m, nil
    }
    return m, pruneTick() // re-arm regardless of leader state
```

Two non-obvious points: re-arm `pruneTick()` even when standby (otherwise
a flip to leader is silent until the next *un*armed tick that never
fires), and skip re-arming entirely when `m.elector == nil` (the WASM
demo has no leader concept, and an unbounded ticker would just heat the
event loop). Errors go through `log.Printf` — `tea.LogToFile` is already
configured so they land in the bacio debug log rather than corrupting
the alt-screen.

### Quitting

`return m, tea.Quit` is the normal path. `Program.Kill()` aborts immediately and skips alt-screen restore — last resort.

### Logging

`stdout` is the TUI; print there and you'll corrupt the screen. Use `tea.LogToFile`:

```go
if path := os.Getenv("BACIO_LOG"); path != "" {
    f, _ := tea.LogToFile(path, "bacio")
    defer f.Close()
}
log.Printf("loaded %d items", len(items))   // now safe
```

---

## 3. Lipgloss cookbook

### Style construction

Styles are *value types*. Every modifier returns a new style; assignment copies. You can build them as package-level vars without worrying about aliasing.

```go
var titleStyle = lipgloss.NewStyle().
    Bold(true).
    Foreground(lipgloss.Color("231")).   // bright white
    Background(lipgloss.Color("57")).    // purple
    Padding(0, 1)

fmt.Println(titleStyle.Render("Hello"))
```

### Colour reference

`lipgloss.Color("…")` accepts:

- ANSI 16: `"0"`–`"15"`. `"1"` red, `"2"` green, `"4"` blue, `"5"` magenta, `"6"` cyan, `"7"` light grey, `"8"` dark grey, `"9"`–`"15"` bright variants.
- ANSI 256: `"16"`–`"255"`. Useful pickings:
  - `"57"` rich purple (bacio title bg)
  - `"63"` indigo
  - `"86"` aqua
  - `"196"` strong red (bacio error)
  - `"201"` hot pink
  - `"205"` rose
  - `"214"` orange
  - `"220"` warm yellow
  - `"231"` bright white (bacio title fg)
  - `"238"`/`"241"`/`"244"` greys (bacio separators/footer/inactive tab)
- TrueColor: `"#7D56F4"`. Downsamples automatically on poor terminals.

For terminals where the user might be on light or dark, use adaptive:

```go
var fg = lipgloss.AdaptiveColor{Light: "236", Dark: "248"}
```

### Padding, margin, border (CSS shorthand)

```go
.Padding(2)             // all sides
.Padding(1, 4)          // top/bottom, left/right
.Padding(1, 2, 0, 2)    // top, right, bottom, left
.Margin(1)              // same shorthand
```

```go
.Border(lipgloss.RoundedBorder()).
    BorderForeground(lipgloss.Color("63"))
.Border(lipgloss.NormalBorder(), true, false)   // top+bottom only
```

Border presets: `NormalBorder`, `RoundedBorder`, `ThickBorder`, `DoubleBorder`, `HiddenBorder`. `HiddenBorder` reserves the cells without drawing — handy for keeping layouts stable.

### Width, height, alignment, inline

```go
.Width(40).Height(10).Align(lipgloss.Center)
.MaxWidth(40)                      // truncates without padding
.Inline(true)                      // strip newlines, ignore margins/padding
```

`Align` takes `lipgloss.Left | Center | Right`. `Position` constants are floats (`Top=0.0`, `Center=0.5`, `Bottom=1.0`) — anywhere a position is expected you can pass a literal like `0.2`.

### Composition

```go
left  := boxStyle.Render("LEFT")
right := boxStyle.Render("RIGHT")
row   := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
page  := lipgloss.JoinVertical(lipgloss.Left, header, row, footer)
```

`Join*` aligns *across* the join axis: `JoinHorizontal(lipgloss.Top, …)` aligns the tops of differently-tall blocks. `JoinVertical(lipgloss.Center, …)` centres differently-wide blocks horizontally.

### Place — fill a region

```go
lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
```

Use this to centre a card on the alt-screen, or to bottom-stick a status line.

### Measuring

```go
w := lipgloss.Width(rendered)    // visible cells, ignores ANSI
h := lipgloss.Height(rendered)
```

ALWAYS measure with these, never `len()`. `len()` counts bytes including escape sequences.

### Inheritance and copying

```go
base := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
emph := base.Bold(true)         // base unchanged; emph has both rules
plain := emph.UnsetBold()       // unset specific rule
```

`Inherit(otherStyle)` copies *only the unset* rules from other. Useful for theme bases.

### The classic gotcha: nested backgrounds

Lipgloss does not "merge" or "punch through" backgrounds. If you render an inner style with no background inside a block that has a background, the inner cells get the *terminal's* default background, not the parent's. Three options:

1. Set the parent background explicitly on every nested style (tedious).
2. Use `Inherit(parent)` so the inner picks up the parent's background.
3. Render the inner *first*, measure it, then paint the parent around it with `Place` instead of nesting.

Related: padding/margin themselves get the *style's* background, so a padded inner-style without a background will still leave a "hole" in the parent.

---

## 4. Bubbles components

All bubbles components implement the same shape: `New(...)` returns a `Model`, which has `Update(msg) (Model, tea.Cmd)` and `View() string`. The parent forwards messages and embeds `View()` in its render.

Imports are `github.com/charmbracelet/bubbles/<component>`.

### textinput

```go
import "github.com/charmbracelet/bubbles/textinput"

type model struct{ ti textinput.Model }

func newModel() model {
    ti := textinput.New()
    ti.Placeholder = "title"
    ti.Prompt = "› "
    ti.CharLimit = 120
    ti.Width = 40
    ti.Focus()
    return model{ti: ti}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
        // m.ti.Value() has the input
        return m, tea.Quit
    }
    var cmd tea.Cmd
    m.ti, cmd = m.ti.Update(msg)
    return m, cmd
}

func (m model) View() string { return m.ti.View() }
```

Notable fields: `Placeholder`, `Prompt`, `PromptStyle`, `TextStyle`, `PlaceholderStyle`, `CharLimit`, `Width`, `EchoMode` (`EchoNormal`, `EchoPassword`, `EchoNone`), `Validate func(string) error`. Methods: `Focus()`, `Blur()`, `Focused()`, `Value()`, `SetValue(s)`, `Reset()`.

### textarea

```go
import "github.com/charmbracelet/bubbles/textarea"

ta := textarea.New()
ta.Placeholder = "Description…"
ta.SetWidth(60)
ta.SetHeight(8)
ta.Focus()
// ta.Value(), ta.SetValue(s), ta.Blur(), ta.Reset()
```

Update/View identical to textinput. Use `key.NewBinding` to disambiguate `enter`-as-newline from `enter`-as-submit at the parent level (the textarea always inserts a newline on enter).

**In use:** the Settings tab (`internal/tui/settings.go`) edits dispatch
prompt-template bodies with `textarea`. Two gotchas it had to handle:

- The textarea binds `ctrl+d` to delete-forward. The Settings editor uses
  `ctrl+d` for reset-to-default, so it **intercepts `ctrl+d` (and
  `ctrl+s`, `tab`, `esc`) before forwarding the message** to
  `ta.Update` — otherwise the reset key would eat a character.
- `textarea` sanitises control runes on input, so a value typed into it
  is always control-char clean. Saving still goes through the store's
  `ValidateBody`, which can reject on the 1 MiB length cap — surface
  that error in the overlay, don't crash.

### The `CapturesInput()` shell contract

A view that embeds a `textarea`/`textinput` MUST implement
`CapturesInput() bool` to return `true` while that input is focused.
The shell (`tui.go`) consults it on every key: while it's true, `q` and
the digit keys `1`–`9` are routed to the view (typed literally) instead
of being treated as quit / tab-switch. `ctrl+c` is never yielded — it is
always a hard quit. Views with no text input return `false`
unconditionally. This is the documented pattern for any future
text-editing view (e.g. the long-mooted "edit description" overlay).

### viewport

```go
import "github.com/charmbracelet/bubbles/viewport"

vp := viewport.New(width, height-2)
vp.SetContent(longString)
vp.Style = lipgloss.NewStyle().Padding(0, 1)
// vp.GotoTop(); vp.GotoBottom(); vp.AtBottom(); vp.ScrollPercent()
```

In Update, forward the message *and* re-set content if it changed:

```go
case tea.WindowSizeMsg:
    m.vp.Width, m.vp.Height = msg.Width, msg.Height-2
default:
    var cmd tea.Cmd
    m.vp, cmd = m.vp.Update(msg)
    return m, cmd
```

Default keys: `↑/↓/k/j` line, `pgup/pgdown` page, `home/end` jump. Override via `vp.KeyMap`.

### list

```go
import "github.com/charmbracelet/bubbles/list"

type item struct{ title, desc string }
func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }   // required

items := []list.Item{item{"Buy carrots", "from the market"}, item{"Pay rent", ""}}
l := list.New(items, list.NewDefaultDelegate(), width, height)
l.Title = "Today"
l.SetShowStatusBar(false)
l.SetFilteringEnabled(true)
// later:
sel, _ := l.SelectedItem().(item)
l.SetSize(w, h)               // call from WindowSizeMsg
m.l, cmd = m.l.Update(msg)    // standard forwarding
```

The `list.Item` interface only requires `FilterValue() string`. `list.NewDefaultDelegate()` expects items also implementing `Title()` and `Description()`. To customise rendering, write your own `list.ItemDelegate` (Render/Height/Spacing/Update). Style entry points: `l.Styles.Title`, `l.Styles.StatusBar`, plus the delegate's own `Styles` (for `NewDefaultDelegate`).

### table

```go
import "github.com/charmbracelet/bubbles/table"

cols := []table.Column{
    {Title: "Key",   Width: 8},
    {Title: "Title", Width: 40},
    {Title: "State", Width: 10},
}
rows := []table.Row{
    {"MK-1", "Initial board layout", "doing"},
    {"MK-2", "History tab",          "todo"},
}
t := table.New(
    table.WithColumns(cols),
    table.WithRows(rows),
    table.WithHeight(12),
    table.WithFocused(true),
)
s := table.DefaultStyles()
s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("57"))
s.Selected = s.Selected.Foreground(lipgloss.Color("231")).Background(lipgloss.Color("57"))
t.SetStyles(s)

// later: row := t.SelectedRow()  // Row is []string
// m.t, cmd = m.t.Update(msg)
```

Row navigation needs the table to be focused (`t.Focus()` / `t.Blur()`). `t.SetRows(rows)` resets rows; `t.SetCursor(i)` jumps.

### Forwarding pattern (general)

```go
var cmds []tea.Cmd
var c tea.Cmd
m.list, c = m.list.Update(msg);  cmds = append(cmds, c)
m.input, c = m.input.Update(msg); cmds = append(cmds, c)
return m, tea.Batch(cmds...)
```

Forward to the *focused* component only when keystrokes shouldn't reach the others — typical when an input is focused and you don't want list nav firing.

---

## 5. Patterns we use in this repo

Map upstream concepts to bacio code:

- `internal/tui/tui.go` — the shell. Owns the alt-screen `tea.Program`, holds tabs (`Board`, `Features`, `Documents`, `History`), tracks `WindowSizeMsg`, dispatches top-level keys (quit, tab switch, digit jump). Defines the `view` interface every tab implements: `Init() tea.Cmd`, `Update(msg) tea.Cmd`, `View(w, h) string`, `Help() string`, `HasOverlay() bool`, `CloseOverlay()`, `Status() string`, `Breadcrumb() string`. When a view returns `true` from `HasOverlay()`, the shell switches into overlay routing: `ctrl+c` and `q` still quit unconditionally, digit shortcuts still switch tabs (closing the overlay on the way out), but `esc`, `tab`/`shift+tab`, and everything else are routed to the view so its overlay handles them.
- `internal/tui/board.go` — the kanban tab. Self-contained `view`. Holds columns keyed by `model.State`, a per-column row cursor, and an optional fullscreen card overlay. Loads comments lazily through a Cmd when a card is selected.
- `internal/tui/docs.go` — documents tab; same `view` shape.
- `internal/tui/history.go` — audit log tab; same shape.
- `internal/tui/styles.go` — shared lipgloss styles. Single source of truth for the palette (purple `"57"` accent, `"231"` foreground, `"196"` errors, `"238"`/`"241"`/`"244"` grey ramp). Edit here to retheme.
- `internal/tui/helpers.go` — small render utilities (truncation, key/value rows) used by multiple views.

The shell is a `*Model` (pointer receiver) so the tabs can mutate through their handles. Each tab is also a pointer-receiver type so its `Update` can mutate its own fields directly; the shell just calls the view's `Update` and bubbles the returned `Cmd`.

`bubbles` is a dependency as of the Settings tab — `bubbles/textarea`
powers the dispatch prompt-template body editor (`internal/tui/settings.go`).
It is pinned to **v0.21.0**, the last release in the bubbletea-v1 line
(newer `bubbles` targets bubbletea v2 and won't compile against the
pinned v1.3.10). Everything else — board cells, the doc viewer — is
still hand-rendered with lipgloss. If we add a fuzzy issue picker, use
`bubbles/list`.

---

## Runes vs cells (Unicode width)

A *rune* is one Unicode code point; a *cell* is one column of terminal grid. ASCII is 1:1, but emoji and CJK are 1 rune / 2 cells, combining marks are N runes / 1 cell, and ZWJ sequences (e.g. 👨‍👩‍👧) collapse many runes into one glyph. `internal/tui/helpers.go`'s `truncate` measures *runes* — fine for ASCII filenames and keys, but if a user-supplied string (issue title, comment body, document name) contains emoji, the truncated line will be shorter cell-wise than expected. lipgloss's own `Width()` is cell-aware (via go-runewidth) and pads correctly, so the symptom is usually a small visual gap, not overflow. Swap `truncate` to `runewidth.Truncate` if a panel ever shows visible emoji-driven misalignment.

---

## TUI snapshots (visual debugging)

`bacio tui --snapshot <target>` renders one TUI view to stdout non-interactively, so you can inspect layout bugs without a real terminal. Targets: `board`, `features`, `docs`, `history`, `card-overlay`, `doc-overlay`, `feature-overlay`, `picker`. Use `--width`/`--height` to fix the viewport and `--issue MINI-1` to focus a specific card before opening `card-overlay` (or to position the cursor on `board`). Implementation is in `internal/tui/snapshot.go`; reproducing a visual bug is usually one command, e.g. `bacio tui --snapshot card-overlay --issue MINI-1 --width 140 --height 50 > /tmp/x.ansi`.

---

## Markdown rendering

Every TUI view that shows markdown — board's description overlay, comment overlay, features description, docs body — routes through `internal/tui/markdown.go`. Two pieces of plumbing:

- `renderMarkdown(md, width)` builds and caches a `glamour.TermRenderer` per terminal width. Pinned to `glamour.WithStandardStyle("dark")` — auto-detect issues an OSC 11 background-colour query against stdin, which races bubbletea's input reader in alt-screen mode and can stall the first render after a resize. The TUI is dark-only by design, so the pin is intentional.
- `mdCache` is a small value type with `get(id, src, width)` / `reset()` that every view embeds inline (board, features, docs). One entry per width, with an `id` check that invalidates stale renders when the selection changes. The comment timeline keeps a separate map keyed by `(commentID, width)` because the overlay renders many comments simultaneously rather than a single focused entity.

Glamour ships `extension.GFM` enabled by default, so tables, task lists, autolinks and strikethrough render without further configuration. `TestRenderMarkdownTable` in `markdown_test.go` guards the "tables render" promise against future upstream changes. Full per-surface story (including the React tree's `<MarkdownView>` counterpart) lives in [`docs/markdown-rendering.md`](markdown-rendering.md) — read it before adding a fifth bespoke cache.

---

## Sources

- https://raw.githubusercontent.com/charmbracelet/bubbletea/main/README.md
- https://pkg.go.dev/github.com/charmbracelet/bubbletea
- https://github.com/charmbracelet/bubbletea/blob/main/examples/simple/main.go
- https://github.com/charmbracelet/bubbletea/blob/main/examples/views/main.go
- https://github.com/charmbracelet/bubbletea/blob/main/examples/window-size/main.go
- https://raw.githubusercontent.com/charmbracelet/lipgloss/master/README.md
- https://pkg.go.dev/github.com/charmbracelet/lipgloss
- https://raw.githubusercontent.com/charmbracelet/bubbles/master/README.md
- https://pkg.go.dev/github.com/charmbracelet/bubbles
- https://raw.githubusercontent.com/charmbracelet/bubbles/master/textinput/textinput.go
- https://raw.githubusercontent.com/charmbracelet/bubbles/master/list/list.go
- https://raw.githubusercontent.com/charmbracelet/bubbles/master/viewport/viewport.go
- https://raw.githubusercontent.com/charmbracelet/bubbles/master/table/table.go
