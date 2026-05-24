CREATE TABLE IF NOT EXISTS repos (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid               TEXT    NOT NULL,
    prefix             TEXT    NOT NULL UNIQUE,
    name               TEXT    NOT NULL,
    -- path is empty for "phantom" repos (a prefix that exists in the
    -- sync repo but has no local working tree on this machine yet).
    -- The UNIQUE-on-path guarantee is preserved by uniq_repos_path
    -- below, which is partial — multiple phantoms (path = '') can
    -- coexist while real working trees still get the dedup guarantee.
    path               TEXT    NOT NULL,
    remote_url         TEXT    NOT NULL DEFAULT '',
    next_issue_number  INTEGER NOT NULL DEFAULT 1,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Partial UNIQUE on repos.path: still enforces "one repo per local
-- working tree", but lets phantom repos (path = '') coexist.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_repos_path ON repos(path) WHERE path != '';

CREATE TABLE IF NOT EXISTS features (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid        TEXT    NOT NULL,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    slug        TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    -- archived_at (BACI-68) — same semantics as on issues. The auto-sweep
    -- archives a feature when every child issue is archived (and the
    -- feature had at least one child); manual `bacio feature archive` /
    -- `unarchive` writes or clears it on demand.
    archived_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, slug)
);

-- idx_features_archived_at is created in migrate() so it works on
-- databases that pre-date the archived_at column (BACI-68). Same
-- pattern as idx_issues_assignee below.

CREATE TABLE IF NOT EXISTS issues (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid        TEXT    NOT NULL,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number      INTEGER NOT NULL,
    feature_id  INTEGER REFERENCES features(id) ON DELETE SET NULL,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    state       TEXT    NOT NULL CHECK (state IN
                  ('todo','in_progress','needs_action','in_review','done','cancelled')),
    assignee    TEXT    NOT NULL DEFAULT '',
    waiting_for_claim INTEGER NOT NULL DEFAULT 0,
    -- archived_at (BACI-68) doubles as the boolean "hidden from default
    -- views" flag and the audit timestamp of when the row was hidden.
    -- NULL = visible; non-NULL = archived. The auto-sweep stamps it with
    -- CURRENT_TIMESTAMP for issues older than 4 days in a terminal
    -- state; manual `bacio issue archive` / `unarchive` writes or clears
    -- it on demand. Reopening an archived issue (state -> todo/...) does
    -- NOT auto-unarchive — the user must unarchive explicitly. List read
    -- paths filter `archived_at IS NULL` by default; single-item show /
    -- brief always returns the row regardless. The display toggle
    -- (`display.show_archived`) and the per-call `--include-archived`
    -- flag opt in to seeing archived rows.
    archived_at DATETIME,
    -- terminal_at (BACI-138) is the timestamp the issue most recently
    -- entered a terminal state (`done` or `cancelled`). NULL whenever
    -- the row is in a non-terminal state. Drives the kanban Done /
    -- Cancelled column ordering (newest-first) without joining the
    -- audit log — chosen over a recover-from-history approach because
    -- it survives the 60-day history prune and lets the board sort with
    -- a plain ORDER BY. Stamped by SetIssueState / setIssueStateForRelease
    -- / setIssueStateForClaim whenever state transitions INTO a terminal
    -- state; cleared whenever state transitions OUT. Re-entering a
    -- terminal state re-stamps with CURRENT_TIMESTAMP (a re-close beats
    -- an old close on the board). Insert paths that land directly in a
    -- terminal state seed terminal_at = CURRENT_TIMESTAMP for the same
    -- reason. The pre-BACI-138 sort used `updated_at` as a proxy, which
    -- was wrong because tag / title / description edits bump it without
    -- changing state.
    terminal_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, number)
);

CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state);
CREATE INDEX IF NOT EXISTS idx_issues_feature ON issues(feature_id);
-- idx_issues_archived_at (BACI-68), idx_issues_terminal_at (BACI-138),
-- and idx_issues_assignee are created in migrate() so they work on
-- databases that pre-date their referenced columns. The ALTER ADD
-- COLUMN must run before the index can reference it; schema.sql runs
-- before migrate() so the index can't live here.

