# Markdown rendering across surfaces

bacio shows markdown bodies in a lot of places — issue descriptions, comments, linked documents, feature descriptions — across three surfaces (TUI, Wails desktop, web bundle served by `bacio api`). Before BACI-65 these surfaces routed through four different renderers with different feature sets; in particular, **GFM tables rendered in the TUI but silently disappeared in the React tree** because react-markdown was wired without `remark-gfm`.

This doc captures the per-surface audit, the canonical-renderer decision per surface family, and the conventions agents must honour when adding a new markdown surface.

> **Required reading before changing markdown rendering anywhere.**
> Linked from `CLAUDE.md`'s "Required reading" alongside `docs/agent-cli-principles.md` and `docs/tui-cookbook.md`. The grep / refactor cost of cleaning up a duplicated renderer down the line is much higher than reading this once.

## 1. The audit

| Surface | Where | Renderer | GFM tables? |
|---|---|---|---|
| TUI — issue description overlay | `internal/tui/board.go` → `boardView.cachedMD` → `renderMarkdown` | **glamour**, dark style, per-width cache | **Yes** (`extension.GFM` default) |
| TUI — comment overlay | `internal/tui/board_overlays.go` → `cachedCommentMD` | **glamour** | Yes |
| TUI — feature description overlay | `internal/tui/features.go` → `featuresView.cachedMD` | **glamour** | Yes |
| TUI — document content overlay | `internal/tui/docs.go` → `docsView.cachedMD` | **glamour** | Yes |
| Desktop / web — issue description (edit + read) | `desktop/frontend/src/components/issue/InlineDescriptionEditor.jsx` → `editor/NotionEditor.tsx` | **TipTap v3** + `tiptap-markdown` (post BACI-292; was `<MarkdownView>`) | Yes |
| Desktop / web — comment timeline | `desktop/frontend/src/components/IssueWorkspace.jsx` | **`<MarkdownView>`** | Yes |
| Desktop / web — linked-doc panel | `desktop/frontend/src/components/issue/LinkedDocPanel.jsx` (non-SVG branch) | **`<MarkdownView>`** | Yes |
| Desktop / web — features view description | `desktop/frontend/src/components/FeaturesView.jsx` | **`<MarkdownView>`** | Yes |
| Desktop / web — DocsView (read + edit) | `desktop/frontend/src/components/DocsView.jsx` → `editor/NotionEditor.tsx` | **TipTap v3** + `tiptap-markdown` + Table extensions | Yes |
| Desktop / web — comment composer | `issue/CommentComposer.jsx` (textarea) | none | n/a |
| CLI — `bacio issue show` / `comment list` / `doc show` text mode | `internal/cli/output.go` | none — raw bytes | n/a (deliberate; lean structured output) |

## 2. The decision — one canonical renderer per surface family

Two families of surface, two different engines — and that's fine. The contract is **one canonical renderer per family, used everywhere in that family**:

### TUI family — glamour

The TUI is already consistent. `internal/tui/markdown.go` exposes:

- `renderMarkdown(md string, width int) string` — the one entry point. Builds and caches a `glamour.TermRenderer` per width (`mdRenderer`), pinned to `WithStandardStyle("dark")` to avoid the OSC 11 auto-detect race against bubbletea's input reader.
- `mdCache` — a small value type with `get(id, src, width)` / `reset()`. Every TUI view that displays a single focused entity stores one of these inline (`boardView`, `featuresView`, `docsView`). One entry per width, with an `id` check that invalidates stale renders when the selection changes.
- The comment timeline keeps a separate `commentMD map[commentMDKey]string` keyed by `(commentID, width)` because the overlay renders many comments simultaneously rather than a single focused entity.

Adding a new TUI surface that shows markdown? Embed an `mdCache` value, call `cache.get(id, body, width)`, call `cache.reset()` from `reload()`. That's it — don't roll a fifth bespoke cache.

### Desktop / web family — react-markdown + remark-gfm

The React tree splits cleanly: **read everywhere = `<MarkdownView>`; edit-WYSIWYG = TipTap (`NotionEditor`, used by DocsView **and** the issue description since BACI-292); edit-plain = textarea (comment composer)**.

`desktop/frontend/src/lib/markdownView.tsx` is the **only** call-site for `react-markdown` outside DocsView. The wrapper:

- Imports `react-markdown` + `remark-gfm` once.
- Exposes a single `{className?, children}` signature — pass markdown in, get a rendered tree out. The optional `className` is applied to a wrapping `<div>` so the existing `.mk-markdown` styles still anchor correctly.
- Wires `remarkPlugins=[remarkGfm]` — that's the BACI-65 fix. GFM tables, task lists, autolinks and strikethrough render uniformly across every read surface.

