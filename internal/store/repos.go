package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

var ErrNotFound = errors.New("not found")

const repoCols = `id, uuid, prefix, name, kind, path, remote_url, next_issue_number, created_at, updated_at`

// validateRepoKindPath is the store-boundary guard for the (kind, path)
// truth table documented on model.Repo's predicates:
//
//	kind='git'       path=''      -> phantom (synced prefix, no checkout here)
//	kind='git'       path='/abs'  -> a linked git repo
//	kind='workspace' path=''      -> a manual workspace
//	kind='workspace' path!=''     -> IMPOSSIBLE — rejected here
//
// A workspace with a path would silently re-overload the `path == ""`
// signal the pivot just disentangled: every filesystem-touching site
// (git detection, .bacio/config.yaml, worktrees, doc --from-path)
// would then treat a workspace as a checkout. There is deliberately no
// SQL CHECK — per docs/agent-cli-principles.md rule #4 the invariant
// lives at the store boundary, so every transport (CLI, HTTP, Wails,
// sync import) inherits it from the one place that writes the row.
//
// Empty kind is accepted and read as git: the column's DEFAULT is
// 'git' and model.Repo's predicates treat '' the same way, so a value
// built in Go without an explicit Kind is not an error.
func validateRepoKindPath(kind model.RepoKind, path string) error {
	switch kind {
	case model.RepoKindWorkspace:
		if path != "" {
			return fmt.Errorf("workspace repos must have an empty path (got %q): a workspace has no working tree", path)
		}
		return nil
	case model.RepoKindGit, "":
		return nil
	default:
		return fmt.Errorf("unknown repo kind %q (want %q or %q)", kind, model.RepoKindGit, model.RepoKindWorkspace)
	}
}

// insertRepo is the single INSERT path for the repos table — every
// exported creator funnels through it so the (kind, path) invariant is
// enforced exactly once and no caller can route around it.
func (s *Store) insertRepo(uuid, prefix, name string, kind model.RepoKind, path, remoteURL string) (*model.Repo, error) {
	if kind == "" {
		kind = model.RepoKindGit
	}
	if err := validateRepoKindPath(kind, path); err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO repos (uuid, prefix, name, kind, path, remote_url) VALUES (?, ?, ?, ?, ?, ?)`,
		uuid, prefix, name, string(kind), path, remoteURL,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetRepoByID(id)
}

// CreateRepo registers a git-backed repo. `path` may be empty, which
// yields a phantom (a prefix known to the sync repo with no local
// checkout); it can never yield a workspace — that is CreateWorkspace's
// job, and the kind is written explicitly here rather than leaning on
// the column DEFAULT so the row is unambiguous on inspection.
func (s *Store) CreateRepo(prefix, name, path, remoteURL string) (*model.Repo, error) {
	return s.insertRepo(identity.New(), prefix, name, model.RepoKindGit, path, remoteURL)
}

// CreateWorkspace registers a manual workspace: a bacio-only container
// with a 4-char prefix, issues, documents, folders and a Kanban, but no
// checkout, no remote and no working tree (kind='workspace', path='',
// remote_url=''). The (kind, path) invariant is structural here — the
// signature takes no path at all — and is re-asserted by insertRepo.
//
// prefix is optional: empty allocates one from the name via the same
// AllocatePrefix machinery every git registration uses (the user's
// "4-letter workspaces" IS the existing prefix concept), and a supplied
// prefix is validated + upper-cased by ValidatePrefix. Prefix
// uniqueness is one namespace shared with git repos, enforced by the
// UNIQUE on repos.prefix.
//
// Unlike CreateRepo this DOES bootstrap, because nothing else ever
// will: a git repo is re-resolved from its cwd on every command and
// bootstrapped there, but a workspace has no cwd to be found from, so
// an un-bootstrapped one would open with no Kanban board and no
// catch-all features, permanently. BootstrapRepoDefaults is idempotent,
// so a caller that also bootstraps is harmless.
//
// The sync importer must NOT use this — it needs to preserve the uuid
// from repo.yaml and must not mint a new one, and it runs inside a
// single *sql.Tx that this function's nested writes would deadlock
// against. It uses InsertPhantomRepoTx instead.
func (s *Store) CreateWorkspace(prefix, name string) (*model.Repo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("CreateWorkspace: name is required")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		allocated, err := s.AllocatePrefix(name)
		if err != nil {
			return nil, fmt.Errorf("CreateWorkspace: allocate prefix: %w", err)
		}
		prefix = allocated
	} else {
		valid, err := ValidatePrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("CreateWorkspace: %w", err)
		}
		prefix = valid
	}
	repo, err := s.insertRepo(identity.New(), prefix, name, model.RepoKindWorkspace, "", "")
	if err != nil {
		return nil, err
	}
	// A workspace has no cwd to auto-register from, so nothing will
	// re-resolve it later and bootstrap it the way the git path does on
	// every `resolveRepoC`. Seed it here or it opens with no Kanban board
	// and no catch-all features. Idempotent, so a caller that also
	// bootstraps is harmless.
	if err := s.BootstrapRepoDefaults(repo.ID); err != nil {
		return nil, fmt.Errorf("CreateWorkspace: bootstrap defaults: %w", err)
	}
	return repo, nil
}

func (s *Store) GetRepoByID(id int64) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT `+repoCols+` FROM repos WHERE id = ?`, id))
}