CREATE TABLE IF NOT EXISTS comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid       TEXT    NOT NULL,
    issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author     TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    -- BACI-131: eval comments are quick observations a user posts from
    -- the kanban card while watching an agent work an issue. They are
    -- pinned to the in-flight run via (agent_session_id, dispatch_id,
    -- mode) so a future review can correlate the note to the exact
    -- claim/dispatch that was live at write time. eval=0 (default) is a
    -- normal comment with all three context fields empty. dispatch_id is
    -- a nullable FK to agent_dispatches so a comment whose dispatch row
    -- ages out via the 60-day prune sweep keeps its eval flag and its
    -- (session, mode) snapshot — only the FK silently nulls out.
    eval              INTEGER NOT NULL DEFAULT 0 CHECK (eval IN (0,1)),
    agent_session_id  TEXT    NOT NULL DEFAULT '',
    dispatch_id       INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL,
    mode              TEXT    NOT NULL DEFAULT '',
    -- BACI-141: transcript_event_ref pins an eval comment to a specific
    -- event inside a `.jsonl` transcript. NULL = unanchored (existing
    -- behaviour: dispatch-card-level note). Two anchor formats are used
    -- by the per-event composer in the transcript viewer:
    --   `tool_use_id:<id>` for tool-call events (durable across re-renders;
    --   the same id appears on both the assistant tool_use and the matching
    --   user-tool-result events).
    --   `line_index:<n>` fallback for assistant / dispatch / system-reminder
    --   / attachment events that don't carry a tool_use_id. `.jsonl`
    --   transcripts are append-only on disk so line indices are durable.
    transcript_event_ref TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments(issue_id);
-- Partial index for the future eval-only review feed: queries that
-- filter eval comments on one issue (or scan recently-posted eval rows
-- across the repo) hit this directly; the non-eval comment list keeps
-- using the existing idx_comments_issue index.
CREATE INDEX IF NOT EXISTS idx_comments_issue_eval
    ON comments(issue_id, created_at) WHERE eval = 1;

-- feature_comments is the feature-scoped chronological-handoff
-- scratchpad (BACI-124). Same shape as `comments` but parented on
-- `features` instead of `issues`. A dispatched implement-mode worker
-- posts to this surface on close-out so the next worker on a sibling
-- issue in the same feature has the context it needs (files of context,
-- deviations from the plan, work deferred / scoped out) — no per-worker
-- transcript-archaeology required.
CREATE TABLE IF NOT EXISTS feature_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid       TEXT    NOT NULL,
    feature_id INTEGER NOT NULL REFERENCES features(id) ON DELETE CASCADE,
    author     TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_feature_comments_feature ON feature_comments(feature_id);

CREATE TABLE IF NOT EXISTS issue_relations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    from_issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    to_issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    type          TEXT    NOT NULL CHECK (type IN ('blocks','relates_to','duplicate_of')),
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(from_issue_id, to_issue_id, type),
    CHECK (from_issue_id <> to_issue_id)
);

CREATE INDEX IF NOT EXISTS idx_relations_from ON issue_relations(from_issue_id);
CREATE INDEX IF NOT EXISTS idx_relations_to   ON issue_relations(to_issue_id);

CREATE TABLE IF NOT EXISTS issue_pull_requests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    url        TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(issue_id, url)
);

CREATE INDEX IF NOT EXISTS idx_prs_issue ON issue_pull_requests(issue_id);

CREATE TABLE IF NOT EXISTS issue_tags (
    issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    tag      TEXT    NOT NULL,
    PRIMARY KEY (issue_id, tag),
    CHECK (length(tag) > 0)
);

CREATE INDEX IF NOT EXISTS idx_issue_tags_tag ON issue_tags(tag);

CREATE TABLE IF NOT EXISTS documents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid        TEXT    NOT NULL,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    filename    TEXT    NOT NULL,
    type        TEXT    NOT NULL CHECK (type IN
                  ('user_docs','project_in_planning','project_in_progress',
                   'project_complete','vendor_docs','architecture','designs',
                   'testing_plans','plan','transcript','rendered_transcript',
                   'review','session_retro')),
    content     TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL,
    -- source_path is the repo-relative on-disk path the document was last
    -- imported from via `bacio doc add/upsert --from-path`. Empty if the doc
    -- was created with an explicit filename. Used by `bacio doc export --to-path`
    -- to materialise the doc back to its canonical location.
    source_path TEXT    NOT NULL DEFAULT '',
    -- archived_at (BACI-68) — same semantics as on issues. The auto-sweep
    -- archives a document when every linked parent (issue and/or
    -- feature) is archived (and the doc had at least one link); docs
    -- with zero links are NOT orphans and are left alone. Manual
    -- `bacio doc archive` / `unarchive` writes or clears it on demand.
    archived_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, filename)
);

CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(type);
-- idx_documents_archived_at is created in migrate() — same reason as
-- idx_issues_archived_at above (BACI-68).

CREATE TABLE IF NOT EXISTS document_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    issue_id    INTEGER REFERENCES issues(id)   ON DELETE CASCADE,
    feature_id  INTEGER REFERENCES features(id) ON DELETE CASCADE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((issue_id IS NULL) <> (feature_id IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_doc_issue
    ON document_links(document_id, issue_id) WHERE issue_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_doc_feature
    ON document_links(document_id, feature_id) WHERE feature_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doc_links_issue   ON document_links(issue_id);
CREATE INDEX IF NOT EXISTS idx_doc_links_feature ON document_links(feature_id);

-- history is an append-only audit log. It deliberately has no foreign keys
-- so entries survive deletion of the referenced repo/issue/feature etc.
-- All ID/label columns are recorded as snapshots at the time of the op.
CREATE TABLE IF NOT EXISTS history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id      INTEGER,
    repo_prefix  TEXT    NOT NULL DEFAULT '',
    actor        TEXT    NOT NULL,
    op           TEXT    NOT NULL,
    kind         TEXT    NOT NULL DEFAULT '',
    target_id    INTEGER,
    target_label TEXT    NOT NULL DEFAULT '',
    details      TEXT    NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_history_repo_time   ON history(repo_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_history_actor       ON history(actor);
CREATE INDEX IF NOT EXISTS idx_history_op          ON history(op);
CREATE INDEX IF NOT EXISTS idx_history_created_at  ON history(created_at);

-- Per-repo TUI preferences. Generic KV so future toggles (default tab,
-- saved filters, etc.) don't need a schema change each time. Values are
-- application-defined strings; the store layer doesn't introspect them.
CREATE TABLE IF NOT EXISTS tui_settings (
    repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repo_id, key)
);

-- Global (not per-repo) KV store — the sibling of tui_settings for
-- preferences that aren't tied to a single repo. Used today for scalar
-- preferences (board.hide_empty_columns). Same generic-KV rationale as
-- tui_settings. Dispatch prompt templates used to live here keyed
-- `prompt_template.<mode>`; they were promoted to the dedicated
-- prompt_templates table (BACI-31) so users can add / rename / delete
-- arbitrary templates instead of editing a fixed five-stage set.
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- prompt_templates is the user's set of dispatch prompt templates. Each
-- row is a named, deletable, renameable template with its own body and
-- state-gate (the set of issue states it's valid to run from). A small
-- bundled set of "built-in" templates (plan, implement, review, ship,
-- fix_review) is seeded on first run from the embedded
-- prompttemplates/<slug>.txt defaults; after that the user owns every
-- row and can delete a built-in if they want. `is_builtin` is purely
-- informational — it does NOT gate deletion. Use
-- `bacio settings template restore-defaults` (idempotent) to re-seed
-- any deleted built-ins.
--
-- This is a dedicated table rather than a JSON blob in app_settings
-- because it needs atomic per-row updates, clean audit-log targets, and
-- room to grow (a future tag_predicate column for richer gating slots
-- in with one ALTER instead of a blob-shape migration).
--
-- The `allowed_states_json` column stores the state-gate as a JSON array
-- of canonical state strings. Order is preserved verbatim; an empty
-- array means "no state qualifies" — such a template never appears on a
-- per-card action menu and is only reachable via
-- `bacio agent dispatch --mode <slug>`.
CREATE TABLE IF NOT EXISTS prompt_templates (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                TEXT    NOT NULL UNIQUE,
    name                TEXT    NOT NULL,
    body                TEXT    NOT NULL DEFAULT '',
    allowed_states_json TEXT    NOT NULL DEFAULT '[]',
    is_builtin          INTEGER NOT NULL DEFAULT 0,
    -- concurrency_limit caps the number of in-flight (pending+delivered,
    -- excluding bacio-channel setup rows) dispatches per (repo, mode)
    -- the matcher will allow. 0 = unlimited; the seed step sets `ship` to
    -- 1 by default so the BACI-51 ship-it pipeline serialises merges.
    concurrency_limit   INTEGER NOT NULL DEFAULT 0,
    -- action_label (BACI-67) is the imperative form of the template's
    -- display name, used by the dispatch action menus on the kanban
    -- card and the issue workspace shelf so the buttons read as a
    -- call to action ("Plan", "Design", …) instead of a status
    -- description ("Planning", "Designing", …). The activity pill on
    -- a taken card still reads the gerund (lowercased Name). An
    -- empty value means "derive from Name" — DeriveActionLabel runs
    -- on the stored Name as a UI-side fallback. The seed step writes
    -- the imperative form for every built-in slug.
    action_label        TEXT    NOT NULL DEFAULT '',
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- Case-insensitive UNIQUE on `name` — two templates called "Plan" and
-- "plan" would be a UX trap, even though their slugs are distinct.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_prompt_templates_name_ci
    ON prompt_templates(LOWER(name));

-- sync_state tracks records that have participated in a git-backed sync
-- pass. Presence-of-row means "previously synced"; absence means
-- "local-only, never exported". CRUD lands in a later phase; the table
-- exists now so migrate() can add it idempotently to older DBs.
CREATE TABLE IF NOT EXISTS sync_state (
    uuid             TEXT    NOT NULL PRIMARY KEY,
    kind             TEXT    NOT NULL CHECK (kind IN
                       ('issue','feature','document','comment','feature_comment','repo')),
    last_synced_at   DATETIME NOT NULL,
    last_synced_hash TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sync_state_kind ON sync_state(kind);

-- agents is the persistent identity layer above sessions. One row per
-- agent (e.g. "cheerful-otter@claude.shiny"); a session links back to
-- it so audit history and `agent list` can correlate work across the
-- many sessions a single agent racks up over its lifetime. Local only.
-- name is a free-form single-line slug the agent picks; UNIQUE is what
-- catches accidental collisions between two agents that independently
-- generate the same slug, prompting the loser to retry.
CREATE TABLE IF NOT EXISTS agents (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- agent_sessions tracks live AI-agent sessions driving the repo. Local
-- only — never synced. session_id is the external id (e.g.
-- CLAUDE_CODE_SESSION_ID); `ended_at IS NULL` means "still alive".
-- agent_id is the persistent identity (see `agents`); nullable so old
-- sessions registered before the identity layer existed keep working.
--
-- claude_pid is the pid of the `claude` process driving this session,
-- walked up the process tree by the `bacio hook` handlers. It's how a
-- `bacio channel` subprocess (which is never told its session id) is
-- correlated back to a session: the channel records the same claude_pid
-- in agent_channels. channel_seen_at is bumped by the hooks whenever a
-- live agent_channels row matches (host, claude_pid) — so it freshness-
-- decays exactly like last_seen_at once the channel dies. Both stay at
-- the defaults for sessions from before the channel-correlation layer.
CREATE TABLE IF NOT EXISTS agent_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT    NOT NULL UNIQUE,
    repo_id         INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    agent_id        INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    actor           TEXT    NOT NULL,
    model           TEXT    NOT NULL DEFAULT '',
    host            TEXT    NOT NULL DEFAULT '',
    branch          TEXT    NOT NULL DEFAULT '',
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME,
    end_reason      TEXT    NOT NULL DEFAULT '',
    claude_pid      INTEGER NOT NULL DEFAULT 0,
    channel_seen_at DATETIME,
    -- registered_at marks when the agent actually completed registration
    -- via the bacio channel's `register` tool (vs. just being created as
    -- a stub by the SessionStart hook). NULL until register fires. The
    -- TUI/desktop/CLI agent list defaults to filtering registered_at IS
    -- NOT NULL — unregistered stubs (sessions without the bacio channel
    -- loaded, or that never got that far) stay invisible by default.
    registered_at   DATETIME,
    -- channel_version is the bacio binary version reported by the
    -- `bacio channel` MCP server the agent talked to at register time —
    -- useful when multiple bacio processes coexist (TUI + desktop +
    -- per-session channels) and one is running an outdated binary. NULL
    -- until register fires; populated from mcp_version on the tool call.
    channel_version TEXT    NOT NULL DEFAULT ''
);

-- idx_agent_sessions_agent is created in migrate() so it works on databases
-- that pre-date the agent_id column. The ALTER ADD COLUMN must run before the
-- index can reference it, and schema.sql is applied before migrate().

CREATE INDEX IF NOT EXISTS idx_agent_sessions_repo_active
    ON agent_sessions(repo_id, ended_at);

-- agent_claims records which issues an agent is currently focused on.
-- Multiple agents may claim the same issue (pairing/review). Distinct
-- from issues.assignee — claim is "intent", assignee is "ownership".
-- prompt records the instruction/dispatch text the agent was working
-- from when it claimed the issue. Empty for claims made without one
-- (e.g. a bare `bacio agent claim` with no --prompt).
CREATE TABLE IF NOT EXISTS agent_claims (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_pk   INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    issue_id     INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    prompt       TEXT NOT NULL DEFAULT '',
    claimed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    released_at  DATETIME
);

-- Enforce "a session may hold at most one open claim per issue" without
-- making rapid claim/release/claim within the same second collide on a
-- (session_pk, issue_id, claimed_at) UNIQUE — CURRENT_TIMESTAMP is
-- 1-sec granular in SQLite.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_claims_open
    ON agent_claims(session_pk, issue_id) WHERE released_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_claims_active
    ON agent_claims(issue_id, released_at);
CREATE INDEX IF NOT EXISTS idx_agent_claims_by_session
    ON agent_claims(session_pk, released_at);

-- sync_remotes records, per (canonical) remote URL, where each user has
-- their sync repo cloned locally. The remote is the shared truth (also
-- in .bacio/config.yaml of every project that uses this sync repo); the
-- local path is per-machine and lives only in this table.
--
-- last_sync_at is bumped at the end of every successful `bacio sync`.
-- last_sync_error (BACI-89) carries the last run's failure message, or
-- NULL when the last run succeeded — the background sync ticker writes
-- it so the web UI can surface a failed mirror. Both are informational;
-- never used to gate behaviour.
CREATE TABLE IF NOT EXISTS sync_remotes (
    remote_url      TEXT NOT NULL PRIMARY KEY,
    local_path      TEXT NOT NULL,
    cloned_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_sync_at    DATETIME,
    last_sync_error TEXT
);

-- agent_dispatches is the supervisor->agent work queue. A dispatch is a
-- unit of work (an issue to look at, an instruction) aimed at an agent
-- identity and/or one specific session. Local-only — never synced. It's
-- drained by the `bacio hook` SessionStart/UserPromptSubmit handlers
-- (pull delivery) and pushed live by `bacio channel` (push delivery).
-- A dispatch must name a target: an agent identity, a session, or both.
CREATE TABLE IF NOT EXISTS agent_dispatches (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id           INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    target_agent_id   INTEGER REFERENCES agents(id) ON DELETE CASCADE,
    target_session_id TEXT    NOT NULL DEFAULT '',
    issue_id          INTEGER REFERENCES issues(id) ON DELETE SET NULL,
    -- mode is one of the DispatchMode stage names (or '' for untyped).
    -- No SQL CHECK: the set grew (plan/implement + review/ship/fix_review)
    -- and ParseDispatchMode already guards at the store boundary, so a
    -- CHECK here would just be a migration tax on every future stage.
    mode              TEXT    NOT NULL DEFAULT '',
    payload           TEXT    NOT NULL DEFAULT '',
    status            TEXT    NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('queued','pending','delivered','acked','cancelled')),
    created_by        TEXT    NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at      DATETIME,
    acked_at          DATETIME,
    ack_note          TEXT    NOT NULL DEFAULT ''
    -- Target CHECK is intentionally absent: queued (BACI-51) rows leave
    -- target_agent_id NULL and target_session_id '' until the matcher
    -- binds them; the Go-side validator in AddDispatch enforces "queued
    -- or named target" instead.
);

CREATE INDEX IF NOT EXISTS idx_dispatches_agent
    ON agent_dispatches(target_agent_id, status);
CREATE INDEX IF NOT EXISTS idx_dispatches_session
    ON agent_dispatches(target_session_id, status);
CREATE INDEX IF NOT EXISTS idx_dispatches_repo
    ON agent_dispatches(repo_id, status);

-- agent_channels records live `bacio channel` subprocesses. Claude Code
-- never tells a channel its session id (only hooks get that), so a
-- channel can't stamp an agent_sessions row directly. Instead it walks
-- its process tree to the `claude` process and records that claude_pid
-- here, heartbeating last_seen_at every poll tick. The `bacio hook`
-- handlers — which DO know the session id and can walk to the same
-- `claude` pid — join (host, claude_pid) back onto agent_sessions to
-- light up channel_seen_at. Pure liveness state, no historical value:
-- pruneAgentChannels drops rows whose heartbeat went stale, and a
-- recycled claude_pid simply upserts over its predecessor's row.
CREATE TABLE IF NOT EXISTS agent_channels (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id      INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    agent_id     INTEGER REFERENCES agents(id) ON DELETE SET NULL,
    host         TEXT    NOT NULL DEFAULT '',
    claude_pid   INTEGER NOT NULL,
    channel_pid  INTEGER NOT NULL DEFAULT 0,
    started_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(host, claude_pid)
);

CREATE INDEX IF NOT EXISTS idx_agent_channels_repo ON agent_channels(repo_id);

-- agent_session_todos mirrors the agent's task list per session — the
-- in-progress plan a Claude Code session is executing. Local-only — never
-- synced. Cascaded out by the agent_sessions ON DELETE chain, so the
-- existing live-list / 60-day session prune sweeps it too; no separate
-- retention pass.
--
-- Two write paths share this table:
--   * TaskCreate (Claude Code 2.1+): insert a new row at MAX(position)+1
--     with content=subject, status=pending, task_id=<auto-assigned id>.
--   * TaskUpdate (Claude Code 2.1+): update the row keyed by
--     (session_pk, task_id); position is preserved so the agent view's
--     insertion order is stable.
--
-- task_id is the Claude Code-assigned identifier on the Task* tool call
-- (string-typed because Claude Code emits it as a string, e.g. "1",
-- "2", ...; bacio doesn't interpret it). Empty string is reserved for
-- rows written by the legacy whole-snapshot TodoWrite path that
-- predates BACI-60; those rows stay valid but can't be updated
-- in-place. The partial unique index keeps (session, task_id) unique
-- only for non-empty ids.
--
-- issue_key (BACI-62) is the canonical issue key the session was
-- claiming when the row was inserted (resolved from the session's
-- single open claim at hook time; empty string when there were zero
-- or many open claims). Per-(session, issue) UI lookups filter on it
-- so an agent that handles two dispatches back-to-back doesn't show
-- the first job's completed rows on the second job's card; pre-BACI-62
-- rows keep '' and fall into the orphan bucket, which the new
-- lookups deliberately skip.
--
-- dispatch_id (BACI-132) widens the scope key from (session, issue) to
-- (session, issue, dispatch) so two dispatches on the same issue
-- (e.g. plan → implement on BACI-X) get separate task lists on the
-- kanban Tasks pill and the Agents view. NULL means "orphan" (no
-- dispatch resolvable at hook time) or "legacy" (pre-BACI-132 row).
-- Card surfaces filter on a real dispatch id so orphan + legacy rows
-- fall out of those views; the unfiltered list reads still see them.
--
-- session_pk (the int FK) rather than session_id (the external string)
-- matches the rest of the agent_* tables. position is part of the PK
-- so the legacy whole-snapshot semantics still hold; the Task* path
-- assigns the next free position on insert. CHECK on status is a
-- defensive belt over model.ParseTodoStatus / store.ValidateSessionTodos.
CREATE TABLE IF NOT EXISTS agent_session_todos (
    session_pk  INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    content     TEXT    NOT NULL,
    status      TEXT    NOT NULL CHECK (status IN ('pending','in_progress','completed')),
    task_id     TEXT    NOT NULL DEFAULT '',
    issue_key   TEXT    NOT NULL DEFAULT '',
    dispatch_id INTEGER REFERENCES agent_dispatches(id),
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_pk, position)
);

CREATE INDEX IF NOT EXISTS idx_agent_session_todos_session
    ON agent_session_todos(session_pk);
-- The (session_pk, issue_key) index that backs the BACI-62 per-(session,
-- issue) lookups lives in internal/store/store.go::migrate, not here.
-- schema.sql runs before migrate(), so a DB upgrading from the pre-
-- BACI-62 table doesn't have the issue_key column yet; the migration
-- adds the column and the index together. The
-- idx_agent_session_todos_session_issue_dispatch index (BACI-132) lives
-- in migrate() for the same reason — the dispatch_id column is added
-- there for upgrading DBs.

-- agent_session_questions backs the BACI-53 ask_user_question MCP tool:
-- a clarification an agent asked the user via the bacio channel,
-- written by the channel, surfaced by the TUI/desktop/web, and answered
-- or dismissed by the user. The channel parks the MCP reply in memory
-- (request_uuid -> JSON-RPC id) and re-correlates via a poll tick that
-- reads the row's state back. Cascaded out by the agent_sessions
-- ON DELETE chain — no separate retention pass.
--
-- request_uuid is the channel-minted UUIDv7 used for in-memory
-- correlation between the parked JSON-RPC reply and the answered row.
-- issue_key is optional; when present it's the key of the issue the
-- session's open claim was on at ask time (so per-issue surfaces can
-- light up alongside the Agents-view modal).
--
-- payload_json holds the AskUserQuestion-shaped questions array; the
-- shape is validated at the store boundary by ValidateQuestionPayload
-- so a hallucinated payload can never reach the table. answers_json
-- mirrors the AskUserQuestion result shape: a map keyed by question
-- text, with single-select values as strings and multi-select values
-- as []string. NULL while open.
--
-- state lifecycle:
--   open      — the question is in the user's queue. The channel is
--               parked on this row's request_uuid.
--   answered  — the user submitted answers. The channel's next tick
--               will deliver them back to the agent.
--   cancelled — the user dismissed the question. The channel delivers
--               an MCP tool error to the agent.
--   abandoned — the channel restarted (or a fresh channel inherits the
--               session). The parked reply is lost. The agent already
--               restarted with the channel, so no recovery is possible.
CREATE TABLE IF NOT EXISTS agent_session_questions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    session_pk    INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    request_uuid  TEXT    NOT NULL UNIQUE,
    issue_key     TEXT    NOT NULL DEFAULT '',
    payload_json  TEXT    NOT NULL,
    answers_json  TEXT,
    state         TEXT    NOT NULL DEFAULT 'open'
                    CHECK (state IN ('open','answered','cancelled','abandoned')),
    asked_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    answered_at   DATETIME,
    asked_by      TEXT    NOT NULL,
    answered_by   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_asq_session_state
    ON agent_session_questions(session_pk, state);
CREATE INDEX IF NOT EXISTS idx_asq_state
    ON agent_session_questions(state);
-- The (session_pk, task_id) partial unique index lives in
-- internal/store/store.go::migrate, not here. schema.sql runs before
-- migrate(), so a DB upgrading from the pre-BACI-60 table doesn't have
-- the task_id column yet — declaring the index here would fail on it.
-- The migration adds the column and the index together; on fresh DBs
-- migrate() still runs (the index ddl is idempotent) so coverage is
-- the same.

-- ui_leader is a single-row lease table. Only one UI process (TUI or desktop
-- app) holds the lease at a time; all others stand by. The CHECK (id = 1)
-- constraint + INSERT OR IGNORE seed guarantee exactly one row forever.
-- heartbeat_at is seeded to 0 (integer) so the first ACQUIRE's staleness
-- test (heartbeat_at < datetime('now','-180 seconds')) is immediately true
-- on a fresh database.
CREATE TABLE IF NOT EXISTS ui_leader (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    holder_token TEXT    NOT NULL DEFAULT '',
    holder_label TEXT    NOT NULL DEFAULT '',
    acquired_at  DATETIME,
    heartbeat_at DATETIME
);
INSERT OR IGNORE INTO ui_leader (id, heartbeat_at) VALUES (1, 0);
