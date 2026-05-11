---
name: plan-feature
description: Use this skill when the user wants to plan, break down, scope, or scaffold a new feature/epic into child issues with dependencies. Triggers on phrases like "plan the X feature", "break this down into tasks", "scaffold an epic for X", "let's plan out X", "design the work for X". Creates the feature, child issues with sensible titles/tags, blocks/blocked-by relations, and optionally a linked design document. Always presents the proposed plan before writing; mutates only on user approval.
---

# Plan a feature

Take a rough goal and produce a feature with child issues, dependency edges, and (optionally) a linked design doc.

## When to use this

- The user says *"plan the X feature"*, *"break X down into tasks"*, *"scaffold an epic for …"*, *"design the work for …"*, *"let's plan out X"*.
- The work is bigger than one issue. For single-issue capture, use `file-issue`. For grooming an existing backlog, use `triage`.

## Procedure

1. **Clarify scope, briefly.** If the goal is vague, ask 1–2 sharp questions:
   - *"Is this 'replace the JWT lib' or 'rewrite the entire auth flow'?"*
   - *"Are we shipping behind a flag or hard-cutting over?"*
   Don't drown the user in questions — two is the limit. If they answered enough to draft, draft.

2. **Draft the feature.** Title is human-readable (*"Auth rewrite"*); slug is kebab-case derived from the title (`auth-rewrite`).

3. **Draft 3–8 child issues.** Each has:
   - A specific, actionable title (verbs: *"Add"*, *"Migrate"*, *"Wire up"*, *"Backfill"*).
   - A 1–3 sentence description if non-obvious.
   - Tags where they're informative (`backend`, `migration`, `tests`).
   - Implicit ordering: which issues block which.

4. **Draft dependency edges.** Identify pairs *A blocks B* where B can't start until A lands. Don't over-constrain — only model real blockers, not "logical order". A false blocker means an issue sits artificially blocked.

5. **Optional: a design doc.** If the user gave you enough detail, draft a short markdown document (architecture decisions, key trade-offs, open questions) and plan to link it to the feature.

6. **Present the plan.** Show:
   - Feature name + slug.
   - Issue list as a numbered outline, with blockers noted (*"3. depends on #2"*).
   - Whether you'll create a design doc and what it'll cover.

7. **Wait for approval.** Apply only after the user says *"do it"* / *"go"* / *"yes"*. If they want changes, redraft and re-present — don't half-apply.

8. **Apply, in this order.** All with `--user <agent-name>`:
   ```bash
   # 1. Feature.
   bacio feature add "Auth rewrite" --slug auth-rewrite \
     --description-file /tmp/auth-overview.md --user Claude

   # 2. Child issues — capture the keys returned by each call.
   bacio issue add --user Claude --json '{
     "title":"Add new JWT lib",
     "feature_slug":"auth-rewrite",
     "tags":["backend"]
   }' -o json
   # ... repeat for each ...

   # 3. Optional design doc.
   bacio doc add auth-overview.md --type architecture \
     --content-file /tmp/auth-overview.md --user Claude
   bacio doc link auth-overview.md auth-rewrite \
     --why "Source of truth for the rewrite" --user Claude

   # 4. Dependency edges, after all keys are known.
   bacio link YOUR-12 blocks YOUR-13 --user Claude
   bacio link YOUR-13 blocks YOUR-14 --user Claude
   ```

9. **Confirm.** *"Created feature `auth-rewrite` with 5 issues. Run `bacio feature plan auth-rewrite` to see the dependency-ordered execution plan."*

## On dry-run

For step 8, dry-run isn't worth it — the operations are creates, and `bacio feature add` / `bacio issue add` already fail loudly on duplicate slugs or bad inputs. Save dry-run for cleanup if you ever need to retract a botched plan with `bacio feature rm --dry-run`.

## After the plan exists

The user — or another agent — can drive the feature in dependency order:

```bash
bacio feature plan auth-rewrite                        # human-readable plan
bacio issue next --feature auth-rewrite --user Claude  # atomically claim the next ready issue
```

`bacio issue next` returns `{"issue": null}` when nothing is currently claimable (everything's done, blocked, or already assigned) — callers should poll, not error.

## Reference

Full bacio surface (relations, doc linking, feature plan, issue next): `.claude/skills/bacio/SKILL.md`.
