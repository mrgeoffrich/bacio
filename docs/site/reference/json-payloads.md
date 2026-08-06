---
title: JSON payloads
description: The agent contract — JSON in via --json, schemas reachable at runtime via bacio schema.
---

# JSON payloads

Every mutating bacio command accepts a JSON payload via `--json` (alias `-j`). Three forms:

```bash
bacio issue add --json '{"title": "Login 500 on Safari", "tags": ["bug"]}'
bacio issue add --json -                                            # stdin
bacio issue add --json @payload.json                                # file
```

Mutually exclusive with positional args and per-field flags — mixing them is a hard error.

## Discover schemas at runtime

The bacio binary itself is the source of truth for payload shapes. Don't memorise them — ask:

```bash
bacio schema list                # every schema name
bacio schema show issue.add      # one schema with a hand-curated example
bacio schema all                 # everything in one document
```

Schema names mirror command paths: `bacio issue add` → `issue.add`, `bacio feature edit` → `feature.edit`.

## Conventions

- **Strict decode.** Unknown fields are rejected, not silently dropped — typos are errors, not lossy.
- **Canonical issue keys.** JSON payloads must use `MINI-42`, not the bare number. The bare-number shortcut is for humans on the flag path only.
- **No silent normalisation.** Whitespace in filenames or slugs is a hard error, not a `TrimSpace` away. What you send is what gets validated.
- **`""` can be a value, not a blank.** On the move verbs — `doc.folder.mv` (`to`), `doc.mv` (`folder`), `kanban.move` (`column`) — the empty string names a real destination: the [document tree root](/concepts/document-folders), or [off the Kanban board](/concepts/kanban-and-pipeline). Those decoders therefore check the key's **presence**, so omitting it is an error rather than an implicit re-root or an accidental sweep off the board. On the flag path, `--to-root` / `--off-board` are the explicit spellings.
- **`--dry-run` everywhere.** Every mutation supports `--dry-run`. Stdout has the same shape as a real call (so the same parser works); stderr writes `[dry-run] no changes were written`. No audit log entry.

## See also

- **[`bacio schema`](/reference/cli/schema)** — discover schemas at runtime.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the six design rules behind this contract.
