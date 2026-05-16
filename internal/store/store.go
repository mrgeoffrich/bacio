package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// SQL driver is registered in driver_native.go (modernc/sqlite on
	// the CLI binary) or driver_wasm.go (ncruces/go-sqlite3 in the
	// browser build). Both register under name "sqlite".

	"github.com/mrgeoffrich/bacio/internal/identity"
)

// HistoryRetention bounds how long audit-log entries are kept. Anything
// older is dropped on every Open() — keeps the table from growing without
// bound. Tweak here if a longer or shorter retention window is wanted.
const HistoryRetention = 60 * 24 * time.Hour

//go:embed schema.sql
var schemaSQL string

type Store struct {
	DB *sql.DB
}

// DefaultPath returns ~/.bacio/db.sqlite
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".bacio", "db.sqlite"), nil
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// busy_timeout(5000) is the load-bearing pragma for multi-process
	// access. WAL lets readers and writers coexist, but writers still
	// serialise on the single writer lock — and the default busy_timeout
	// is 0, meaning SQLITE_BUSY returns immediately. With the TUI / the
	// desktop / a hook subprocess / a `bacio channel` MCP server all
	// racing to write at session-start, that returned-immediately error
	// surfaces as silent failures (e.g. EnsureAgentIdentity losing the
	// race and leaving a session identity-less). 5s is generous enough
	// to cover the worst observed contention without hanging the UI.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// History prune runs on every Open. Failures here aren't fatal — the
	// caller's user-visible work shouldn't fail because retention housekeeping
	// hit a snag — so we surface a warning and carry on.
	if err := pruneHistory(db, HistoryRetention); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: history prune failed:", err)
	}
	if err := pruneAgentSessions(db, AgentSessionRetention); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: agent-session prune failed:", err)
	}
	if err := pruneDispatches(db, AgentDispatchRetention); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: dispatch prune failed:", err)
	}
	if err := pruneAgentChannels(db); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: agent-channel prune failed:", err)
	}
	return &Store{DB: db}, nil
}

// OpenMemory opens a transient in-memory database with the bacio schema
// applied. No filesystem access, no WAL — used by the WASM demo so the
// browser can seed and query a real bacio store without touching disk.
//
// The history retention prune is skipped (in-memory store starts empty),
// and migrations run unconditionally since the schema is whatever the
// embed gives us.
func OpenMemory() (*Store, error) {
	// "file::memory:" is SQLite's canonical URI form for an anonymous
	// in-memory database — both modernc and ncruces accept it. The
	// modernc-specific "?_pragma=…" shorthand isn't portable, so we
	// flip foreign_keys on with a follow-up Exec instead.
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: db}, nil
}

// pruneHistory deletes audit-log rows older than retention.
func pruneHistory(db *sql.DB, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`DELETE FROM history WHERE created_at < ?`, cutoff)
	return err
}