react-markdown v10 ships with raw-HTML rendering off by default (no `rehype-raw`), so `<script>` tags in a description render as literal text rather than executing. bacio inputs are local-owner-trusted anyway (see `docs/agent-cli-principles.md`'s "no HTML / markdown sanitisation"), but the layered default is a nice belt-and-braces.

#### Issue descriptions on TipTap — revisited in BACI-292

BACI-65 originally kept issue descriptions off TipTap for three reasons. BACI-292 revisited the call and **moved the issue description onto `NotionEditor`** (the same TipTap engine DocsView uses) so the description gets WYSIWYG markdown editing consistent with the Documents screen. The original three reasons and how they land now:

1. **Round-trip risk (accepted).** `tiptap-markdown` (markdown-it under the hood) silently normalises whitespace, list markers, link titles, and code-fence info strings on every save. Issue bodies are routinely edited by agents (CLI / API) outside the editor, so an agent-authored body edited then saved in the UI will be reformatted. The user explicitly accepted this tradeoff for BACI-292 (a Render/Source raw-markdown toggle was offered and declined). The normalisation only fires on a UI save — bodies nobody touches in the UI are untouched.
2. **Composer scope creep (still deferred).** A WYSIWYG comment composer remains a separate UX project — the comment composer keeps its `<textarea>` (see the follow-up below).
3. **Performance (bounded).** The original concern was `IssueWorkspace` mounting *many* small TipTap instances. BACI-292 mounts exactly **one** TipTap editor per issue (the description) — the comment timeline and linked-doc panels still read through `<MarkdownView>` — so the per-issue cost is one ProseMirror instance, not N. The description editor is always-on (no read/edit toggle); if mount cost ever bites, fall back to a click-to-edit toggle (read = `<MarkdownView>`, edit = `NotionEditor`).

The remaining natural follow-up is replacing the comment composer's `<textarea>` with a slim TipTap-based composer (markdown out, lighter than full DocsView NotionEditor) so writing a table comment doesn't require remembering pipe syntax. The composer change is purely additive — `<MarkdownView>` still does the reading.

## 3. Conventions

When you add a new surface that shows markdown:

- **React tree (desktop / web):** import `<MarkdownView>` from `src/lib/markdownView.tsx`. **Never import `react-markdown` directly** outside that wrapper. A `git grep "from 'react-markdown'" desktop/frontend/src` should always return one line — the wrapper itself.
- **React tree, edit:** if you need a WYSIWYG edit surface, reuse `NotionEditor` (DocsView + the issue description both do) — **don't add a third edit engine**. For a plain edit surface where round-trip normalisation matters (e.g. the comment composer), a `<textarea>` is still fine; weigh the round-trip and performance trade-offs above.
- **TUI:** call `renderMarkdown` (or use the `mdCache` value type for cached scrolling). Don't construct your own `glamour.TermRenderer`.
- **CSS:** style markdown output via `.mk-markdown` selectors in `app.css`. The block already covers headings, paragraphs, lists, code, blockquotes, GFM tables, task-list checkboxes, and strikethrough. If you need a new selector, add it next to the others rather than introducing a parallel class.

## 4. Risks and known costs

- **`remark-gfm` adds ~50 KB minified to the web bundle.** Acceptable for local serve; already inside the chunk-size budget Vite warns about. If we ever need to shave it, an option is lazy-loading `<MarkdownView>` behind a `React.lazy` boundary — the issue workspace is the main consumer and is already gated behind a route.
- **`react-markdown` v10 + `remark-gfm` v4 stay in lockstep** — both maintained by the same group (unified). Pin via `package-lock.json`; verify `npm run build:web` is clean after upgrades.
- **Tables in narrow columns.** `.mk-markdown table` is set `display: block; overflow-x: auto` so a wide table inside the workspace rail scrolls horizontally rather than blowing the layout. In the TUI, glamour's default `WithTableWrap(true)` wraps table cells to fit overlay width.

## 5. Follow-ups (out of scope for BACI-65)

- `rehype-highlight` (or chroma-equivalent) for fenced code in the React tree, matched against the TUI's chroma palette. Currently fenced code renders in a plain `<pre><code>` block on the React side; the TUI styles it via chroma.
- ESLint rule banning direct `react-markdown` imports outside `src/lib/markdownView.tsx`. The doc + the grep guard + the SKILL.md gotcha are the convention enforcement today.
- Optional `bacio issue show --pretty` if humans complain about raw markdown in `bacio issue show` text mode. The CLI text path stays unstyled by default — that's the agent-CLI principle of lean structured output.
- Slim TipTap-based comment composer (see §2 reasoning).
