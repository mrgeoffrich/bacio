---
title: Exit codes
description: What bacio returns to your shell — the simple model, plus the one important edge case for the atomic claim loop.
---

# Exit codes

bacio uses a deliberately minimal scheme — two values, easy to script against.

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | Any error. The human-readable error goes to stderr, prefixed with `bacio: `. With `-o json` the structured error goes to stdout. |

That's the whole contract. There are no per-command exit codes, no signal-based codes, no `2` for usage errors.

## The one edge case

`bacio issue next --feature <slug>` — the atomic claim pattern — emits `{"issue": null}` and **exits 0** when nothing is currently claimable. This is deliberate: callers are expected to poll / retry, not treat "nothing ready" as an error.

If you're driving an agent through a feature in dependency order, the canonical loop is:

```bash
while true; do
  result=$(bacio issue next --feature auth-rewrite --user agent-claude -o json)
  if [ "$(echo "$result" | jq -r .issue)" = "null" ]; then
    sleep 30                    # nothing ready — wait and retry
  else
    # work the claim, then continue
    ...
  fi
done
```

Anything else (validation failure, missing flags, DB lock, network error in remote mode, …) is exit `1`.

## Patterns

### Strict shell mode

```bash
set -euo pipefail
bacio issue add "New bug"              # exit on failure
```

### Branching on success

```bash
if bacio issue show MINI-42 >/dev/null 2>&1; then
  echo "exists"
else
  bacio issue add "Login broken" --json '{"title":"...", "..."}'
fi
```

### Suppressing errors but keeping the code

```bash
bacio issue show MINI-42 2>/dev/null || echo "not found"
```

## See also

- **[JSON payloads](/reference/json-payloads)** — the structured error shape when you pass `-o json`.
- **[`bacio issue`](/reference/cli/issue)** — the `next` / `peek` atomic claim pattern.