// migrate brings older databases up to the current schema. SQLite's ALTER
// TABLE doesn't support IF NOT EXISTS, so we check column presence first.
// Once the schema gets more complex, swap this for a real migration tool.
func migrate(db *sql.DB) error {
	has, err := columnExists(db, "features", "updated_at")
	if err != nil {
		return err
	}
	if !has {
		// SQLite forbids non-constant defaults on ALTER TABLE ADD COLUMN, so
		// add it nullable then backfill from created_at.
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN updated_at DATETIME`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE features SET updated_at = created_at WHERE updated_at IS NULL`); err != nil {
			return err
		}
	}
	// The attachments feature was removed in favour of documents; drop the
	// table from any pre-existing DB. Historical history.attachment.* rows
	// stay put since the audit log is append-only.
	if _, err := db.Exec(`DROP TABLE IF EXISTS attachments`); err != nil {
		return err
	}
	hasSrc, err := columnExists(db, "documents", "source_path")
	if err != nil {
		return err
	}
	if !hasSrc {
		if _, err := db.Exec(`ALTER TABLE documents ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	hasAssignee, err := columnExists(db, "issues", "assignee")
	if err != nil {
		return err
	}
	if !hasAssignee {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN assignee TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee)`); err != nil {
		return err
	}
	hasWaitingForClaim, err := columnExists(db, "issues", "waiting_for_claim")
	if err != nil {
		return err
	}
	if !hasWaitingForClaim {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN waiting_for_claim INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	hasRepoUpdated, err := columnExists(db, "repos", "updated_at")
	if err != nil {
		return err
	}
	if !hasRepoUpdated {
		if _, err := db.Exec(`ALTER TABLE repos ADD COLUMN updated_at DATETIME`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE repos SET updated_at = created_at WHERE updated_at IS NULL`); err != nil {
			return err
		}
	}
	if err := migrateUUIDs(db); err != nil {
		return err
	}
	if err := migrateRepoPathUnique(db); err != nil {
		return err
	}
	// sync_remotes is a Phase-4 addition; CREATE TABLE IF NOT EXISTS is
	// idempotent so this is fine to run on every Open(). Older DBs
	// gain the table here; newer DBs already have it from schema.sql.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sync_remotes (
			remote_url   TEXT NOT NULL PRIMARY KEY,
			local_path   TEXT NOT NULL,
			cloned_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_sync_at DATETIME
		)`); err != nil {
		return fmt.Errorf("create sync_remotes: %w", err)
	}
	// agent_sessions.agent_id was added when the persistent agent-identity
	// layer landed. Older DBs that already created agent_sessions (from
	// the registry's initial v1) gain the column here. The CREATE TABLE
	// for `agents` itself is in schema.sql and idempotent — already there
	// by the time this ALTER runs because Open() applies schema.sql first.
	hasAgentID, err := columnExists(db, "agent_sessions", "agent_id")
	if err != nil {
		return err
	}
	if !hasAgentID {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN agent_id INTEGER REFERENCES agents(id) ON DELETE SET NULL`); err != nil {
			return fmt.Errorf("add agent_id to agent_sessions: %w", err)
		}
	}
	// Index lives here, not in schema.sql: it references agent_id, which the
	// ALTER above only adds to older DBs. schema.sql runs before migrate(),
	// so an index declaration there would fail on those DBs. Idempotent.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_sessions_agent ON agent_sessions(agent_id)`); err != nil {
		return fmt.Errorf("create idx_agent_sessions_agent: %w", err)
	}
	// State-set change: `backlog` and `duplicate` were retired and
	// `needs_action` was added. Existing rows are migrated in place;
	// the new code never writes the dropped states. SQLite's
	// CREATE TABLE IF NOT EXISTS doesn't update CHECK constraints on
	// pre-existing tables, so old DBs keep a looser CHECK — harmless,
	// since ParseState rejects the dropped names at the boundary.
	if _, err := db.Exec(`UPDATE issues SET state = 'todo' WHERE state = 'backlog'`); err != nil {
		return fmt.Errorf("migrate backlog→todo: %w", err)
	}
	if _, err := db.Exec(`UPDATE issues SET state = 'cancelled' WHERE state = 'duplicate'`); err != nil {
		return fmt.Errorf("migrate duplicate→cancelled: %w", err)
	}
	// agent_dispatches.mode was added when plan/implement dispatch intent
	// landed. The ALTER can't carry a CHECK — old DBs that gained mode
	// this way have no CHECK at all, while DBs created fresh in the
	// plan/implement era carry CHECK(mode IN ('','plan','implement')).
	hasDispatchMode, err := columnExists(db, "agent_dispatches", "mode")
	if err != nil {
		return err
	}
	if !hasDispatchMode {
		if _, err := db.Exec(`ALTER TABLE agent_dispatches ADD COLUMN mode TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add mode to agent_dispatches: %w", err)
		}
	}
	// The dispatch-mode set grew (review/ship/fix_review joined
	// plan/implement). Any DB still carrying the old strict CHECK would
	// reject the new modes, so rebuild the table without a mode CHECK —
	// ParseDispatchMode guards the set at the store boundary, and
	// dropping the CHECK means no future stage ever needs a migration.
	if err := migrateAgentDispatchesModeCheck(db); err != nil {
		return err
	}
	// agent_sessions.claude_pid + channel_seen_at landed with the
	// channel-correlation layer. agent_channels itself is created by
	// schema.sql (CREATE TABLE IF NOT EXISTS, applied before migrate()).
	hasClaudePID, err := columnExists(db, "agent_sessions", "claude_pid")
	if err != nil {
		return err
	}
	if !hasClaudePID {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN claude_pid INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add claude_pid to agent_sessions: %w", err)
		}
	}
	hasChannelSeen, err := columnExists(db, "agent_sessions", "channel_seen_at")
	if err != nil {
		return err
	}
	if !hasChannelSeen {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN channel_seen_at DATETIME`); err != nil {
			return fmt.Errorf("add channel_seen_at to agent_sessions: %w", err)
		}
	}
	// agent_claims.prompt records the instruction the agent claimed the
	// issue from. schema.sql creates it on fresh DBs (CREATE TABLE IF NOT
	// EXISTS, applied before migrate()); this ALTER backfills older ones.
	hasClaimPrompt, err := columnExists(db, "agent_claims", "prompt")
	if err != nil {
		return err
	}
	if !hasClaimPrompt {
		if _, err := db.Exec(`ALTER TABLE agent_claims ADD COLUMN prompt TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add prompt to agent_claims: %w", err)
		}
	}
	// agent_sessions.registered_at + channel_version came in with the
	// register-tool lifecycle: the SessionStart hook now creates a
	// minimal stub row, and the bacio channel's register tool enriches
	// it. registered_at distinguishes the two states; channel_version is
	// the binary version reported by the channel the agent talked to.
	// Older sessions that pre-date the lifecycle change are backfilled
	// with registered_at = started_at if they have an agent_id set, so
	// the post-change UI filter doesn't make historical sessions vanish.
	hasRegisteredAt, err := columnExists(db, "agent_sessions", "registered_at")
	if err != nil {
		return err
	}
	if !hasRegisteredAt {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN registered_at DATETIME`); err != nil {
			return fmt.Errorf("add registered_at to agent_sessions: %w", err)
		}
		if _, err := db.Exec(`UPDATE agent_sessions SET registered_at = started_at WHERE agent_id IS NOT NULL`); err != nil {
			return fmt.Errorf("backfill agent_sessions.registered_at: %w", err)
		}
	}
	hasChannelVersion, err := columnExists(db, "agent_sessions", "channel_version")
	if err != nil {
		return err
	}
	if !hasChannelVersion {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN channel_version TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add channel_version to agent_sessions: %w", err)
		}
	}
	// agent_sessions.permission_mode was a hangover from when we mirrored
	// Claude Code's permission mode on the session row — bacio never did
	// anything useful with the value, so it's noise. Drop the column on
	// existing DBs; SQLite supports ALTER TABLE DROP COLUMN since 3.35
	// (modernc/sqlite ships much newer). New DBs from schema.sql never
	// have it.
	hasPermissionMode, err := columnExists(db, "agent_sessions", "permission_mode")
	if err != nil {
		return err
	}
	if hasPermissionMode {
		if _, err := db.Exec(`ALTER TABLE agent_sessions DROP COLUMN permission_mode`); err != nil {
			return fmt.Errorf("drop permission_mode from agent_sessions: %w", err)
		}
	}
	return nil
}

// migrateRepoPathUnique relaxes the column-level UNIQUE on repos.path
// (a hangover from before phantom repos were a thing) and replaces it
// with a partial unique index that ignores empty paths. Phantom repos
// (rows with `path = ''`) represent prefixes that exist in the sync
// repo but have no local working tree on this machine; multiple of
// them must be allowed to coexist.
//
// SQLite can't drop a column-level UNIQUE in place, so we do the
// table-rebuild dance: build repos_new with the relaxed schema, copy
// rows over, drop the old table, rename. The migration is keyed off
// the actual table SQL rather than the presence of the partial index,
// because the schema.sql declaration that re-applies on every Open()
// already creates the index — leaving the column-level UNIQUE in
// force on older DBs unless we explicitly rebuild.
func migrateRepoPathUnique(db *sql.DB) error {
	needs, err := reposPathUniqueNeedsRelax(db)
	if err != nil {
		return err
	}
	if !needs {
		return nil
	}
	// Older DB: column-level UNIQUE is in force. Rebuild the table.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Disable FK enforcement for the duration of the rebuild —
	// otherwise the DROP TABLE step would cascade through every child
	// table that REFERENCES repos(id). The PRAGMA only takes effect
	// outside a transaction in SQLite, so we set it on the connection
	// before BEGIN; here we use the well-known PRAGMA defer_foreign_keys
	// trick which IS scoped to the transaction.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fk: %w", err)
	}

	// Build the new shape with the relaxed (no inline UNIQUE on path)
	// column declaration. Match the column types & defaults of the
	// existing schema exactly so a SELECT * INSERT * round-trips.
	if _, err := tx.Exec(`
		CREATE TABLE repos_new (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid               TEXT    NOT NULL,
			prefix             TEXT    NOT NULL UNIQUE,
			name               TEXT    NOT NULL,
			path               TEXT    NOT NULL,
			remote_url         TEXT    NOT NULL DEFAULT '',
			next_issue_number  INTEGER NOT NULL DEFAULT 1,
			created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create repos_new: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO repos_new
			(id, uuid, prefix, name, path, remote_url, next_issue_number, created_at, updated_at)
		SELECT
			id, uuid, prefix, name, path, remote_url, next_issue_number, created_at, updated_at
		FROM repos
	`); err != nil {
		return fmt.Errorf("copy repos rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE repos`); err != nil {
		return fmt.Errorf("drop old repos: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE repos_new RENAME TO repos`); err != nil {
		return fmt.Errorf("rename repos_new: %w", err)
	}
	// Re-create the uuid uniqueness index (migrateUUIDs created it on
	// the old table, which is now gone) and the new partial path
	// index. The schema.sql declarations are CREATE ... IF NOT EXISTS,
	// so re-applying schema.sql on the next Open() is harmless.
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_repos_uuid ON repos(uuid)`); err != nil {
		return fmt.Errorf("recreate uniq_repos_uuid: %w", err)
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_repos_path ON repos(path) WHERE path != ''`); err != nil {
		return fmt.Errorf("create uniq_repos_path: %w", err)
	}
	return tx.Commit()
}

// migrateAgentDispatchesModeCheck rebuilds agent_dispatches to drop the
// column-level CHECK on `mode`. Early-generation DBs created in the
// plan/implement era carry CHECK (mode IN ('','plan','implement')),
// which now rejects the review/ship/fix_review stages. SQLite can't
// drop a column CHECK in place, so this is the table-rebuild dance —
// the same pattern as migrateRepoPathUnique. Keyed off the stored
// CREATE TABLE SQL: DBs that never had the CHECK (older still, gained
// `mode` via ALTER) and fresh DBs (schema.sql no longer declares it)
// skip the rebuild.
func migrateAgentDispatchesModeCheck(db *sql.DB) error {
	needs, err := agentDispatchesModeCheckPresent(db)
	if err != nil {
		return err
	}
	if !needs {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// defer_foreign_keys is transaction-scoped — it keeps the DROP TABLE
	// from cascading through children that REFERENCE agent_dispatches
	// (none today, but future-proof and consistent with the repos rebuild).
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fk: %w", err)
	}
	// Mirror schema.sql's agent_dispatches shape exactly, minus the
	// CHECK on `mode`, so SELECT *-style copy round-trips.
	if _, err := tx.Exec(`
		CREATE TABLE agent_dispatches_new (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id           INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			target_agent_id   INTEGER REFERENCES agents(id) ON DELETE CASCADE,
			target_session_id TEXT    NOT NULL DEFAULT '',
			issue_id          INTEGER REFERENCES issues(id) ON DELETE SET NULL,
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
		)
	`); err != nil {
		return fmt.Errorf("create agent_dispatches_new: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO agent_dispatches_new
			(id, repo_id, target_agent_id, target_session_id, issue_id, mode,
			 payload, status, created_by, created_at, delivered_at, acked_at, ack_note)
		SELECT
			id, repo_id, target_agent_id, target_session_id, issue_id, mode,
			payload, status, created_by, created_at, delivered_at, acked_at, ack_note
		FROM agent_dispatches
	`); err != nil {
		return fmt.Errorf("copy agent_dispatches rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE agent_dispatches`); err != nil {
		return fmt.Errorf("drop old agent_dispatches: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE agent_dispatches_new RENAME TO agent_dispatches`); err != nil {
		return fmt.Errorf("rename agent_dispatches_new: %w", err)
	}
	// Re-create the indexes the dropped table carried; the schema.sql
	// declarations are CREATE ... IF NOT EXISTS so re-applying is harmless.
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_dispatches_agent ON agent_dispatches(target_agent_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_dispatches_session ON agent_dispatches(target_session_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_dispatches_repo ON agent_dispatches(repo_id, status)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate agent_dispatches index: %w", err)
		}
	}
	return tx.Commit()
}

// agentDispatchesModeCheckPresent reports whether the agent_dispatches
// table still carries a CHECK constraint on `mode` — the strict
// CHECK (mode IN (...)) from the plan/implement-era schema.sql. Looks at
// the stored CREATE TABLE SQL, whitespace-collapsed so reformats don't
// fool the matcher.
func agentDispatchesModeCheckPresent(db *sql.DB) (bool, error) {
	var sqlText sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'agent_dispatches'`).Scan(&sqlText)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !sqlText.Valid {
		return false, nil
	}
	collapsed := strings.Join(strings.Fields(sqlText.String), " ")
	return strings.Contains(collapsed, "CHECK (mode IN"), nil
}

// reposPathUniqueNeedsRelax reports whether the repos table still
// carries the column-level UNIQUE on `path`. It looks at the stored
// CREATE TABLE SQL in sqlite_master rather than at index presence,
// because schema.sql's CREATE INDEX IF NOT EXISTS already runs on
// every Open() and would mask the underlying need to rebuild.
//
// The check is deliberately strict-string: we look for the literal
// `path  TEXT  NOT NULL UNIQUE` (whitespace-tolerant) in the table's
// `sql` column. False on a freshly-created or already-migrated DB
// where the column declaration has lost the inline UNIQUE.
func reposPathUniqueNeedsRelax(db *sql.DB) (bool, error) {
	var sqlText sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'repos'`).Scan(&sqlText)
	if err != nil {
		return false, err
	}
	if !sqlText.Valid {
		return false, nil
	}
	// Collapse whitespace so trivial reformats don't fool the matcher.
	collapsed := strings.Join(strings.Fields(sqlText.String), " ")
	// The old column-level UNIQUE looks like `path TEXT NOT NULL UNIQUE`;
	// the relaxed version drops the trailing UNIQUE.
	return strings.Contains(collapsed, "path TEXT NOT NULL UNIQUE"), nil
}

// migrateUUIDs adds nullable `uuid` columns to issues, features,
// documents, comments, and repos on databases that pre-date them, then
// backfills each row with a freshly-generated UUIDv7. Once every row is
// populated, a unique index is created so the column matches the
// schema.sql declaration on fresh DBs.
//
// SQLite's ALTER TABLE forbids non-constant defaults on ADD COLUMN, so
// the column is added nullable and backfilled in Go (uuid7 is not a
// SQLite built-in). The whole sequence is idempotent: if the column
// already exists (fresh DB), the ALTER is skipped; if no rows are NULL,
// the backfill loop is a no-op; CREATE UNIQUE INDEX IF NOT EXISTS
// papers over re-runs.
func migrateUUIDs(db *sql.DB) error {
	for _, t := range []string{"issues", "features", "documents", "comments", "repos"} {
		has, err := columnExists(db, t, "uuid")
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN uuid TEXT`, t)); err != nil {
				return fmt.Errorf("add uuid to %s: %w", t, err)
			}
		}
		if err := backfillUUIDs(db, t); err != nil {
			return fmt.Errorf("backfill uuid on %s: %w", t, err)
		}
		idx := "uniq_" + t + "_uuid"
		if _, err := db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s(uuid)`, idx, t)); err != nil {
			return fmt.Errorf("index %s: %w", idx, err)
		}
	}
	return nil
}

// backfillUUIDs assigns a fresh UUIDv7 to every row whose uuid is NULL
// (or empty, just in case a partial older migration ran). Row-by-row
// updates are fine for bacio's data sizes (thousands of rows at most).
//
// The whole backfill runs inside a single transaction. A crash midway
// through would otherwise leave rows with NULL uuid; subsequent reads
// (Scan into a string field) would then fail on every command until
// the next migrate() pass completed. With the tx, a partial failure
// rolls back to "all NULL", which is harmless because the next Open()
// retries idempotently.
func backfillUUIDs(db *sql.DB, table string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(fmt.Sprintf(`SELECT id FROM %s WHERE uuid IS NULL OR uuid = ''`, table))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(fmt.Sprintf(`UPDATE %s SET uuid = ? WHERE id = ?`, table))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(identity.New(), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error { return s.DB.Close() }
