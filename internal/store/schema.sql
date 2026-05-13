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

-- agent_sessions tracks live AI-agent sessions driving the repo. Local
-- only — never synced. session_id is the external id (e.g.
-- CLAUDE_CODE_SESSION_ID); `ended_at IS NULL` means "still alive".
CREATE TABLE IF NOT EXISTS agent_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT    NOT NULL UNIQUE,
    repo_id         INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    actor           TEXT    NOT NULL,
    model           TEXT    NOT NULL DEFAULT '',
    permission_mode TEXT    NOT NULL DEFAULT '',
    host            TEXT    NOT NULL DEFAULT '',
    branch          TEXT    NOT NULL DEFAULT '',
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at        DATETIME,
    end_reason      TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_repo_active
    ON agent_sessions(repo_id, ended_at);

-- agent_claims records which issues an agent is currently focused on.
-- Multiple agents may claim the same issue (pairing/review). Distinct
-- from issues.assignee — claim is "intent", assignee is "ownership".
CREATE TABLE IF NOT EXISTS agent_claims (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_pk   INTEGER NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    issue_id     INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    claimed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    released_at  DATETIME,
    UNIQUE (session_pk, issue_id, claimed_at)
);

CREATE INDEX IF NOT EXISTS idx_agent_claims_active
    ON agent_claims(issue_id, released_at);

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
