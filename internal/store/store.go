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
	"github.com/mrgeoffrich/bacio/internal/model"
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
			remote_url      TEXT NOT NULL PRIMARY KEY,
			local_path      TEXT NOT NULL,
			cloned_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_sync_at    DATETIME,
			last_sync_error TEXT
		)`); err != nil {
		return fmt.Errorf("create sync_remotes: %w", err)
	}
	// sync_remotes.last_sync_error is a BACI-89 addition — the
	// background sync ticker records the last run's failure here so
	// the web UI badge can surface it. Older DBs that already created
	// sync_remotes gain the column with this idempotent ALTER.
	hasSyncErr, err := columnExists(db, "sync_remotes", "last_sync_error")
	if err != nil {
		return err
	}
	if !hasSyncErr {
		if _, err := db.Exec(`ALTER TABLE sync_remotes ADD COLUMN last_sync_error TEXT`); err != nil {
			return fmt.Errorf("add last_sync_error to sync_remotes: %w", err)
		}
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
	// BACI-31: dispatch prompt templates moved from app_settings (KV
	// rows keyed prompt_template.<slug> / prompt_states.<slug>) into a
	// dedicated prompt_templates table. The CREATE TABLE itself lives in
	// schema.sql and is applied before migrate(); this step seeds the
	// five built-in rows on first run and folds any app_settings
	// overrides the user already had into the new table.
	if err := migratePromptTemplates(db); err != nil {
		return fmt.Errorf("migrate prompt templates: %w", err)
	}
	// BACI-51: per-template concurrency limit. The CREATE TABLE in
	// schema.sql carries the column for fresh DBs; this ALTER + seed
	// brings older DBs up to date and stamps `ship` to 1 so the
	// matcher serialises ship-it dispatches by default. Only the
	// initial ALTER seeds — a user who already tweaked the value (the
	// column is present) is left alone.
	hasConcurrencyLimit, err := columnExists(db, "prompt_templates", "concurrency_limit")
	if err != nil {
		return err
	}
	if !hasConcurrencyLimit {
		if _, err := db.Exec(`ALTER TABLE prompt_templates ADD COLUMN concurrency_limit INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add concurrency_limit to prompt_templates: %w", err)
		}
		if _, err := db.Exec(
			`UPDATE prompt_templates SET concurrency_limit = ? WHERE slug = ?`,
			model.BuiltinTemplateShipConcurrency, model.BuiltinTemplateShip,
		); err != nil {
			return fmt.Errorf("seed ship.concurrency_limit: %w", err)
		}
	}
	// BACI-67: per-template action_label override. The CREATE TABLE in
	// schema.sql carries the column for fresh DBs; this ALTER + seed
	// brings older DBs up to date and stamps the imperative form for
	// every built-in slug whose action_label is still empty (a user
	// who has already customised the row is left alone). Only the
	// initial ALTER seeds — once the column is present, this branch is
	// skipped on every subsequent Open.
	hasActionLabel, err := columnExists(db, "prompt_templates", "action_label")
	if err != nil {
		return err
	}
	if !hasActionLabel {
		if _, err := db.Exec(`ALTER TABLE prompt_templates ADD COLUMN action_label TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add action_label to prompt_templates: %w", err)
		}
		for _, slug := range model.BuiltinTemplateSlugs() {
			lbl := model.BuiltinTemplateActionLabel(slug)
			if lbl == "" {
				// Reserved slugs (e.g. _dispatch_preamble) have no
				// imperative override — they never reach a dropdown.
				continue
			}
			if _, err := db.Exec(
				`UPDATE prompt_templates SET action_label = ? WHERE slug = ? AND action_label = ''`,
				lbl, slug,
			); err != nil {
				return fmt.Errorf("seed action_label for %s: %w", slug, err)
			}
		}
	}
	// BACI-51: relax the agent_dispatches.status CHECK to include
	// 'queued' and drop the (target_agent_id NOT NULL OR ...) row
	// CHECK so queued rows can leave both targets unset until the
	// matcher binds them. Same table-rewrite dance as the mode-check
	// relax above; keyed off the stored CREATE TABLE SQL so re-runs
	// are no-ops.
	if err := migrateAgentDispatchesStatusCheck(db); err != nil {
		return err
	}
	// BACI-60: agent_session_todos.task_id keys rows by the Claude Code
	// Task* tool's task identifier so TaskUpdate can find a previously
	// inserted TaskCreate row and flip its status in place. Older DBs
	// from the TodoWrite-era keep their existing rows (task_id ''),
	// which is fine — the legacy whole-snapshot path is gone, so those
	// rows just age out with the session. The partial unique index has
	// to be created here (not in schema.sql) because the column may
	// have just been added by the ALTER.
	hasTaskID, err := columnExists(db, "agent_session_todos", "task_id")
	if err != nil {
		return err
	}
	if !hasTaskID {
		if _, err := db.Exec(`ALTER TABLE agent_session_todos ADD COLUMN task_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add task_id to agent_session_todos: %w", err)
		}
	}
	if _, err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_session_todos_task
			ON agent_session_todos(session_pk, task_id) WHERE task_id != ''`,
	); err != nil {
		return fmt.Errorf("create uq_agent_session_todos_task: %w", err)
	}
	// BACI-62: agent_session_todos.issue_key scopes each mirrored row to
	// the issue the session was claiming at insert time, so the Agents
	// view's `n/m` badge and drill-down can render only the current job's
	// rows instead of every row the session has ever written. Pre-BACI-62
	// rows keep the empty-string default ('') — they fall into the orphan
	// bucket the new per-(session, issue) lookups ignore, which is the
	// desired behaviour (the user's previous-job history stays queryable
	// but stops bleeding into the foreground UI).
	hasTodoIssueKey, err := columnExists(db, "agent_session_todos", "issue_key")
	if err != nil {
		return err
	}
	if !hasTodoIssueKey {
		if _, err := db.Exec(`ALTER TABLE agent_session_todos ADD COLUMN issue_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add issue_key to agent_session_todos: %w", err)
		}
	}
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_agent_session_todos_session_issue
			ON agent_session_todos(session_pk, issue_key)`,
	); err != nil {
		return fmt.Errorf("create idx_agent_session_todos_session_issue: %w", err)
	}
	// BACI-52: backfill the _dispatch_preamble row for existing users
	// (the first-time seed step in migratePromptTemplates is gated on
	// the table being empty, so DBs that already had per-mode templates
	// won't pick it up otherwise). Idempotent: INSERT OR IGNORE keys off
	// the UNIQUE slug, so re-running on an upgraded DB is a no-op, and a
	// deliberate `bacio settings template rm _dispatch_preamble` won't
	// be undone on the next Open (the delete also drops the row this
	// step would reinsert — but a `restore-defaults` is the documented
	// recovery path, identical to the other built-ins).
	if err := backfillDispatchPreamble(db); err != nil {
		return fmt.Errorf("backfill dispatch preamble: %w", err)
	}
	// BACI-76: refresh the stored _dispatch_preamble body from the old
	// (spawn `general-purpose` + paste brief) default to the new (spawn
	// the per-mode subagent + tiny stub) default — but only when the
	// user never customised it. Must run after backfillDispatchPreamble
	// so the row exists.
	if err := refreshDispatchPreamble(db); err != nil {
		return fmt.Errorf("refresh dispatch preamble: %w", err)
	}
	// BACI-128: refresh the per-mode dispatch templates (and the
	// dispatch preamble's `attach_transcript` example) when the
	// stored body byte-matches the pre-BACI-128 default. Adds the
	// `Pass issue_id: <issue_id>` instruction to each per-mode
	// template's Questions paragraph and renames the
	// `attach_transcript` example arg in the preamble. Must run
	// after refreshDispatchPreamble (the BACI-128 preamble pre-frozen
	// body is the post-XML-stub default — running this after means
	// we never byte-compare against a row we just replaced).
	if err := refreshAskUserQuestionTemplates(db); err != nil {
		return fmt.Errorf("refresh ask_user_question templates: %w", err)
	}
	// BACI-68: add archived_at columns + indexes to issues, features,
	// documents on older DBs. The CREATE TABLE declarations in
	// schema.sql carry the column for fresh DBs; this ALTER + index
	// pair brings older DBs up to date. NULL = visible, non-NULL =
	// archived (the auto-sweep stamps CURRENT_TIMESTAMP; manual archive
	// / unarchive verbs read or write it). Index lives here (not in
	// schema.sql) for the same reason as the other late-bound indexes:
	// schema.sql runs before migrate() and would error on a DB without
	// the column.
	for _, table := range []string{"issues", "features", "documents"} {
		has, err := columnExists(db, table, "archived_at")
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN archived_at DATETIME`, table)); err != nil {
				return fmt.Errorf("add archived_at to %s: %w", table, err)
			}
		}
		idx := fmt.Sprintf("idx_%s_archived_at", table)
		if _, err := db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(archived_at)`, idx, table)); err != nil {
			return fmt.Errorf("create %s: %w", idx, err)
		}
	}
	// BACI-115: the documents.type CHECK was widened to admit four new
	// values (plan, transcript, rendered_transcript, review). A DB
	// carrying the pre-BACI-115 narrow CHECK would reject inserts of
	// those new types with a constraint failure. SQLite can't widen a
	// column CHECK in place, so this is the table-rebuild dance — the
	// same pattern as migrateAgentDispatchesModeCheck. Keyed off the
	// stored CREATE TABLE SQL: a DB whose CHECK already lists `plan`
	// (fresh schema.sql or a prior run of this migration) is a no-op.
	if err := migrateDocumentsTypeCheck(db); err != nil {
		return err
	}
	if err := migrateSyncStateKindCheck(db); err != nil {
		return err
	}
	return nil
}

// backfillDispatchPreamble inserts the _dispatch_preamble row if it's
// not already present. Used by the migration to bring older DBs (where
// the first-time seed step has already run with only the per-mode
// built-ins) up to date with the BACI-52 wrapper.
func backfillDispatchPreamble(db *sql.DB) error {
	slug := model.BuiltinTemplatePreamble
	body := model.DefaultPromptBodyForBuiltinSlug(slug)
	name := model.BuiltinTemplateLabel(slug)
	if name == "" {
		name = slug
	}
	// allowed_states_json = '[]' (no state-gate) — keeps the row out of
	// the per-card dispatch picker (availableDispatchModes filters by
	// state-gate match). action_label = '' (preamble never reaches the
	// dropdown either, so the imperative form is irrelevant).
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO prompt_templates
		  (slug, name, body, allowed_states_json, is_builtin, concurrency_limit, action_label, created_at, updated_at)
		VALUES (?, ?, ?, '[]', 1, 0, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		slug, name, body); err != nil {
		return err
	}
	return nil
}

// migratePromptTemplates handles the one-shot migration from the
// pre-BACI-31 world (templates as app_settings KV rows) to the
// dedicated prompt_templates table. The migration is gated on the
// presence of `prompt_template.*` keys in app_settings AND the table
// being empty:
//
//   - On a brand-new DB (table empty, no app_settings keys), it seeds
//     the bundled built-in templates from the embedded defaults.
//   - On a pre-BACI-31 DB (table empty, app_settings keys present), it
//     seeds the built-ins and folds the user's overrides into them.
//   - On a post-BACI-31 DB (table non-empty), it does nothing — so a
//     user delete is not undone on the next Open. Restore via
//     `RestoreBuiltinPromptTemplates` or the `restore-defaults` verb.
//
// The migrated app_settings rows are removed at the end of the fold
// step so a future Open never re-applies them.
func migratePromptTemplates(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Bail unless the table is empty — once the user owns the rows,
	// later Opens must not re-seed deleted templates. RestoreBuiltins
	// is the deliberate-user-action recovery path.
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM prompt_templates`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return tx.Commit()
	}

	// Seed every built-in from the embedded defaults.
	for _, slug := range model.BuiltinTemplateSlugs() {
		body := model.DefaultPromptBodyForBuiltinSlug(slug)
		states := model.DefaultPromptStatesForBuiltinSlug(slug)
		encoded, err := encodeStates(states)
		if err != nil {
			return err
		}
		name := model.BuiltinTemplateLabel(slug)
		if name == "" {
			name = slug
		}
		if _, err := tx.Exec(`
			INSERT INTO prompt_templates (slug, name, body, allowed_states_json, is_builtin, concurrency_limit, action_label, created_at, updated_at)
			VALUES (?, ?, ?, ?, 1, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			slug, name, body, encoded, model.DefaultConcurrencyLimit(slug), model.BuiltinTemplateActionLabel(slug)); err != nil {
			return err
		}
	}

	// Fold any pre-BACI-31 app_settings overrides into the freshly
	// seeded rows, then drop the migrated KV rows.
	for _, slug := range model.BuiltinTemplateSlugs() {
		bodyKey := "prompt_template." + slug
		statesKey := "prompt_states." + slug

		var (
			bodyVal   sql.NullString
			statesVal sql.NullString
		)
		if err := tx.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, bodyKey).Scan(&bodyVal); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, statesKey).Scan(&statesVal); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !bodyVal.Valid && !statesVal.Valid {
			continue
		}

		// Translate the comma-separated state list (the old format) to
		// the new JSON-array shape.
		if statesVal.Valid && strings.TrimSpace(statesVal.String) != "" {
			parts := strings.Split(statesVal.String, ",")
			states := make([]model.State, 0, len(parts))
			for _, p := range parts {
				st, err := model.ParseState(p)
				if err != nil {
					continue
				}
				states = append(states, st)
			}
			encoded, err := encodeStates(states)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE prompt_templates
				   SET allowed_states_json = ?, updated_at = CURRENT_TIMESTAMP
				 WHERE slug = ?`, encoded, slug); err != nil {
				return err
			}
		}
		if bodyVal.Valid {
			if _, err := tx.Exec(`
				UPDATE prompt_templates
				   SET body = ?, updated_at = CURRENT_TIMESTAMP
				 WHERE slug = ?`, bodyVal.String, slug); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM app_settings WHERE key IN (?, ?)`, bodyKey, statesKey); err != nil {
			return err
		}
	}

	return tx.Commit()
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

// migrateAgentDispatchesStatusCheck rebuilds agent_dispatches with the
// BACI-51 status-CHECK (adds 'queued') and without the trailing target
// CHECK (queued rows leave both target_agent_id and target_session_id
// unset until the matcher binds them; AddDispatch's Go-side validator
// enforces "queued OR named target" instead). Same table-rebuild dance
// as migrateAgentDispatchesModeCheck. Keyed off the stored CREATE
// TABLE SQL: a DB whose status CHECK already mentions 'queued' (fresh
// schema.sql or a prior run of this migration) skips the rebuild.
func migrateAgentDispatchesStatusCheck(db *sql.DB) error {
	needs, err := agentDispatchesStatusCheckNeedsRelax(db)
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
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fk: %w", err)
	}
	// Mirror the post-BACI-51 schema.sql shape exactly: relaxed status
	// CHECK, no target CHECK. Everything else matches the existing
	// columns so a SELECT * INSERT * copy round-trips.
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
			                    CHECK (status IN ('queued','pending','delivered','acked','cancelled')),
			created_by        TEXT    NOT NULL,
			created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			delivered_at      DATETIME,
			acked_at          DATETIME,
			ack_note          TEXT    NOT NULL DEFAULT ''
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

// agentDispatchesStatusCheckNeedsRelax reports whether the
// agent_dispatches table still carries the pre-BACI-51 status CHECK
// (the 4-status form without 'queued'). Whitespace-collapsed lookup on
// the stored CREATE TABLE SQL so trivial reformatting doesn't fool it.
func agentDispatchesStatusCheckNeedsRelax(db *sql.DB) (bool, error) {
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
	// True iff the table predates BACI-51 — its status CHECK omits
	// 'queued'. The fresh schema.sql lists 'queued' first, so the
	// presence of that literal in the stored SQL means we're done.
	return !strings.Contains(collapsed, "'queued'"), nil
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

// migrateDocumentsTypeCheck rebuilds documents to widen the column
// CHECK on `type` to admit the four BACI-115 values (plan, transcript,
// rendered_transcript, review). SQLite can't widen a column CHECK in
// place, so this is the table-rebuild dance — the same pattern as
// migrateAgentDispatchesModeCheck. Keyed off the stored CREATE TABLE
// SQL: a DB whose CHECK already lists 'plan' (fresh schema.sql or a
// prior run of this migration) is a no-op.
func migrateDocumentsTypeCheck(db *sql.DB) error {
	stale, err := documentsTypeCheckIsStale(db)
	if err != nil {
		return err
	}
	if !stale {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// defer_foreign_keys is transaction-scoped — it keeps the DROP TABLE
	// from cascading through document_links (which REFERENCEs documents
	// with ON DELETE CASCADE) during the rebuild.
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fk: %w", err)
	}
	// Mirror schema.sql's documents shape exactly, with the widened CHECK.
	// SELECT *-style copy round-trips because columns line up.
	if _, err := tx.Exec(`
		CREATE TABLE documents_new (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid        TEXT    NOT NULL,
			repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			filename    TEXT    NOT NULL,
			type        TEXT    NOT NULL CHECK (type IN
			              ('user_docs','project_in_planning','project_in_progress',
			               'project_complete','vendor_docs','architecture','designs',
			               'testing_plans','plan','transcript','rendered_transcript',
			               'review')),
			content     TEXT    NOT NULL,
			size_bytes  INTEGER NOT NULL,
			source_path TEXT    NOT NULL DEFAULT '',
			archived_at DATETIME,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(repo_id, filename)
		)
	`); err != nil {
		return fmt.Errorf("create documents_new: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO documents_new
			(id, uuid, repo_id, filename, type, content, size_bytes,
			 source_path, archived_at, created_at, updated_at)
		SELECT
			id, uuid, repo_id, filename, type, content, size_bytes,
			source_path, archived_at, created_at, updated_at
		FROM documents
	`); err != nil {
		return fmt.Errorf("copy documents rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE documents`); err != nil {
		return fmt.Errorf("drop old documents: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE documents_new RENAME TO documents`); err != nil {
		return fmt.Errorf("rename documents_new: %w", err)
	}
	// Re-create the indexes the dropped table carried. idx_documents_type
	// is declared in schema.sql; idx_documents_archived_at is created
	// later in migrate() (so it can run after the archived_at column is
	// backfilled on very old DBs) but we recreate it here too so a
	// freshly-rebuilt table never has a missing index.
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(type)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_archived_at ON documents(archived_at)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate documents index: %w", err)
		}
	}
	return tx.Commit()
}

