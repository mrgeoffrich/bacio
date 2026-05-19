# bacio's agent-CLI principles

The compact reference. Read this when planning a feature so the conventions we adopted from [Justin Poehnelt's "Rewrite Your CLI for AI Agents"](https://justin.poehnelt.com/posts/rewrite-your-cli-for-ai-agents/) stay true.

## Six rules new code should honour

### 1. Mutations accept JSON via `--json`
Every command that writes to the database accepts a JSON payload via `--json` (alias `-j`). Three forms: inline string, `-` for stdin, `@path/to.json` for a file. Mutually exclusive with positionals and per-field flags — mixing them is a hard error. Strict decode (`DisallowUnknownFields`); typos are rejected, not silently dropped.

- New mutations define an `Input` struct in `internal/cli/inputs/<group>.go` and add a `--json` branch in the runner that funnels through the same store call as the flag path.
- Required fields fail with a specific message; pointer fields combined with the presence map distinguish "absent" (no change) from "explicit clear".
- Issue keys in JSON must be canonical (`MINI-42`). The bare-number shortcut is for humans on the flag path.

### 2. Schemas are published at runtime
Every `--json` payload has a JSON Schema (draft 2020-12) reflected from the same `inputs.*Input` struct the decoder uses, so schema and parser can't drift. Agents discover shapes via `bacio schema list / show <name> / all` rather than memorising them.

- New mutations register in the `schemaRegistry` in `internal/cli/schema.go` with a hand-curated `examples[0]` showing a realistic call. Don't autogenerate examples — the value is in the realism.
- Schema names are dotted forms of the cobra command path: `bacio issue add` → `issue.add`. The audit log uses a parallel dotted naming (`<entity>.<verb>`) but the verbs are CRUD-flavoured — `bacio issue add` records `issue.create`, `bacio feature edit` records `feature.update`. Schema names and audit op names are two related-but-distinct namespaces.

### 3. Lists are lean by default
JSON output from list-style commands strips heavy fields (issue / feature `description`, doc `content`). Opt-in to inflation with `--with-description` or fetch the full record via `show` / `brief`. Heavy bodies belong on per-row lookups, not on bulk reads.

- `bacio issue brief` is the deliberate exception: it inlines linked-doc bodies in one shot, but offers `--no-feature-docs`, `--no-comments`, `--no-doc-content` so agents can dial bulk back down.
- New list commands follow the lean default. Don't add fields to a list response without checking the size impact.

### 4. Validators run at the store boundary
Every mutation entry-point in `internal/store/` runs the supplied strings through `internal/store/validate.go` before SQL. Single-line fields (titles, names, slugs, filenames, URLs, tags) reject all C0 controls and DEL; multi-line fields (descriptions, bodies, content) keep `\t \n \r` and reject the rest. Length caps are generous-but-finite. UTF-8 validity required.

- New mutations call the existing validators (`ValidateTitle`, `ValidateName`, `ValidateSlug`, `ValidateBody`, `ValidatePRURLStrict`, `ValidateDocFilenameStrict`) — don't reimplement.
- **Don't add silent normalisation.** Leading/trailing whitespace in a filename or slug is a hard error, not a `TrimSpace` away. Agents should see what they sent get rejected, not have it normalised.
- Defence-in-depth: validators in the store layer cover CLI, TUI, and any future programmatic caller, not just one entry point.

### 5. Every mutation has `--dry-run`
The global `--dry-run` flag short-circuits every mutation right after validators and lookups but before the SQL write. Stdout has the same shape as a real call (so the same parsing code works); stderr writes `[dry-run] no changes were written`. No history entry.

- Implementation pattern: `if opts.dryRun { return emitDryRun(projectedEntity) }` immediately before the store mutation. Construct the projected entity in memory.
- Destructive commands (`*.rm`) project a `*DeletePreview` struct that carries the row plus cascade counts (comments, relations, doc links, etc.) so agents can see the blast radius.
- Server-time fields (`id`, `created_at`, `updated_at`) come back as zero values in dry-run output. Everything else is faithful.

### 6. `SKILL.md` is the agent-facing reference
`.claude/skills/bacio/SKILL.md` is the single source of truth for how to drive bacio from an agent. The "Agent quick start" section ties the rules together as discover (schema) → compose (`--json`) → rehearse (`--dry-run`) → execute (with `--user`) → query lean.

- When adding a feature that changes the agent surface, update SKILL.md in the same commit.
- `embed.go` embeds the file at build time and `bacio install-skill` redistributes it. Re-running after a build picks up the latest doc automatically.
- Keep the frontmatter `description` triggering on the right keywords (issues, kanban, tags, blocks, PRs, audit log, project documents) — that's what makes Claude auto-load the skill.

## Implemented principles (post-#1–#6)

- **#7 Multi-surface architecture.** REST API surface shipped (`bacio api` — see the "HTTP API" section of `SKILL.md`, and `docs/site/guides/run-the-api-server.md` for the end-to-end walk-through). Same `inputs.*Input` structs, same `schemaRegistry`, same validators, same audit log; only the transport differs. The MCP server surface also shipped, narrowly: `bacio channel` is an MCP-over-stdio server implementing Claude Code's "channel" contract — but it's a harness-integration shim (see below), not a general-purpose MCP surface over the whole CLI.

## Harness-integration shims are exempt from the six rules

`bacio tui`, `bacio api`, `bacio web`, `bacio hook`, and `bacio channel` are *glue to a host* — a terminal, an HTTP client, the OS default browser, the Claude Code hook/channel runtime — not agent-facing mutation verbs. They deliberately skip rules #1–#5: no `--json` input, no `bacio schema` entry, no `--dry-run`. The host defines their I/O contract (hook-event JSON on stdin, JSON-RPC over stdio, etc.), so re-deriving a bacio JSON schema for them would be noise. They are still documented in `SKILL.md` (rule #6 holds). The persistent `--log-dir` / `--log-level` knobs added in BACI-73 sit in this same carve-out: they're harness-only operational knobs (the long-running processes consume them; short-lived CLI verbs ignore them) and have no `bacio schema` entry by design.

The line: if a human or agent invokes the command *to mutate bacio state*, it follows the six rules (so `bacio agent dispatch` / `bacio agent ack` do — they're agent-facing). If a *host runtime* invokes it as an integration callback, it's a shim and is exempt. When in doubt, ask "who types this command?" — a person/agent, or a runtime.

## What we deliberately don't do

These came up during the redesign and were considered + rejected. New PRs shouldn't reintroduce them without a fresh case for why bacio's tradeoffs have changed.

- **No NDJSON / streaming output.** bacio's largest realistic response (`bacio issue brief`) is one object. Streaming complexity isn't justified.
- **No generic `--field` / projection mask.** Three opt-in flags (`--with-description`, `--metadata`, `--no-doc-content`) cover the actual hot spots.
- **No unicode bidi-override / zero-width filtering.** Defensible for a public service rendering user-supplied text to others; overkill for a single-user local tracker.
- **No HTML / markdown sanitisation.** bacio doesn't render HTML; markdown rendering goes through glamour in the TUI which has its own escape boundary.
- **No nested-transaction dry-run.** "BEGIN; mutate; ROLLBACK" would be cleaner in the abstract, but threading `*sql.Tx` through ~20 store functions isn't worth it. In-memory projection works fine.
- **No autogenerated schema examples.** Reflection won't invent useful examples (`{"title":"string"}` is worse than nothing). Hand-curated examples are short and realistic.
- **No second skill file.** A separate `bacio-agent.md` would split the source of truth from `SKILL.md`.

## Pending principles (#8-#9)

- **#8 Response sanitization.** bacio doesn't currently filter prompt-injection-style content from issue/comment/doc text it returns to agents. Worth revisiting if bacio ingests data from third parties.
- **#9 Incremental implementation.** Already how we're doing it.

When implementing #8, document the design pass before updating this doc — write it up alongside the change so the rules and their rationale stay together.