// GetRepoByPath resolves the repo bound to a local working tree.
//
// An empty path is never a hit: `path = ''` is not an identity — it is
// shared by every phantom AND every workspace (uniq_repos_path is
// partial for exactly that reason), so matching it would return an
// arbitrary pathless row. Callers that pass an unvalidated path (e.g.
// the "is this path already registered?" pre-check on repo create) get
// ErrNotFound instead of a false positive.
func (s *Store) GetRepoByPath(path string) (*model.Repo, error) {
	if path == "" {
		return nil, ErrNotFound
	}
	return scanRepo(s.DB.QueryRow(`SELECT `+repoCols+` FROM repos WHERE path = ?`, path))
}

func (s *Store) GetRepoByPrefix(prefix string) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT `+repoCols+` FROM repos WHERE prefix = ?`, prefix))
}

// GetRepoByUUID is the canonical sync-side lookup: every record in
// repo.yaml carries the immutable uuid, so import resolves a synced
// folder back to its DB row by uuid rather than the (mutable) prefix
// or path.
func (s *Store) GetRepoByUUID(uuid string) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT `+repoCols+` FROM repos WHERE uuid = ?`, uuid))
}

// CreatePhantomRepo inserts a row representing a sync-repo prefix
// that has no local working tree on this machine yet (path = '').
// Used by the import path when it encounters a repos/<prefix>/
// folder for which bacio has no DB row.
//
// The caller supplies the uuid (read from repo.yaml so it survives
// across machines) and the prefix; remoteURL and name are best-effort
// hints from the imported file. Validates that no other repo —
// phantom or real — already owns the prefix, since prefix is still
// a hard UNIQUE.
//
// Phantom rows can later be "upgraded" to real ones via
// UpgradePhantomRepo when the user runs bacio from inside the matching
// project working tree.
//
// Always kind='git': a phantom is a *git* repo whose checkout lives on
// another machine. A synced prefix that is actually a workspace is a
// different row shape (kind='workspace', permanently pathless) and is
// never upgradeable, so it must not come through here.
func (s *Store) CreatePhantomRepo(uuid, prefix, name, remoteURL string) (*model.Repo, error) {
	if uuid == "" || prefix == "" {
		return nil, fmt.Errorf("CreatePhantomRepo: uuid and prefix are required")
	}
	return s.insertRepo(uuid, prefix, name, model.RepoKindGit, "", remoteURL)
}

