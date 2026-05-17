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
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, slug)
);

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
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, number)
);

CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state);
CREATE INDEX IF NOT EXISTS idx_issues_feature ON issues(feature_id);
-- idx_issues_assignee is created in migrate() so it works on databases that
-- pre-date the assignee column. The ALTER ADD COLUMN must run before the
-- index can reference it.

CREATE TABLE IF NOT EXISTS comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid       TEXT    NOT NULL,
    issue_id   INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author     TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments(issue_id);

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
                   'testing_plans')),
    content     TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL,
    -- source_path is the repo-relative on-disk path the document was last
    -- imported from via `bacio doc add/upsert --from-path`. Empty if the doc
    -- was created with an explicit filename. Used by `bacio doc export --to-path`
    -- to materialise the doc back to its canonical location.
    source_path TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, filename)
);

CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(type);

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
                       ('issue','feature','document','comment','repo')),
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
-- It's purely informational today — useful for `bacio status`-style
-- summaries; never used to gate behaviour.
CREATE TABLE IF NOT EXISTS sync_remotes (
    remote_url   TEXT NOT NULL PRIMARY KEY,
    local_path   TEXT NOT NULL,
    cloned_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_sync_at DATETIME
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
                        CHECK (status IN ('pending','delivered','acked','cancelled')),
    created_by        TEXT    NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at      DATETIME,
    acked_at          DATETIME,
    ack_note          TEXT    NOT NULL DEFAULT '',
    CHECK (target_agent_id IS NOT NULL OR target_session_id != '')
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

-- agent_session_todos mirrors the agent's TodoWrite tool list per session
-- — the in-progress plan a Claude Code session is executing. The list is
-- replaced wholesale on every TodoWrite event (atomic per-session swap
-- inside a single transaction so a partial write never shows a
-- frankenlist). Local-only — never synced. Cascaded out by the
-- agent_sessions ON DELETE chain, so the existing live-list / 60-day
-- session prune sweeps it too; no separate retention pass.
--
-- session_pk (the int FK) rather than session_id (the external string)
-- matches the rest of the agent_* tables (agent_claims.session_pk,
-- agent_channels' (host, claude_pid) join key resolved server-side).
-- position is part of the PK because the list is replaced wholesale and
-- stable ordering is the only thing that matters; per-row identity buys
-- nothing. The CHECK on status is a defensive belt over
-- model.ParseTodoStatus / store.ValidateSessionTodos, which both run
-- before the row reaches the DB.
CREATE TABLE IF NOT EXISTS agent_session_todos (
    session_pk  INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    content     TEXT    NOT NULL,
    status      TEXT    NOT NULL CHECK (status IN ('pending','in_progress','completed')),
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_pk, position)
);

CREATE INDEX IF NOT EXISTS idx_agent_session_todos_session
    ON agent_session_todos(session_pk);

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
