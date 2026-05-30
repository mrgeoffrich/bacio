package store

import (
	"context"
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

// ProxyRequestRetention bounds how long BACI-302 proxy-capture index rows
// are kept. Mirrors HistoryRetention's 60-day window — the proxy_requests
// table is cross-cutting like the audit log, so it gets the same on-Open
// prune. Note: this prunes the SQLite index only; the raw req/resp capture
// files on disk have no auto-prune in BACI-302 (same as today's logs /
// transcripts) — see docs/reverse-proxy.md.
const ProxyRequestRetention = 60 * 24 * time.Hour

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
	// BACI-140: agent_claims.issue_id has ON DELETE CASCADE + foreign_keys
	// ON, so an orphan should be impossible. But raw `sqlite3` writes
	// from pre-BACI-134 smoke-test scaffolding bypassed the cascade and
	// left rows pointing at vanished issues, which then crashes
	// EndAgentSession (`sql: no rows in result set`) and stalls the idle
	// pinger. Sweep them at Open so the reaper can make progress; a
	// recurrence here means a future code path is bypassing the FK.
	if n, err := pruneOrphanAgentClaims(db); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: orphan agent_claims prune failed:", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "bacio: warning: orphan agent_claims pruned: %d\n", n)
	}
	if err := pruneDispatches(db, AgentDispatchRetention); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: dispatch prune failed:", err)
	}
	if err := pruneAgentChannels(db); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: agent-channel prune failed:", err)
	}
	if err := pruneProxyRequests(db, ProxyRequestRetention); err != nil {
		fmt.Fprintln(os.Stderr, "bacio: warning: proxy-request prune failed:", err)
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

// pruneProxyRequests deletes BACI-302 proxy-capture index rows whose
// started_at is older than retention. Mirrors pruneHistory — best-effort
// housekeeping run on every Open, never fatal. The raw capture files on disk
// are not touched here (no auto-prune in BACI-302).
func pruneProxyRequests(db *sql.DB, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`DELETE FROM proxy_requests WHERE started_at < ?`, cutoff)
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
	// features.emoji (BACI-172) — the per-feature single-glyph brand used
	// by the kanban card render. Idempotent ALTER; default '' for older
	// rows so existing features start with no glyph.
	hasFeatureEmoji, err := columnExists(db, "features", "emoji")
	if err != nil {
		return err
	}
	if !hasFeatureEmoji {
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN emoji TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add emoji to features: %w", err)
		}
	}
	// features.branch_name (BACI-225) is the per-feature integration
	// branch. NULL preserves the legacy ship-to-main behaviour on
	// upgrade; the BACI-226+ follow-ons read non-NULL values to scope
	// ship concurrency, base the worker on the right ref, and merge
	// the branch back to main at feature-done. Idempotent ALTER; no
	// DEFAULT so existing rows surface as NULL rather than ''.
	hasFeatureBranchName, err := columnExists(db, "features", "branch_name")
	if err != nil {
		return err
	}
	if !hasFeatureBranchName {
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN branch_name TEXT`); err != nil {
			return fmt.Errorf("add branch_name to features: %w", err)
		}
	}
	// issues.base_branch (BACI-232) is the per-issue override for the
	// branch a PR merges into. NULL preserves the legacy "inherit from
	// the feature (or ship to main)" behaviour on upgrade; the resolver
	// that combines this with features.branch_name lands in BACI-226.
	// Idempotent ALTER; no DEFAULT so existing rows surface as NULL.
	hasIssueBaseBranch, err := columnExists(db, "issues", "base_branch")
	if err != nil {
		return err
	}
	if !hasIssueBaseBranch {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN base_branch TEXT`); err != nil {
			return fmt.Errorf("add base_branch to issues: %w", err)
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
	// BACI-255: drop the denormalised waiting_for_claim cache column. The
	// flag was always a stored mirror of "is there an open
	// queued/pending/delivered dispatch on this issue?", and could drift
	// — readers now consult agent_dispatches directly via
	// WaitingDispatchForIssue, so the column is dead weight that can lie.
	// SQLite 3.35+ supports DROP COLUMN, which every supported runtime
	// has. Guarded by columnExists so the migration is idempotent on
	// already-migrated DBs and on fresh schema.sql installs.
	hasWaitingForClaim, err := columnExists(db, "issues", "waiting_for_claim")
	if err != nil {
		return err
	}
	if hasWaitingForClaim {
		if _, err := db.Exec(`ALTER TABLE issues DROP COLUMN waiting_for_claim`); err != nil {
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
	// agent_sessions.errored_at / error_type / error_message capture an
	// Anthropic API failure that aborted the session's turn (BACI-296),
	// written by the StopFailure hook and cleared on the next successful
	// heartbeat. All three are added the same way registered_at /
	// channel_version were — guarded by columnExists so the migration is
	// idempotent on existing DBs.
	hasErroredAt, err := columnExists(db, "agent_sessions", "errored_at")
	if err != nil {
		return err
	}
	if !hasErroredAt {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN errored_at DATETIME`); err != nil {
			return fmt.Errorf("add errored_at to agent_sessions: %w", err)
		}
	}
	hasErrorType, err := columnExists(db, "agent_sessions", "error_type")
	if err != nil {
		return err
	}
	if !hasErrorType {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN error_type TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add error_type to agent_sessions: %w", err)
		}
	}
	hasErrorMessage, err := columnExists(db, "agent_sessions", "error_message")
	if err != nil {
		return err
	}
	if !hasErrorMessage {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN error_message TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add error_message to agent_sessions: %w", err)
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
	// BACI-252: drop the legacy prompt_templates.allowed_states_json
	// column on older DBs BEFORE any prompt_templates write below, so
	// every subsequent seed / backfill INSERT matches the new schema.
	// Idempotent via PRAGMA table_info — re-running Open on a
	// post-drop DB is a no-op.
	if err := migrateDropAllowedStatesJSON(db); err != nil {
		return fmt.Errorf("drop allowed_states_json from prompt_templates: %w", err)
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
	// BACI-132: agent_session_todos.dispatch_id narrows the scope key one
	// step further — (session, issue, dispatch) — so two dispatches on
	// the same issue (e.g. plan → implement on BACI-X) don't share a task
	// list on the kanban Tasks pill and the Agents view. Pre-BACI-132
	// rows have NULL here and fall out of the wider card-side filter; the
	// unfiltered HTTP path (`GET /agents/sessions/{id}/todos` without
	// `dispatch_id`) still returns them. The orphan path at hook time
	// also lands here as NULL when no dispatch can be resolved.
	hasTodoDispatchID, err := columnExists(db, "agent_session_todos", "dispatch_id")
	if err != nil {
		return err
	}
	if !hasTodoDispatchID {
		if _, err := db.Exec(`ALTER TABLE agent_session_todos ADD COLUMN dispatch_id INTEGER REFERENCES agent_dispatches(id)`); err != nil {
			return fmt.Errorf("add dispatch_id to agent_session_todos: %w", err)
		}
	}
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_agent_session_todos_session_issue_dispatch
			ON agent_session_todos(session_pk, issue_key, dispatch_id)`,
	); err != nil {
		return fmt.Errorf("create idx_agent_session_todos_session_issue_dispatch: %w", err)
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
	// BACI-138: add issues.terminal_at + index on older DBs. The
	// CREATE TABLE declaration in schema.sql carries the column for
	// fresh DBs; this ALTER + backfill + index brings older DBs up to
	// date. The backfill seeds terminal_at = updated_at for every row
	// already in a terminal state — same fidelity as the pre-BACI-138
	// updated_at-as-proxy sort, so a freshly migrated board picks up
	// roughly the same order it had before the upgrade. From that point
	// forward, only state transitions in/out of done/cancelled touch
	// the column, so a non-state edit (tag/title/etc.) on a closed
	// issue no longer reshuffles the column.
	hasTerminalAt, err := columnExists(db, "issues", "terminal_at")
	if err != nil {
		return err
	}
	if !hasTerminalAt {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN terminal_at DATETIME`); err != nil {
			return fmt.Errorf("add terminal_at to issues: %w", err)
		}
		if _, err := db.Exec(
			`UPDATE issues SET terminal_at = updated_at WHERE state IN ('done','cancelled') AND terminal_at IS NULL`,
		); err != nil {
			return fmt.Errorf("backfill terminal_at: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_terminal_at ON issues(terminal_at)`); err != nil {
		return fmt.Errorf("create idx_issues_terminal_at: %w", err)
	}
	// BACI-131: four columns on `comments` for eval comments — the
	// quick-eval notes the kanban card's composer posts while watching
	// an agent work an issue. The CREATE TABLE in schema.sql carries
	// them for fresh DBs; the ALTERs below bring older DBs up to date.
	// SQLite ALTER TABLE can't carry a CHECK (eval IN (0,1)) — that's
	// fine: writes only ever set eval to 0 or 1 and the partial index
	// predicate works regardless of CHECK presence.
	hasEval, err := columnExists(db, "comments", "eval")
	if err != nil {
		return err
	}
	if !hasEval {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN eval INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add eval to comments: %w", err)
		}
	}
	hasCommentSession, err := columnExists(db, "comments", "agent_session_id")
	if err != nil {
		return err
	}
	if !hasCommentSession {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add agent_session_id to comments: %w", err)
		}
	}
	hasCommentDispatch, err := columnExists(db, "comments", "dispatch_id")
	if err != nil {
		return err
	}
	if !hasCommentDispatch {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN dispatch_id INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL`); err != nil {
			return fmt.Errorf("add dispatch_id to comments: %w", err)
		}
	}
	hasCommentMode, err := columnExists(db, "comments", "mode")
	if err != nil {
		return err
	}
	if !hasCommentMode {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN mode TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add mode to comments: %w", err)
		}
	}
	// BACI-141: nullable per-event anchor for eval comments. Pattern-
	// matches the four BACI-131 ALTERs above. NULL is the default for
	// existing rows and for any eval comment posted without an event
	// handle — that's the "dispatch-card-level" fallback the transcript
	// viewer renders pinned to the prompt card.
	hasCommentEventRef, err := columnExists(db, "comments", "transcript_event_ref")
	if err != nil {
		return err
	}
	if !hasCommentEventRef {
		if _, err := db.Exec(`ALTER TABLE comments ADD COLUMN transcript_event_ref TEXT`); err != nil {
			return fmt.Errorf("add transcript_event_ref to comments: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_comments_issue_eval ON comments(issue_id, created_at) WHERE eval = 1`); err != nil {
		return fmt.Errorf("create idx_comments_issue_eval: %w", err)
	}
	if err := migrateSyncStateKindCheck(db); err != nil {
		return err
	}
	// BACI-179: queued_after_dispatch_id is the optional "fire after"
	// link for follow-on dispatches. schema.sql carries it for fresh
	// DBs; the ALTER + partial index backfill older ones. ON DELETE
	// SET NULL must be expressed inline since SQLite ALTERs can't
	// retroactively add FK constraints — the FK still applies for fresh
	// DBs from schema.sql, and the controller sweep's NULL short-circuit
	// (§4 row 4 of the design doc) handles the "parent pruned, follow-on
	// dangles" case on upgraded DBs too.
	hasQueuedAfter, err := columnExists(db, "agent_dispatches", "queued_after_dispatch_id")
	if err != nil {
		return err
	}
	if !hasQueuedAfter {
		if _, err := db.Exec(`ALTER TABLE agent_dispatches ADD COLUMN queued_after_dispatch_id INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL`); err != nil {
			return fmt.Errorf("add queued_after_dispatch_id to agent_dispatches: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_dispatches_queued_after ON agent_dispatches(queued_after_dispatch_id) WHERE queued_after_dispatch_id IS NOT NULL`); err != nil {
		return fmt.Errorf("create idx_dispatches_queued_after: %w", err)
	}
	// BACI-217: queued_until_blockers_clear is the second optional "fire
	// after" gate for follow-on dispatches — wait until every issue on
	// the `to` side of an open `blocks` edge pointing at this
	// dispatch's issue is done/cancelled. schema.sql carries the
	// column for fresh DBs; the ALTER backfills older ones. No partial
	// index in v1: the matcher predicate already filters on
	// status='queued' first, and a full-table scan over queued rows is
	// cheap at v1 scale.
	hasBlockersClear, err := columnExists(db, "agent_dispatches", "queued_until_blockers_clear")
	if err != nil {
		return err
	}
	if !hasBlockersClear {
		if _, err := db.Exec(`ALTER TABLE agent_dispatches ADD COLUMN queued_until_blockers_clear INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add queued_until_blockers_clear to agent_dispatches: %w", err)
		}
	}
	// agent_dispatches.base_branch (BACI-226) is the resolved base
	// branch for the dispatched work — the output of ResolveBaseBranch
	// against the dispatch's issue + its feature, stamped at
	// dispatch-bind time so the worker prompt envelope and the
	// (BACI-227) per-(repo, mode, base_branch) concurrency grouping
	// agree on a single value. NULL on rows queued before BACI-226 and
	// on dispatches without an issue (the SetupDispatchCreator nudges
	// and BACI-57 idle pings); a NULL value falls through to
	// model.DefaultBaseBranch in every read site. Idempotent ALTER; no
	// DEFAULT so legacy rows surface as NULL rather than 'main' (the
	// resolver's fallback is the single source of truth for what
	// "absent" means, and writing 'main' onto every legacy row would
	// lie about what the matcher actually used at the time).
	hasDispatchBaseBranch, err := columnExists(db, "agent_dispatches", "base_branch")
	if err != nil {
		return err
	}
	if !hasDispatchBaseBranch {
		if _, err := db.Exec(`ALTER TABLE agent_dispatches ADD COLUMN base_branch TEXT`); err != nil {
			return fmt.Errorf("add base_branch to agent_dispatches: %w", err)
		}
	}
	// BACI-199: add `state` + `state_manual` columns to `features` on
	// older DBs. The CREATE TABLE declaration in schema.sql carries the
	// columns (with their CHECK constraints) for fresh DBs; the ALTER
	// pair brings older DBs up to date. SQLite ALTER TABLE ADD COLUMN
	// cannot carry a CHECK constraint, but the Go-side parser
	// (model.ParseFeatureState) + the store-boundary writer
	// (SetFeatureState) reject anything outside the enum, so the
	// invariant holds either way. The `NOT NULL DEFAULT` on the ALTER
	// stamps `active` / `0` on every existing row, so an upgraded DB
	// behaves identically to a freshly-seeded one with no manual
	// backfill required.
	hasFeatureState, err := columnExists(db, "features", "state")
	if err != nil {
		return err
	}
	if !hasFeatureState {
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN state TEXT NOT NULL DEFAULT 'active'`); err != nil {
			return fmt.Errorf("add state to features: %w", err)
		}
	}
	hasFeatureStateManual, err := columnExists(db, "features", "state_manual")
	if err != nil {
		return err
	}
	if !hasFeatureStateManual {
		if _, err := db.Exec(`ALTER TABLE features ADD COLUMN state_manual INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add state_manual to features: %w", err)
		}
	}
	// BACI-188: the global "Hide empty board columns" preference (Settings
	// toggle, REST endpoint, store helpers, app_settings KV row) was
	// removed in favour of per-column collapse persisted client-side. Drop
	// the leftover KV row from existing DBs so `bacio history` readers
	// don't keep tripping over a key with no live writer. Idempotent on
	// every startup — `DELETE WHERE` is a no-op once the row is gone.
	if _, err := db.Exec(`DELETE FROM app_settings WHERE key = 'board.hide_empty_columns'`); err != nil {
		return fmt.Errorf("drop board.hide_empty_columns KV row: %w", err)
	}
	// BACI-194: backfill the `research` template row for existing DBs.
	// Mirrors the backfillDispatchPreamble pattern: INSERT OR IGNORE so a
	// deliberate user delete is not resurrected on the next Open, and the
	// operation is idempotent on DBs that already have the row.
	if err := backfillResearchTemplate(db); err != nil {
		return fmt.Errorf("backfill research template: %w", err)
	}
	// BACI-164: backfill the `scope` template row for existing DBs.
	// Same pattern as backfillResearchTemplate — INSERT OR IGNORE so a
	// deliberate user delete is not resurrected on the next Open.
	if err := backfillScopeTemplate(db); err != nil {
		return fmt.Errorf("backfill scope template: %w", err)
	}
	// Pipeline page (BACI Pipeline phase 1). The following columns +
	// tables back the three-column board. All ALTERs carry constant
	// defaults so they are legal SQLite ALTER ADD COLUMNs and existing
	// rows take the sane zero value (priority 0, engine off, no pause).
	// schema.sql carries the same shapes for fresh DBs; these idempotent
	// ALTERs / CREATEs bring older DBs up to date.
	hasPriority, err := columnExists(db, "issues", "priority")
	if err != nil {
		return err
	}
	if !hasPriority {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add priority to issues: %w", err)
		}
	}
	hasEngineMode, err := columnExists(db, "issues", "engine_mode")
	if err != nil {
		return err
	}
	if !hasEngineMode {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN engine_mode TEXT NOT NULL DEFAULT 'off'`); err != nil {
			return fmt.Errorf("add engine_mode to issues: %w", err)
		}
	}
	hasEnginePause, err := columnExists(db, "issues", "engine_pause_reason")
	if err != nil {
		return err
	}
	if !hasEnginePause {
		if _, err := db.Exec(`ALTER TABLE issues ADD COLUMN engine_pause_reason TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add engine_pause_reason to issues: %w", err)
		}
	}
	// pipeline_jobs is the per-issue process chain. CREATE TABLE IF NOT
	// EXISTS is idempotent — fresh DBs get it from schema.sql, older DBs
	// here. No CHECK on `status`: model.ParseJobStatus guards the set at
	// the store boundary (same rationale as agent_dispatches.mode).
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pipeline_jobs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id     INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
			sequence     INTEGER NOT NULL,
			mode         TEXT    NOT NULL,
			status       TEXT    NOT NULL DEFAULT 'pending',
			dispatch_id  INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at   DATETIME,
			completed_at DATETIME,
			UNIQUE(issue_id, sequence)
		)`); err != nil {
		return fmt.Errorf("create pipeline_jobs: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_pipeline_jobs_issue ON pipeline_jobs(issue_id)`); err != nil {
		return fmt.Errorf("create idx_pipeline_jobs_issue: %w", err)
	}
	// Re-parent questions onto a pipeline job (nullable; legacy
	// session-parented rows keep it NULL). ON DELETE SET NULL so a job
	// row's deletion leaves the question reachable. ALTER ADD COLUMN
	// with a REFERENCES clause is legal in SQLite for a nullable column.
	hasQuestionJob, err := columnExists(db, "agent_session_questions", "pipeline_job_id")
	if err != nil {
		return err
	}
	if !hasQuestionJob {
		if _, err := db.Exec(`ALTER TABLE agent_session_questions ADD COLUMN pipeline_job_id INTEGER REFERENCES pipeline_jobs(id) ON DELETE SET NULL`); err != nil {
			return fmt.Errorf("add pipeline_job_id to agent_session_questions: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_asq_pipeline_job ON agent_session_questions(pipeline_job_id)`); err != nil {
		return fmt.Errorf("create idx_asq_pipeline_job: %w", err)
	}
	// Drop the issues.state CHECK on older DBs so the two new Pipeline
	// states (in_pipeline / to_be_shipped) are writable — model.ParseState
	// guards the set at the boundary. Runs AFTER the column ALTERs above
	// so the rebuilt table mirrors the full current column set.
	if err := migrateIssuesStateCheck(db); err != nil {
		return err
	}
	// BACI-300: rewrite any rows still parked in the retired in_progress /
	// needs_action states to in_review (the only off-pipeline resting
	// column left), then rebuild the table to drop the now-unused
	// user_action_reason_type column. The rewrite runs before the rebuild
	// so the dropped column carries no live data, and both run after the
	// state-check rebuild above so they see the full current column set.
	if err := migrateIssuesStateRewrite(db); err != nil {
		return err
	}
	if err := migrateIssuesDropUserActionReason(db); err != nil {
		return err
	}
	// (state, priority) index backs the Backlog / Shipping ordered reads.
	// Created after the rebuild so it lands on the final issues table.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_state_priority ON issues(state, priority)`); err != nil {
		return fmt.Errorf("create idx_issues_state_priority: %w", err)
	}
	// repo_settings.auto_ship (Pipeline) — the per-repo Shipping-column
	// auto-ship toggle. Idempotent ALTER with a constant default for
	// older DBs; schema.sql carries it for fresh ones.
	hasAutoShip, err := columnExists(db, "repo_settings", "auto_ship")
	if err != nil {
		return err
	}
	if !hasAutoShip {
		if _, err := db.Exec(`ALTER TABLE repo_settings ADD COLUMN auto_ship INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add auto_ship to repo_settings: %w", err)
		}
	}
	// BACI-305: classification + correlation columns on proxy_requests so
	// BACI-306's per-job message parser can select exactly the Anthropic
	// message captures and attribute them to a worktree's active dispatch.
	// content_type / is_stream / is_anthropic are transport-level
	// classification; session_id / dispatch_id are best-effort correlation
	// resolved from the X-Bacio-Corr launch header. dispatch_id is a
	// nullable INTEGER with no FK — a capture row is cross-cutting like the
	// audit log, so a deleted dispatch must not cascade-delete it. Each
	// ALTER is columnExists-guarded and idempotent; schema.sql carries the
	// columns for fresh DBs.
	for _, col := range []struct{ name, ddl string }{
		{"content_type", `ALTER TABLE proxy_requests ADD COLUMN content_type TEXT NOT NULL DEFAULT ''`},
		{"is_stream", `ALTER TABLE proxy_requests ADD COLUMN is_stream INTEGER NOT NULL DEFAULT 0`},
		{"is_anthropic", `ALTER TABLE proxy_requests ADD COLUMN is_anthropic INTEGER NOT NULL DEFAULT 0`},
		{"session_id", `ALTER TABLE proxy_requests ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
		{"dispatch_id", `ALTER TABLE proxy_requests ADD COLUMN dispatch_id INTEGER`},
	} {
		has, err := columnExists(db, "proxy_requests", col.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(col.ddl); err != nil {
				return fmt.Errorf("add %s to proxy_requests: %w", col.name, err)
			}
		}
	}
	// BACI-305: agent_sessions.worktree_slug carries the resolved wtenv
	// manifest slug the session is driving, stamped at session open by the
	// session-start hook. It is the launch-time-stable key the reverse-proxy
	// capture resolves back to a session/dispatch. Idempotent ALTER for
	// older DBs; schema.sql carries it for fresh ones.
	hasWorktreeSlug, err := columnExists(db, "agent_sessions", "worktree_slug")
	if err != nil {
		return err
	}
	if !hasWorktreeSlug {
		if _, err := db.Exec(`ALTER TABLE agent_sessions ADD COLUMN worktree_slug TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add worktree_slug to agent_sessions: %w", err)
		}
	}
	return nil
}

// backfillResearchTemplate inserts the `research` built-in template row
// for existing DBs where the first-time seed step already ran before
// BACI-194 added the template. Mirrors backfillDispatchPreamble:
// INSERT OR IGNORE is idempotent, and a deliberate user delete is not
// resurrected (the migration skips rows that never existed).
func backfillResearchTemplate(db *sql.DB) error {
	slug := model.BuiltinTemplateResearch
	body := model.DefaultPromptBodyForBuiltinSlug(slug)
	name := model.BuiltinTemplateLabel(slug)
	if name == "" {
		name = slug
	}
	actionLabel := model.BuiltinTemplateActionLabel(slug)
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO prompt_templates
		  (slug, name, body, is_builtin, concurrency_limit, action_label, created_at, updated_at)
		VALUES (?, ?, ?, 1, 0, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		slug, name, body, actionLabel); err != nil {
		return err
	}
	return nil
}

// backfillScopeTemplate inserts the `scope` built-in template row for
// existing DBs where the first-time seed step already ran before
// BACI-164 added the template. Mirrors backfillResearchTemplate:
// INSERT OR IGNORE is idempotent, and a deliberate user delete is not
// resurrected (the migration skips rows that never existed).
func backfillScopeTemplate(db *sql.DB) error {
	slug := model.BuiltinTemplateScope
	body := model.DefaultPromptBodyForBuiltinSlug(slug)
	name := model.BuiltinTemplateLabel(slug)
	if name == "" {
		name = slug
	}
	actionLabel := model.BuiltinTemplateActionLabel(slug)
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO prompt_templates
		  (slug, name, body, is_builtin, concurrency_limit, action_label, created_at, updated_at)
		VALUES (?, ?, ?, 1, 0, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		slug, name, body, actionLabel); err != nil {
		return err
	}
	return nil
}

// backfillDispatchPreamble inserts the _dispatch_preamble row if it's
// not already present. Used by the migration to bring older DBs (where
// the first-time seed step has already run with only the per-mode
// built-ins) up to date with the BACI-52 wrapper.
//
// The leading-underscore slug keeps the row out of the per-card
// dispatch picker via the reserved-slug filter (BACI-252 — see
// nonReservedPrompts in KanbanCard / IssueWorkspace). action_label =
// ” (preamble never reaches the dropdown either, so the imperative
// form is irrelevant).
func backfillDispatchPreamble(db *sql.DB) error {
	slug := model.BuiltinTemplatePreamble
	body := model.DefaultPromptBodyForBuiltinSlug(slug)
	name := model.BuiltinTemplateLabel(slug)
	if name == "" {
		name = slug
	}
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO prompt_templates
		  (slug, name, body, is_builtin, concurrency_limit, action_label, created_at, updated_at)
		VALUES (?, ?, ?, 1, 0, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		slug, name, body); err != nil {
		return err
	}
	return nil
}

// migrateDropAllowedStatesJSON is the BACI-252 in-place column drop on
// the prompt_templates table. PRAGMA table_info is the column-exists
// probe; the ALTER TABLE ... DROP COLUMN is gated so re-Opens on a
// post-drop DB are a no-op. SQLite 3.35+ (March 2021) supports DROP
// COLUMN natively; the modernc/sqlite driver bundles a much newer
// engine on every supported platform.
func migrateDropAllowedStatesJSON(db *sql.DB) error {
	has, err := columnExists(db, "prompt_templates", "allowed_states_json")
	if err != nil {
		return err
	}
	if !has {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE prompt_templates DROP COLUMN allowed_states_json`); err != nil {
		return fmt.Errorf("drop allowed_states_json: %w", err)
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
		name := model.BuiltinTemplateLabel(slug)
		if name == "" {
			name = slug
		}
		if _, err := tx.Exec(`
			INSERT INTO prompt_templates (slug, name, body, is_builtin, concurrency_limit, action_label, created_at, updated_at)
			VALUES (?, ?, ?, 1, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			slug, name, body, model.DefaultConcurrencyLimit(slug), model.BuiltinTemplateActionLabel(slug)); err != nil {
			return err
		}
	}

	// Fold any pre-BACI-31 app_settings body overrides into the freshly
	// seeded rows, then drop the migrated KV rows. BACI-252 retired
	// the per-template state-gate, so the matching `prompt_states.*`
	// KV rows are now just discarded — the gate they encoded is gone.
	for _, slug := range model.BuiltinTemplateSlugs() {
		bodyKey := "prompt_template." + slug
		statesKey := "prompt_states." + slug

		var bodyVal sql.NullString
		if err := tx.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, bodyKey).Scan(&bodyVal); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if bodyVal.Valid {
			if _, err := tx.Exec(`
				UPDATE prompt_templates
				   SET body = ?, updated_at = CURRENT_TIMESTAMP
				 WHERE slug = ?`, bodyVal.String, slug); err != nil {
				return err
			}
		}
		// Always drop both KV keys (body + retired state-gate) so a
		// later Open never re-applies them.
		if _, err := tx.Exec(`DELETE FROM app_settings WHERE key IN (?, ?)`, bodyKey, statesKey); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// migrateRepoPathUnique relaxes the column-level UNIQUE on repos.path
// (a hangover from before phantom repos were a thing) and replaces it
// with a partial unique index that ignores empty paths. Phantom repos
// (rows with `path = ”`) represent prefixes that exist in the sync
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
// plan/implement era carry CHECK (mode IN (”,'plan','implement')),
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
	// CHECK on `mode`, so SELECT *-style copy round-trips. BACI-179:
	// include queued_after_dispatch_id so the rebuilt table carries
	// the column whether or not the source table did (the source may
	// pre-date BACI-179; the column gets backfilled to NULL).
	// BACI-217: also carry queued_until_blockers_clear so the rebuilt
	// table preserves the second follow-on gate even when the source
	// pre-dates it (it gets backfilled to 0 in that case).
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
			queued_after_dispatch_id INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL,
			queued_until_blockers_clear INTEGER NOT NULL DEFAULT 0,
			base_branch       TEXT,
			CHECK (target_agent_id IS NOT NULL OR target_session_id != '')
		)
	`); err != nil {
		return fmt.Errorf("create agent_dispatches_new: %w", err)
	}
	// Discover whether the source table carries queued_after_dispatch_id
	// already: a DB rebuilt by this migration after BACI-179 lands does,
	// a pre-BACI-179 DB doesn't. The SELECT clause varies accordingly so
	// the older shape doesn't error on the missing column. BACI-226:
	// same dance for base_branch.
	hasQueuedAfter, err := txColumnExists(tx, "agent_dispatches", "queued_after_dispatch_id")
	if err != nil {
		return err
	}
	hasBlockersClear, err := txColumnExists(tx, "agent_dispatches", "queued_until_blockers_clear")
	if err != nil {
		return err
	}
	hasBaseBranch, err := txColumnExists(tx, "agent_dispatches", "base_branch")
	if err != nil {
		return err
	}
	copyStmt := buildAgentDispatchesCopyStmt(hasQueuedAfter, hasBlockersClear, hasBaseBranch)
	if _, err := tx.Exec(copyStmt); err != nil {
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
		`CREATE INDEX IF NOT EXISTS idx_dispatches_queued_after ON agent_dispatches(queued_after_dispatch_id) WHERE queued_after_dispatch_id IS NOT NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate agent_dispatches index: %w", err)
		}
	}
	return tx.Commit()
}

// buildAgentDispatchesCopyStmt assembles the INSERT…SELECT used by the
// agent_dispatches table-rebuild migrations. The base column list is
// always populated; queued_after_dispatch_id (BACI-179),
// queued_until_blockers_clear (BACI-217), and base_branch (BACI-226)
// are appended only when the source table already carries them, so an
// older DB doesn't error on a missing column. Columns absent from
// SELECT pick up their schema defaults (NULL / 0) on the destination.
func buildAgentDispatchesCopyStmt(hasQueuedAfter, hasBlockersClear, hasBaseBranch bool) string {
	cols := []string{"id", "repo_id", "target_agent_id", "target_session_id", "issue_id", "mode",
		"payload", "status", "created_by", "created_at", "delivered_at", "acked_at", "ack_note"}
	if hasQueuedAfter {
		cols = append(cols, "queued_after_dispatch_id")
	}
	if hasBlockersClear {
		cols = append(cols, "queued_until_blockers_clear")
	}
	if hasBaseBranch {
		cols = append(cols, "base_branch")
	}
	list := strings.Join(cols, ", ")
	return `INSERT INTO agent_dispatches_new (` + list + `) SELECT ` + list + ` FROM agent_dispatches`
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
	// columns so a SELECT * INSERT * copy round-trips. BACI-179: include
	// queued_after_dispatch_id so the rebuilt table carries the column
	// even when the source table pre-dates it. BACI-217: same for
	// queued_until_blockers_clear.
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
			ack_note          TEXT    NOT NULL DEFAULT '',
			queued_after_dispatch_id INTEGER REFERENCES agent_dispatches(id) ON DELETE SET NULL,
			queued_until_blockers_clear INTEGER NOT NULL DEFAULT 0,
			base_branch       TEXT
		)
	`); err != nil {
		return fmt.Errorf("create agent_dispatches_new: %w", err)
	}
	hasQueuedAfter, err := txColumnExists(tx, "agent_dispatches", "queued_after_dispatch_id")
	if err != nil {
		return err
	}
	hasBlockersClear, err := txColumnExists(tx, "agent_dispatches", "queued_until_blockers_clear")
	if err != nil {
		return err
	}
	hasBaseBranch, err := txColumnExists(tx, "agent_dispatches", "base_branch")
	if err != nil {
		return err
	}
	copyStmt := buildAgentDispatchesCopyStmt(hasQueuedAfter, hasBlockersClear, hasBaseBranch)
	if _, err := tx.Exec(copyStmt); err != nil {
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
		`CREATE INDEX IF NOT EXISTS idx_dispatches_queued_after ON agent_dispatches(queued_after_dispatch_id) WHERE queued_after_dispatch_id IS NOT NULL`,
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

// issuesStateCheckPresent reports whether the issues table still carries
// a column-level CHECK on `state` — every DB created from a pre-Pipeline
// schema.sql has CHECK (state IN (...)), which would reject the two new
// Pipeline states. Whitespace-collapsed lookup on the stored CREATE
// TABLE SQL so reformatting doesn't fool it.
func issuesStateCheckPresent(db *sql.DB) (bool, error) {
	var sqlText sql.NullString
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'issues'`).Scan(&sqlText)
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
	return strings.Contains(collapsed, "CHECK (state IN"), nil
}

// migrateIssuesStateCheck rebuilds the issues table to drop the
// column-level CHECK on `state`, so the two new Pipeline states
// (in_pipeline / to_be_shipped) are writable. model.ParseState guards
// the set at the store boundary, so the CHECK is redundant safety that
// SQLite can't extend in place.
//
// issues is the parent of many cascade children (comments, tags,
// relations, PRs, claims, document_links, pipeline_jobs all REFERENCE
// issues with ON DELETE CASCADE / SET NULL). DROP TABLE performs an
// implicit row-delete that fires those cascades when foreign-key
// enforcement is ON — and unlike the `repos` rebuild's transaction-scoped
// defer_foreign_keys (which does NOT suppress the cascade in this
// driver), the only thing that reliably stops the wipe is fully
// disabling foreign_keys. That PRAGMA is per-connection and a no-op
// inside a transaction, so the whole rebuild runs on one dedicated
// connection with enforcement OFF; the copy preserves every id, so child
// FKs still resolve once enforcement is switched back ON.
//
// Keyed off the stored CREATE TABLE SQL: fresh DBs (schema.sql no longer
// declares the CHECK) and already-migrated DBs skip the rebuild. Runs
// AFTER the Pipeline column ALTERs in migrate(), so the source table is
// guaranteed to carry priority / engine_mode / engine_pause_reason and
// the copy column list is fixed.
func migrateIssuesStateCheck(db *sql.DB) error {
	present, err := issuesStateCheckPresent(db)
	if err != nil || !present {
		return err
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable fk: %w", err)
	}
	rebuildErr := rebuildIssuesDropStateCheck(ctx, conn)
	// Restore the connection's pragmas before it returns to the pool —
	// even if the rebuild failed — so no pooled connection is left with
	// FK enforcement off or in legacy rename mode.
	if _, err := conn.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`); err != nil && rebuildErr == nil {
		rebuildErr = fmt.Errorf("reset legacy alter table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil && rebuildErr == nil {
		rebuildErr = fmt.Errorf("re-enable fk: %w", err)
	}
	return rebuildErr
}

// rebuildIssuesDropStateCheck runs the create/copy/drop/rename dance on
// the supplied dedicated connection (foreign_keys already OFF). It also
// recreates the indexes and the one trigger defined ON issues — both are
// dropped with the table; the next Open() re-applies schema.sql's
// IF NOT EXISTS forms, but recreating them in-tx keeps the table correct
// for the rest of this process. A foreign_key_check before commit guards
// against a botched copy leaving dangling child references.
func rebuildIssuesDropStateCheck(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// legacy_alter_table makes the RENAME below a pure rename instead of
	// trying to rewrite references to "issues" inside other objects.
	// issues is referenced by the side-data bump triggers (on
	// issue_pull_requests / issue_relations / issue_tags); once the old
	// issues table is dropped the modern RENAME walks those trigger
	// bodies and fails with "no such table: issues" before the rename
	// lands. (migrateRepoPathUnique doesn't need this — nothing
	// references repos in a trigger body.)
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = ON`); err != nil {
		return fmt.Errorf("legacy alter table on: %w", err)
	}
	// Mirror schema.sql's issues shape exactly, minus the state CHECK.
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE issues_new (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid        TEXT    NOT NULL,
			repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			number      INTEGER NOT NULL,
			feature_id  INTEGER REFERENCES features(id) ON DELETE SET NULL,
			title       TEXT    NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
			state       TEXT    NOT NULL,
			assignee    TEXT    NOT NULL DEFAULT '',
			archived_at DATETIME,
			terminal_at DATETIME,
			user_action_reason_type TEXT,
			base_branch TEXT,
			priority    INTEGER NOT NULL DEFAULT 0,
			engine_mode TEXT    NOT NULL DEFAULT 'off',
			engine_pause_reason TEXT NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(repo_id, number)
		)
	`); err != nil {
		return fmt.Errorf("create issues_new: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issues_new
			(id, uuid, repo_id, number, feature_id, title, description, state, assignee,
			 archived_at, terminal_at, user_action_reason_type, base_branch,
			 priority, engine_mode, engine_pause_reason, created_at, updated_at)
		SELECT
			id, uuid, repo_id, number, feature_id, title, description, state, assignee,
			archived_at, terminal_at, user_action_reason_type, base_branch,
			priority, engine_mode, engine_pause_reason, created_at, updated_at
		FROM issues
	`); err != nil {
		return fmt.Errorf("copy issues rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE issues`); err != nil {
		return fmt.Errorf("drop old issues: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE issues_new RENAME TO issues`); err != nil {
		return fmt.Errorf("rename issues_new: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_feature ON issues(feature_id)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_archived_at ON issues(archived_at)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_terminal_at ON issues(terminal_at)`,
		`CREATE TRIGGER IF NOT EXISTS bump_issue_updated_on_archive_change
		 AFTER UPDATE OF archived_at ON issues
		 WHEN NEW.archived_at IS NOT OLD.archived_at
		   AND NEW.updated_at = OLD.updated_at
		 BEGIN
		     UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
		 END`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("recreate issues index/trigger: %w", err)
		}
	}
	// Integrity net: with every id preserved there must be zero dangling
	// child references. Catch a botched copy before commit rather than
	// leaving the store inconsistent.
	bad, err := txHasForeignKeyViolation(ctx, tx)
	if err != nil {
		return err
	}
	if bad {
		return errors.New("issues rebuild left dangling foreign keys")
	}
	return tx.Commit()
}

// txHasForeignKeyViolation reports whether PRAGMA foreign_key_check finds
// any violating row in tx. The pragma runs regardless of the connection's
// enforcement setting, so it works mid-rebuild with foreign_keys OFF.
func txHasForeignKeyViolation(ctx context.Context, tx *sql.Tx) (bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return false, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	bad := rows.Next()
	if err := rows.Err(); err != nil {
		return false, err
	}
	return bad, nil
}

// migrateIssuesStateRewrite (BACI-300) rewrites any rows still parked in
// the retired in_progress / needs_action states to in_review — the only
// off-pipeline non-terminal resting column left after the retirement. A
// card that was mid-flight on an old DB lands in the "awaiting a human"
// column rather than being lost; done / cancelled / the Pipeline states
// are untouched. Idempotent: a DB with no legacy-state rows updates zero
// rows. This is a plain UPDATE rather than a table rebuild, so it carries
// no FK / trigger risk; it runs before migrateIssuesDropUserActionReason
// so the column being dropped carries no live data.
func migrateIssuesStateRewrite(db *sql.DB) error {
	if _, err := db.Exec(
		`UPDATE issues SET state = 'in_review', updated_at = CURRENT_TIMESTAMP WHERE state IN ('in_progress', 'needs_action')`,
	); err != nil {
		return fmt.Errorf("rewrite legacy issue states: %w", err)
	}
	return nil
}

// migrateIssuesDropUserActionReason (BACI-300) rebuilds the issues table
// to drop the user_action_reason_type column, whose last writer (the
// terminal-error needs_action stamp) was retired alongside the
// needs_action state. Modelled verbatim on migrateIssuesStateCheck's
// rebuild dance — guarded on columnExists so fresh / already-migrated
// DBs skip it, runs on a dedicated connection with foreign_keys OFF so
// the DROP TABLE doesn't cascade through the issues children, preserves
// every id, and FK-checks before commit.
func migrateIssuesDropUserActionReason(db *sql.DB) error {
	has, err := columnExists(db, "issues", "user_action_reason_type")
	if err != nil || !has {
		return err
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable fk: %w", err)
	}
	rebuildErr := rebuildIssuesDropUserActionReason(ctx, conn)
	// Restore the connection's pragmas before it returns to the pool —
	// even if the rebuild failed — so no pooled connection is left with
	// FK enforcement off or in legacy rename mode.
	if _, err := conn.ExecContext(ctx, `PRAGMA legacy_alter_table = OFF`); err != nil && rebuildErr == nil {
		rebuildErr = fmt.Errorf("reset legacy alter table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil && rebuildErr == nil {
		rebuildErr = fmt.Errorf("re-enable fk: %w", err)
	}
	return rebuildErr
}

// rebuildIssuesDropUserActionReason runs the create/copy/drop/rename
// dance on the supplied dedicated connection (foreign_keys already OFF),
// producing an issues table without the user_action_reason_type column.
// It mirrors rebuildIssuesDropStateCheck exactly — same index/trigger
// recreation and pre-commit foreign_key_check — minus the dropped column
// from the CREATE / INSERT column lists.
func rebuildIssuesDropUserActionReason(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// legacy_alter_table makes the RENAME below a pure rename instead of
	// trying to rewrite references to "issues" inside the side-data bump
	// triggers — same rationale as rebuildIssuesDropStateCheck.
	if _, err := tx.ExecContext(ctx, `PRAGMA legacy_alter_table = ON`); err != nil {
		return fmt.Errorf("legacy alter table on: %w", err)
	}
	// Mirror schema.sql's issues shape exactly, minus user_action_reason_type.
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE issues_new (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid        TEXT    NOT NULL,
			repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			number      INTEGER NOT NULL,
			feature_id  INTEGER REFERENCES features(id) ON DELETE SET NULL,
			title       TEXT    NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
			state       TEXT    NOT NULL,
			assignee    TEXT    NOT NULL DEFAULT '',
			archived_at DATETIME,
			terminal_at DATETIME,
			base_branch TEXT,
			priority    INTEGER NOT NULL DEFAULT 0,
			engine_mode TEXT    NOT NULL DEFAULT 'off',
			engine_pause_reason TEXT NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(repo_id, number)
		)
	`); err != nil {
		return fmt.Errorf("create issues_new: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO issues_new
			(id, uuid, repo_id, number, feature_id, title, description, state, assignee,
			 archived_at, terminal_at, base_branch,
			 priority, engine_mode, engine_pause_reason, created_at, updated_at)
		SELECT
			id, uuid, repo_id, number, feature_id, title, description, state, assignee,
			archived_at, terminal_at, base_branch,
			priority, engine_mode, engine_pause_reason, created_at, updated_at
		FROM issues
	`); err != nil {
		return fmt.Errorf("copy issues rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE issues`); err != nil {
		return fmt.Errorf("drop old issues: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE issues_new RENAME TO issues`); err != nil {
		return fmt.Errorf("rename issues_new: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_feature ON issues(feature_id)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_assignee ON issues(assignee)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_archived_at ON issues(archived_at)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_terminal_at ON issues(terminal_at)`,
		`CREATE TRIGGER IF NOT EXISTS bump_issue_updated_on_archive_change
		 AFTER UPDATE OF archived_at ON issues
		 WHEN NEW.archived_at IS NOT OLD.archived_at
		   AND NEW.updated_at = OLD.updated_at
		 BEGIN
		     UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
		 END`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("recreate issues index/trigger: %w", err)
		}
	}
	// Integrity net: with every id preserved there must be zero dangling
	// child references. Catch a botched copy before commit.
	bad, err := txHasForeignKeyViolation(ctx, tx)
	if err != nil {
		return err
	}
	if bad {
		return errors.New("issues rebuild left dangling foreign keys")
	}
	return tx.Commit()
}

// migrateDocumentsTypeCheck rebuilds documents to widen the column
// CHECK on `type` to admit the BACI-115 values (plan, transcript,
// rendered_transcript, review) plus the later `session_retro` addition.
// SQLite can't widen a column CHECK in place, so this is the
// table-rebuild dance — the same pattern as migrateAgentDispatchesModeCheck.
// Keyed off the stored CREATE TABLE SQL: a DB whose CHECK already
// lists 'session_retro' (fresh schema.sql or a prior run of this
// migration that's been re-cut) is a no-op.
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
	// Drop the BACI-144 document_links triggers that reference
	// `documents` before the rebuild. SQLite parses every trigger body
	// during ALTER TABLE RENAME to find references to the old name; a
	// trigger whose body still references the just-dropped `documents`
	// table fails that parse with "no such table" before the rename
	// re-creates it. We re-add the triggers below after the rename.
	for _, stmt := range []string{
		`DROP TRIGGER IF EXISTS bump_document_updated_on_link_insert`,
		`DROP TRIGGER IF EXISTS bump_document_updated_on_link_delete`,
		`DROP TRIGGER IF EXISTS bump_document_updated_on_link_description_update`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("drop doc-link trigger: %w", err)
		}
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
			               'review','session_retro')),
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
	// Re-create the BACI-144 document_links triggers dropped above. The
	// statements are byte-identical to the schema.sql definitions so a
	// freshly-rebuilt table behaves the same as a fresh schema.sql
	// install. Kept inline rather than re-applying schemaSQL to avoid
	// double-defining the rest of the schema.
	for _, stmt := range []string{
		`CREATE TRIGGER IF NOT EXISTS bump_document_updated_on_link_insert
		 AFTER INSERT ON document_links
		 BEGIN
		     UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.document_id;
		 END`,
		`CREATE TRIGGER IF NOT EXISTS bump_document_updated_on_link_delete
		 AFTER DELETE ON document_links
		 BEGIN
		     UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.document_id;
		 END`,
		`CREATE TRIGGER IF NOT EXISTS bump_document_updated_on_link_description_update
		 AFTER UPDATE OF description ON document_links
		 BEGIN
		     UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.document_id;
		 END`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate doc-link trigger: %w", err)
		}
	}
	return tx.Commit()
}

// documentsTypeCheckIsStale reports whether the documents table still
// carries a narrow CHECK on `type` — i.e. one that does not yet mention
// 'session_retro'. Whitespace-collapsed lookup on the stored CREATE
// TABLE SQL so trivial reformatting doesn't fool it. Gating on
// 'session_retro' (rather than the older BACI-115 'plan' sentinel) also
// re-runs the rebuild on any DB that pre-dates the session-retro
// addition — the latest token in the allow-list is always the right
// staleness gate.
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
	return !strings.Contains(collapsed, "'session_retro'"), nil
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

// txColumnExists mirrors columnExists but runs inside a *sql.Tx.
// PRAGMA table_info reads the live schema; running it inside the
// rebuild tx ensures the migration sees the table shape it's about
// to drop (rather than a sibling caller's view of the world).
func txColumnExists(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
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