// CreatePhantomWorkspace is CreatePhantomRepo's workspace twin: a
// uuid-preserving insert for a synced prefix that the sync repo marks
// as a workspace (a repos/<PREFIX>/workspace.yaml sentinel sits beside
// its repo.yaml).
//
// Neither existing constructor fits. CreateWorkspace mints its own
// UUIDv7, which would fork the identity the sync repo already carries;
// CreatePhantomRepo is hard-wired to kind='git' precisely so a
// workspace can't sneak in through it.
//
// Deliberately does NOT call BootstrapRepoDefaults. An imported prefix
// gets its features and Kanban lanes from the sync repo itself, and
// seeding a local default set on top would immediately collide with the
// incoming records (see the sync importer's name-collision handling).
// That is the same stance CreatePhantomRepo takes.
func (s *Store) CreatePhantomWorkspace(uuid, prefix, name string) (*model.Repo, error) {
	if uuid == "" || prefix == "" {
		return nil, fmt.Errorf("CreatePhantomWorkspace: uuid and prefix are required")
	}
	return s.insertRepo(uuid, prefix, name, model.RepoKindWorkspace, "", "")
}

// InsertPhantomRepoTx is the transaction-aware insert the sync importer
// uses, mirroring MarkSyncedTx's precedent: the importer runs its whole
// pass inside one *sql.Tx, so it cannot call the *sql.DB-based
// constructors above — a second pooled connection would block on the
// open write transaction until busy_timeout expired and then fail.
//
// It exists so the importer stops writing `INSERT INTO repos` by hand.
// The (kind, path) invariant is structural here: there is no path
// parameter at all. An imported prefix never has a local working tree
// — a git prefix lands as a phantom and is upgraded later by
// UpgradePhantomRepo when the user runs bacio inside the matching
// checkout, and a workspace is permanently pathless — so hard-wiring
// path to '' is both correct and unbreakable by a caller.
//
// createdAt / updatedAt are pre-formatted SQLite timestamp strings
// (the importer copies them verbatim from repo.yaml so a round-trip
// preserves history); pass "" to fall back to the column defaults.
func InsertPhantomRepoTx(
	tx *sql.Tx,
	uuid, prefix, name string,
	kind model.RepoKind,
	remoteURL string,
	nextIssueNumber int64,
	createdAt, updatedAt string,
) error {
	if tx == nil {
		return fmt.Errorf("InsertPhantomRepoTx: tx is required")
	}
	if uuid == "" || prefix == "" {
		return fmt.Errorf("InsertPhantomRepoTx: uuid and prefix are required")
	}
	if kind == "" {
		kind = model.RepoKindGit
	}
	if err := validateRepoKindPath(kind, ""); err != nil {
		return err
	}
	cols := `uuid, prefix, name, kind, path, remote_url, next_issue_number`
	vals := `?, ?, ?, ?, '', ?, ?`
	args := []any{uuid, prefix, name, string(kind), remoteURL, nextIssueNumber}
	if createdAt != "" {
		cols += `, created_at`
		vals += `, ?`
		args = append(args, createdAt)
	}
	if updatedAt != "" {
		cols += `, updated_at`
		vals += `, ?`
		args = append(args, updatedAt)
	}
	_, err := tx.Exec(`INSERT INTO repos (`+cols+`) VALUES (`+vals+`)`, args...)
	return err
}

