---
name: file-issue
description: Use this skill when the user wants to log, file, capture, or open an issue/ticket/bug/todo with a short description. Triggers on phrases like "file an issue", "log this bug", "add a ticket", "create an issue for…", "throw this on the board", "make a card for that". Composes the title, description, tags, and feature attachment from the user's prose; uses `bacio issue add` with `--user <agent-name>`. Prefer this over walking the user through `bacio` flag-by-flag — they want a quick capture.
---

# File an issue

Quick capture: turn a short user request into a well-formed `bacio` issue.

## When to use this

- The user says some variant of *"file an issue"* / *"log this"* / *"add a ticket"* / *"make a card"* / *"throw this on the board"*.
- They've described a bug, feature request, or task in 1–3 sentences.

If they want to plan a whole epic with multiple child tickets, prefer `plan-feature`. If they want to talk through what to work on next, prefer `triage`.

## Procedure

1. **Frame the title.** Short imperative or noun phrase, ≤ ~60 characters. *"Login 500s on Safari with `&` in password"*, not *"Bug: I noticed that …"*. Strip filler.
2. **Body, if any.** Capture reproduction steps, error message, link to a Slack thread, etc. Markdown is fine. If the user only gave you one sentence, skip the body — the title is enough.
3. **Tags.** Add `bug` for bugs, `feature` for feature requests. Add component tags only when obvious from the prose (`backend`, `auth`, `ci`). Don't invent priority tags unless the user signalled urgency.
4. **Feature attachment.** If the user mentions an in-flight feature (*"for the auth rewrite"*, *"part of the deploy refactor"*), look up its slug with `bacio feature list -o json` and pass `feature_slug`. Otherwise leave it unattached.
5. **Compose and run.** Use `--json` for one clean call. Always pass `--user <agent-name>` so the audit log attributes the write.

```bash
bacio issue add --user Claude --json '{
  "title": "Login 500s on Safari with `&` in password",
  "description": "Repro:\n1. Open /login\n2. Enter password containing `&`\n3. Submit → 500\n\nServer log: ...",
  "tags": ["bug", "auth"],
  "feature_slug": "auth-rewrite"
}' -o json
```

6. **Confirm to the user.** *"Filed as `YOUR-12`."* — short. Don't echo the whole payload back at them.

## When to dry-run

`bacio issue add` is rarely destructive (you can `bacio issue rm` it back), so skip `--dry-run` for routine captures. Use it if you're unsure the feature slug exists — the dry-run will fail loudly on a missing slug instead of you finding out post-write.

## Reference

The full bacio surface is in `.claude/skills/bacio/SKILL.md` — that's the source of truth for flag names and JSON shapes. Run `bacio schema show issue.add` if you want to see the full payload schema with examples.