// documentsTypeCheckIsStale reports whether the documents table still
// carries the pre-BACI-115 narrow CHECK on `type` — i.e. one that does
// not mention 'plan'. Whitespace-collapsed lookup on the stored
// CREATE TABLE SQL so trivial reformatting doesn't fool it.
func documentsTypeCheckIsStale(db *sql.DB) (bool, error) {
	var sqlText sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'documents'`).Scan(&sqlText)
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
	// The widened CHECK lists 'plan' (one of the four new BACI-115
	// values). A stored CREATE TABLE that omits it predates BACI-115
	// and needs the rebuild.
	return !strings.Contains(collapsed, "'plan'"), nil
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

// migrateSyncStateKindCheck rebuilds sync_state to widen its `kind`
// CHECK so that 'feature_comment' (BACI-124) joins the existing
// allowlist. SQLite can't alter CHECK constraints in place, so we
// follow the same drop-and-recopy pattern as
// migrateDocumentsTypeCheck. Keyed off the stored CREATE TABLE SQL: a
// DB whose CHECK already lists `feature_comment` (a fresh schema.sql
// or a prior run of this migration) is a no-op.
func migrateSyncStateKindCheck(db *sql.DB) error {
	stale, err := syncStateKindCheckIsStale(db)
	if err != nil {
		return err
	}
	if !stale {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer fk: %w", err)
	}
	if _, err := tx.Exec(`
		CREATE TABLE sync_state_new (
			uuid             TEXT    NOT NULL PRIMARY KEY,
			kind             TEXT    NOT NULL CHECK (kind IN
			                   ('issue','feature','document','comment','feature_comment','repo')),
			last_synced_at   DATETIME NOT NULL,
			last_synced_hash TEXT    NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create sync_state_new: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO sync_state_new (uuid, kind, last_synced_at, last_synced_hash)
		SELECT uuid, kind, last_synced_at, last_synced_hash FROM sync_state
	`); err != nil {
		return fmt.Errorf("copy sync_state rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE sync_state`); err != nil {
		return fmt.Errorf("drop old sync_state: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE sync_state_new RENAME TO sync_state`); err != nil {
		return fmt.Errorf("rename sync_state_new: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_state_kind ON sync_state(kind)`); err != nil {
		return fmt.Errorf("recreate idx_sync_state_kind: %w", err)
	}
	return tx.Commit()
}

func syncStateKindCheckIsStale(db *sql.DB) (bool, error) {
	var sqlText sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sync_state'`).Scan(&sqlText)
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
	return !strings.Contains(collapsed, "'feature_comment'"), nil
}