// UpgradePhantomRepo populates `path` on a phantom row, turning it
// into a normal repo bound to a local working tree. Errors if the
// row isn't actually a phantom (path != '') so a caller hitting this
// against a real repo finds out loudly rather than silently
// overwriting the existing path.
//
// Bumps updated_at like every other mutation. uuid is the natural
// key here — CreatePhantomRepo wrote uuid from repo.yaml on import,
// and the upgrade flow looks it up by the same uuid.
//
// Refuses outright on a workspace. A workspace is also pathless, so
// the historical `path == ''` test alone would happily hand it a
// working tree and break the store-boundary invariant — the kind is
// read alongside the path and checked first, with a workspace-specific
// message rather than the git "not phantom" one.
func (s *Store) UpgradePhantomRepo(uuid, path string) error {
	if uuid == "" {
		return fmt.Errorf("UpgradePhantomRepo: uuid is required")
	}
	if path == "" {
		return fmt.Errorf("UpgradePhantomRepo: path must be non-empty (use the local working tree)")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		existing string
		kind     model.RepoKind
	)
	if err := tx.QueryRow(`SELECT path, kind FROM repos WHERE uuid = ?`, uuid).Scan(&existing, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := validateRepoKindPath(kind, path); err != nil {
		return fmt.Errorf("repo with uuid %s: %w", uuid, err)
	}
	if existing != "" {
		return fmt.Errorf("repo with uuid %s is not phantom (path=%q)", uuid, existing)
	}
	if _, err := tx.Exec(
		`UPDATE repos SET path = ?, updated_at = CURRENT_TIMESTAMP WHERE uuid = ?`,
		path, uuid,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateRepoByUUID applies fields from repo.yaml to an existing DB
// row by uuid. Used by import to apply inbound changes to name,
// remote_url, next_issue_number. path / id / created_at are
// client-state and never propagate from disk.
//
// Deliberately carries no (kind, path) guard: it writes neither column,
// so it cannot produce a pathed workspace. `kind` in particular is NOT
// updatable from repo.yaml — the sibling workspace.yaml sentinel is
// what marks a synced prefix as a workspace, and repo.yaml must stay
// byte-identical to what an older binary writes (see the A0 rule; a new
// key there hard-fails an older binary's whole sync run).
//
// Bumps updated_at when at least one field changed. Returns
// ErrNotFound if no row matches.
func (s *Store) UpdateRepoByUUID(uuid string, name, remoteURL *string, nextIssueNumber *int64) error {
	if uuid == "" {
		return fmt.Errorf("UpdateRepoByUUID: uuid is required")
	}
	sets := []string{}
	args := []any{}
	if name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *name)
	}
	if remoteURL != nil {
		sets = append(sets, "remote_url = ?")
		args = append(args, *remoteURL)
	}
	if nextIssueNumber != nil {
		sets = append(sets, "next_issue_number = ?")
		args = append(args, *nextIssueNumber)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, uuid)
	res, err := s.DB.Exec(
		fmt.Sprintf(`UPDATE repos SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecomputeNextIssueNumber pulls repos.next_issue_number forward to
// MAX(current value, MAX(issues.number) + 1). Called once per repo
// at the end of `bacio sync import` so a remotely-created MINI-50 (now
// in DB) doesn't get re-used locally the next time `bacio issue create`
// runs.
//
// We deliberately keep the *higher* of the cached counter and the
// max+1, rather than blindly resetting to max+1: if a repo with
// next_issue_number = 50 lost all its issues to a remote sweep, we
// don't want to start handing out MINI-1 again — that namespace is
// "owned" by the deletions and could collide if someone restores
// the deleted records on another machine. The high-water mark is
// the safe lower bound.
//
// The sync importer's upsertRepo enforces the same invariant for the
// remote side: it writes MAX(local_current, remote) so a peer pushing
// an older counter can't drag the local cache backwards. This
// function then handles the issues-MAX clamp on top of whatever value
// upsertRepo left behind.
//
// next_issue_number is advisory (the design doc downgraded it from
// a hard counter once collision handling exists), but it's a
// fast-path cache for the per-repo MAX query that issue create still
// relies on, so we keep it correct.
func (s *Store) RecomputeNextIssueNumber(repoID int64) error {
	var (
		maxNum  sql.NullInt64
		current int64
	)
	if err := s.DB.QueryRow(`SELECT MAX(number) FROM issues WHERE repo_id = ?`, repoID).Scan(&maxNum); err != nil {
		return err
	}
	if err := s.DB.QueryRow(`SELECT next_issue_number FROM repos WHERE id = ?`, repoID).Scan(&current); err != nil {
		return err
	}
	derived := int64(1)
	if maxNum.Valid {
		derived = maxNum.Int64 + 1
	}
	if derived <= current {
		// Already correct (or higher). Don't bump updated_at — no
		// state change.
		return nil
	}
	_, err := s.DB.Exec(
		`UPDATE repos SET next_issue_number = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		derived, repoID,
	)
	return err
}

// RepoCascadeCounts is the impact preview for `bacio repo rm` — every
// row that would disappear if DeleteRepo(id) were called.
//
// It must stay honest: this struct is what the confirm gate shows a
// human (and an agent) before an irreversible delete, so a table that
// cascades but isn't counted here is a silent surprise. Every table
// carrying `repo_id INTEGER NOT NULL REFERENCES repos(id) ON DELETE
// CASCADE` in schema.sql has a field below, plus the transitive
// issue-/feature-/document-owned rows and the FK-less history rows.
//
// Adding a new repo-scoped table? Add a field and a query here in the
// same change.
type RepoCascadeCounts struct {
	Issues        int `json:"issues"`
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	Tags          int `json:"tags"`
	Features      int `json:"features"`
	Documents     int `json:"documents"`
	DocumentLinks int `json:"document_links"`
	DocFolders    int `json:"doc_folders"`
	KanbanColumns int `json:"kanban_columns"`
	TUISettings   int `json:"tui_settings"`
	RepoSettings  int `json:"repo_settings"`
	// The agent-side rows. These were missing from the preview before
	// the workspaces pivot even though they have always cascaded: a
	// `repo rm` on an actively-worked repo silently took its session
	// history, queued dispatches, live channels, parked user messages
	// and unread notifications with it.
	AgentSessions   int `json:"agent_sessions"`
	AgentDispatches int `json:"agent_dispatches"`
	AgentChannels   int `json:"agent_channels"`
	UserMessages    int `json:"user_messages"`
	Notifications   int `json:"notifications"`
	History         int `json:"history"`
}

// RepoCascadeCountsForID returns counts for everything that would be
// deleted alongside the repo. The counts are read independently — no
// transaction — so concurrent writers can drift them slightly, which
// is fine for a preview.
func (s *Store) RepoCascadeCountsForID(repoID int64) (RepoCascadeCounts, error) {
	var c RepoCascadeCounts
	queries := []struct {
		dst *int
		sql string
	}{
		{&c.Issues, `SELECT COUNT(*) FROM issues WHERE repo_id = ?`},
		{&c.Features, `SELECT COUNT(*) FROM features WHERE repo_id = ?`},
		{&c.Documents, `SELECT COUNT(*) FROM documents WHERE repo_id = ?`},
		{&c.DocFolders, `SELECT COUNT(*) FROM doc_folders WHERE repo_id = ?`},
		{&c.KanbanColumns, `SELECT COUNT(*) FROM kanban_columns WHERE repo_id = ?`},
		{&c.TUISettings, `SELECT COUNT(*) FROM tui_settings WHERE repo_id = ?`},
		{&c.RepoSettings, `SELECT COUNT(*) FROM repo_settings WHERE repo_id = ?`},
		{&c.AgentSessions, `SELECT COUNT(*) FROM agent_sessions WHERE repo_id = ?`},
		{&c.AgentDispatches, `SELECT COUNT(*) FROM agent_dispatches WHERE repo_id = ?`},
		{&c.AgentChannels, `SELECT COUNT(*) FROM agent_channels WHERE repo_id = ?`},
		{&c.UserMessages, `SELECT COUNT(*) FROM user_messages WHERE repo_id = ?`},
		{&c.Notifications, `SELECT COUNT(*) FROM notifications WHERE repo_id = ?`},
		{&c.History, `SELECT COUNT(*) FROM history WHERE repo_id = ?`},
		{&c.Comments, `SELECT COUNT(*) FROM comments WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{&c.Tags, `SELECT COUNT(*) FROM issue_tags WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{&c.PullRequests, `SELECT COUNT(*) FROM issue_pull_requests WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{&c.Relations, `SELECT COUNT(*) FROM issue_relations WHERE from_issue_id IN (SELECT id FROM issues WHERE repo_id = ?) OR to_issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{&c.DocumentLinks, `SELECT COUNT(*) FROM document_links WHERE document_id IN (SELECT id FROM documents WHERE repo_id = ?) OR issue_id IN (SELECT id FROM issues WHERE repo_id = ?) OR feature_id IN (SELECT id FROM features WHERE repo_id = ?)`},
	}
	for _, q := range queries {
		// The relations query has two ? placeholders, the doc_links one
		// has three; everything else has one. Bind the same repoID
		// across however many ? marks the statement carries.
		nargs := strings.Count(q.sql, "?")
		args := make([]any, nargs)
		for i := range args {
			args[i] = repoID
		}
		if err := s.DB.QueryRow(q.sql, args...).Scan(q.dst); err != nil {
			return RepoCascadeCounts{}, fmt.Errorf("count: %w", err)
		}
	}
	return c, nil
}

// DeleteRepo drops a repo row, which cascades through every table
// that references repos(id) (features, issues, documents, doc_folders,
// kanban_columns, tui_settings, repo_settings, agent_sessions,
// agent_dispatches, agent_channels, user_messages, notifications) and
// transitively through everything below those (comments,
// feature_comments, issue_tags, issue_relations, issue_pull_requests,
// document_links, pipeline_jobs, agent session todos / questions).
// RepoCascadeCountsForID is the preview of the same blast radius —
// keep the two in step. History rows are NOT covered by the cascade —
// they're deliberately FK-less so audit entries survive normal
// deletes; the caller (bacio repo rm) is expected to call
// DeleteHistoryByRepo first when it wants the clean-slate behaviour.
func (s *Store) DeleteRepo(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM repos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRepos returns every repo — git repos (including phantoms) and
// workspaces alike — ordered by prefix. Signature deliberately
// unchanged by the workspaces pivot: it is the whole-DB list ~15 call
// sites depend on, and two of those (dispatcher.Backend at
// internal/dispatcher/dispatcher.go:18 and the registry store at
// internal/sync/registry.go:95) declare it in an interface, so any
// parameter here — variadic included — would break interface
// satisfaction across the tree. Kind filtering is a sibling verb:
// ListReposByKind.
func (s *Store) ListRepos() ([]*model.Repo, error) {
	return s.queryRepos(`SELECT ` + repoCols + ` FROM repos ORDER BY prefix`)
}

// ListReposByKind returns only the repos of the given kind, ordered by
// prefix — git repos (workspace pickers that must exclude them, the
// sync/dispatch paths that need a working tree) or workspaces (the
// picker's workspace group, the workspace-only CLI verbs).
//
// RepoKindGit matches the legacy empty string too, mirroring
// model.Repo.IsPhantom's `!IsWorkspace()` form: the column's DEFAULT is
// 'git' and '' means the same thing, so a git filter that tested
// `kind = 'git'` would drop any row written before / around the
// migration. Equivalently, "git" here means "not a workspace".
//
// An empty kind is accepted as git (same rule as everywhere else); an
// unrecognised one is a caller bug and errors rather than silently
// returning nothing.
func (s *Store) ListReposByKind(kind model.RepoKind) ([]*model.Repo, error) {
	switch kind {
	case model.RepoKindWorkspace:
		return s.queryRepos(`SELECT `+repoCols+` FROM repos WHERE kind = ? ORDER BY prefix`, string(model.RepoKindWorkspace))
	case model.RepoKindGit, "":
		return s.queryRepos(`SELECT `+repoCols+` FROM repos WHERE kind != ? ORDER BY prefix`, string(model.RepoKindWorkspace))
	default:
		return nil, fmt.Errorf("unknown repo kind %q (want %q or %q)", kind, model.RepoKindGit, model.RepoKindWorkspace)
	}
}

func (s *Store) queryRepos(query string, args ...any) ([]*model.Repo, error) {
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRepo(row rowScanner) (*model.Repo, error) {
	var r model.Repo
	err := row.Scan(&r.ID, &r.UUID, &r.Prefix, &r.Name, &r.Kind, &r.Path, &r.RemoteURL, &r.NextIssueNumber, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	// Normalise the legacy empty string to 'git' on the way out. Rows
	// migrated from a pre-pivot DB carry the column DEFAULT so they
	// already read 'git'; this covers a row written directly with '' and
	// guarantees the discriminator is never blank on the wire —
	// `bacio repo list -o json` and the REST/Wails repo shapes emit
	// Kind with no omitempty, so "" would leak to every consumer.
	if r.Kind == "" {
		r.Kind = model.RepoKindGit
	}
	return &r, nil
}
