---
title: bacio schema
description: Discover the JSON payload shape for every mutating command at runtime — the agent contract.
---

# `bacio schema`

bacio publishes a JSON Schema (draft 2020-12) for every `--json` payload. The schemas are reflected from the same `inputs.*Input` structs the decoder uses, so schema and parser cannot drift. Agents discover payload shapes through this command rather than memorising them.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio schema list` | List every available schema name. |
| `bacio schema show <name>` | Show one schema with a hand-curated example. |
| `bacio schema all` | Dump every schema in one document. |

Names are dotted forms of the command path: `bacio issue add` → `issue.add`, `bacio feature edit` → `feature.edit`.

## Worked examples

```bash
# Browse what's available
bacio schema list

# Pull the schema + example for one command
bacio schema show issue.add

# Just the example, pipe straight into a real call
bacio schema show issue.add | jq .examples[0]

# Compose, rehearse, commit
bacio schema show issue.add | jq .examples[0] \
  | bacio issue add --dry-run --json -
bacio schema show issue.add | jq .examples[0] \
  | bacio issue add --json -

# One-shot ingestion of everything (useful for an agent's first run in a repo)
bacio schema all
```

## How the schemas are produced

Reflected at runtime from the `inputs.*Input` Go structs in `internal/cli/inputs/`. The decoder and the schema generator share one source, so they can't drift — if a field exists in the schema, the decoder accepts it; if it doesn't, the decoder rejects it.

Examples are **hand-curated**, not reflected — they live alongside the registration in `internal/cli/schema.go` so they show realistic field combinations, not the generic `{"title":"string"}` reflection would produce.

## Using schemas in external tooling

Each schema is valid JSON Schema draft 2020-12. Pair with `ajv`, `jsonschema` (Python), or any other validator to lint payloads before sending them:

```bash
bacio schema show issue.add > /tmp/issue-add.schema.json
ajv validate -s /tmp/issue-add.schema.json -d /tmp/payload.json
```

## See also

- **[JSON payloads](/reference/json-payloads)** — the contract from the human side.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — discover → compose → rehearse → execute.
