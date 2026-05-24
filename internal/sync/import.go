package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ImportResult is the structured summary of one Import pass. The
// counts and lists are populated as the four-phase pipeline runs;
// `--output json` serialises the whole struct so an agent can react
// to renumbers / dangling refs / deletions without re-parsing
// human text.
//
// "Touched" counts cover any record the importer either inserted,
// updated, or no-op'd. "Renumbered" / "renamed" / "deleted" are the
// observable side-effects worth surfacing.
type ImportResult struct {
	Source string `json:"source"`
	DryRun bool   `json:"dry_run,omitempty"`

	Repos    int `json:"repos"`
	Features int `json:"features"`
	Issues   int `json:"issues"`
	// Comments counts issue-scoped comments seen on disk. Kept
	// issue-only so pinned-output tests stay green; BACI-124 feature
	// comments roll up into FeatureComments.
	Comments        int `json:"comments"`
	FeatureComments int `json:"feature_comments"`
	Documents       int `json:"documents"`

	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	NoOp     int `json:"noop"`
	// Skipped counts records whose remote YAML carried an older
	// `updated_at` than the matching local DB row. Per the
	// last-writer-wins contract advertised in SKILL.md § Git-backed
	// sync, the importer preserves the newer local version instead of
	// silently downgrading. The export phase on the same run writes
	// the local content back to YAML so the loop closes naturally.
	Skipped      int                 `json:"skipped,omitempty"`
	SkippedStale []SkippedStaleEntry `json:"skipped_stale,omitempty"`

	Renumbered []RenumberEntry `json:"renumbered,omitempty"`
	Renamed    []RenameEntry   `json:"renamed,omitempty"`
	Deleted    []DeletionEntry `json:"deleted,omitempty"`
	Dangling   []DanglingRef   `json:"dangling_refs,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
}

// SkippedStaleEntry records one record whose remote YAML's
// `updated_at` was older than the local DB row's. The import phase
// preserved the local row and left sync_state untouched so the next
// run re-evaluates; the export phase writes the newer local content
// out on this same run.
type SkippedStaleEntry struct {
	Kind          string `json:"kind"` // "issue" | "feature" | "document"
	UUID          string `json:"uuid"`
	Label         string `json:"label,omitempty"` // issue key, feature slug, or doc filename
	LocalUpdated  string `json:"local_updated_at"`
	RemoteUpdated string `json:"remote_updated_at"`
}

// RenumberEntry records one issue renumber driven by the
// collision-resolution phase.
type RenumberEntry struct {
	Prefix    string `json:"prefix"`
	UUID      string `json:"uuid"`
	OldNumber int64  `json:"old_number"`
	NewNumber int64  `json:"new_number"`
}

// RenameEntry records one feature/document rename driven by the
// collision-resolution phase.
type RenameEntry struct {
	Kind   string `json:"kind"` // "feature" | "document"
	Prefix string `json:"prefix"`
	UUID   string `json:"uuid"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// DeletionEntry records one record dropped because its uuid was
// previously synced but didn't appear in the latest scan.
type DeletionEntry struct {
	Kind  string `json:"kind"`
	UUID  string `json:"uuid"`
	Label string `json:"label,omitempty"`
}

// DanglingRef describes a cross-reference whose target uuid wasn't
// resolvable on import. The reference is left in place on disk; we
// just don't write a DB-side edge.
type DanglingRef struct {
	From      string `json:"from"`       // e.g. "MINI-7"
	FromUUID  string `json:"from_uuid"`
	Kind      string `json:"kind"`       // "blocks", "feature", "doc_link"...
	TargetLabel string `json:"target_label"`
	TargetUUID  string `json:"target_uuid"`
}

// scanResult is the in-memory output of phase 1. We keep it tightly
// scoped to the import package — none of this needs to leak through
// the public API.
type scanResult struct {
	repos map[string]*scannedRepo // by prefix

	// seenUUIDs[kind] is the set of uuids found on disk this pass.
	// Used by phase 4 to detect deletions.
	seenUUIDs map[string]map[string]struct{}
}

type scannedRepo struct {
	Prefix    string
	Parsed    *ParsedRepo
	Folder    string // sync-repo-relative path "repos/<prefix>"
	Redirects []Redirect

	Features  map[string]*scannedFeature  // by uuid
	Issues    map[string]*scannedIssue    // by uuid
	Documents map[string]*scannedDocument // by uuid
	// Comments are scanned per-issue; they live inside scannedIssue.
}

type scannedFeature struct {
	Parsed      *ParsedFeature
	Folder      string
	Description string
	BodyHash    string
	// Comments holds the BACI-124 feature-scoped comments scanned from
	// <featureFolder>/comments/. Same shape as scannedIssue.Comments.
	Comments []*scannedFeatureComment
}

// scannedFeatureComment is the parsed on-disk feature comment (BACI-124).
// Identical YAML / MD schema to scannedComment — the type is distinct so
// the apply pass keys off the right table.
type scannedFeatureComment struct {
	Parsed   *ParsedComment
	YAMLPath string
	MDPath   string
	Body     string
	BodyHash string
}

type scannedIssue struct {
	Parsed      *ParsedIssue
	Folder      string
	Description string
	BodyHash    string
	Comments    []*scannedComment
}

type scannedComment struct {
	Parsed   *ParsedComment
	YAMLPath string
	MDPath   string
	Body     string
	BodyHash string
}

type scannedDocument struct {
	Parsed      *ParsedDocument
	Folder      string
	Content     string
	ContentHash string
}

// Import reads `source` (a sync-repo working tree) and applies it to
// the local DB. The four phases of the design doc run in one outer
// SQLite transaction so a partial failure rolls back cleanly:
//
//  1. Scan: walk repos/<prefix>/ trees and parse every record into
//     memory. Build seenUUIDs[kind] and incoming-labels maps.
//     (import_scan.go)
//  2. Resolve label collisions: for each kind, find DB rows whose
//     label matches an incoming label but whose uuid differs. Those
//     rows are local-only (otherwise git would have produced a
//     folder conflict). Renumber/rename them, append to
//     redirects.yaml, log audit ops. (import_collisions.go)
//  3. Apply: walk every scanned record and dispatch on the
//     "DB row × sync_state row × seen-on-disk" case table.
//     (import_apply.go)
//  4. Detect deletions: every uuid in sync_state that wasn't seen
//     on disk → propagate the delete and drop the sync_state row.
//
// After phase 4, RecomputeNextIssueNumber runs once per repo so a
// remotely-imported MINI-50 doesn't get re-used locally.
//
// DryRun rolls back the outer transaction before commit so the user
// sees what would happen without permanent effect; the result is
// still populated.
func (e *Engine) Import(ctx context.Context, source string) (*ImportResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("sync.Import: Store is nil")
	}
	if source == "" {
		return nil, fmt.Errorf("sync.Import: source path is empty")
	}
	res := &ImportResult{Source: source, DryRun: e.DryRun}

	scan, err := e.scanWorkingTree(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	res.Repos = len(scan.repos)
	for _, sr := range scan.repos {
		res.Features += len(sr.Features)
		res.Issues += len(sr.Issues)
		res.Documents += len(sr.Documents)
		for _, si := range sr.Issues {
			res.Comments += len(si.Comments)
		}
		for _, sf := range sr.Features {
			res.FeatureComments += len(sf.Comments)
		}
	}

	tx, err := e.Store.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := e.applyImport(ctx, tx, source, scan, res); err != nil {
		return nil, err
	}

	if e.DryRun {
		// Roll back rather than commit; the result still tells the
		// user what would have happened.
		return res, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true

	// next_issue_number recompute runs outside the import txn — it
	// reads the just-committed state and updates a derived counter.
	for _, sr := range scan.repos {
		repo, err := e.Store.GetRepoByUUID(sr.Parsed.UUID)
		if err != nil {
			continue
		}
		if err := e.Store.RecomputeNextIssueNumber(repo.ID); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("recompute next_issue_number for %s: %v", repo.Prefix, err))
		}
	}
	return res, nil
}

// applyImport runs phases 2-4 inside the outer transaction. Phase 1
// already happened in scanWorkingTree (filesystem-only).
func (e *Engine) applyImport(ctx context.Context, tx *sql.Tx, source string, scan *scanResult, res *ImportResult) error {
	// Phase 2 (collisions) and Phase 3 (apply) need a uuid-keyed view
	// of every existing DB row of the relevant kinds. We populate
	// these maps inside the transaction so a concurrent writer
	// doesn't mutate them mid-import.
	for _, sr := range scan.repos {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Resolve / create the repo first so cross-references inside
		// the repo can use repo.ID.
		repo, err := e.upsertRepo(tx, sr, res)
		if err != nil {
			return fmt.Errorf("repo %s: %w", sr.Prefix, err)
		}
		// Phase 2: resolve label collisions.
		if err := e.resolveCollisions(tx, source, sr, repo, res); err != nil {
			return fmt.Errorf("resolve collisions for %s: %w", sr.Prefix, err)
		}
		// Phase 3: apply features, then issues (so feature-id
		// resolution works), then comments, then documents.
		if err := e.applyFeatures(tx, sr, repo, res); err != nil {
			return fmt.Errorf("apply features for %s: %w", sr.Prefix, err)
		}
		// BACI-124 feature comments live under their feature folder, so
		// apply them immediately after features (before issues) — the
		// foreign key resolves to the just-applied feature row.
		if err := e.applyFeatureComments(tx, sr, res); err != nil {
			return fmt.Errorf("apply feature comments for %s: %w", sr.Prefix, err)
		}
		if err := e.applyIssues(tx, sr, repo, res); err != nil {
			return fmt.Errorf("apply issues for %s: %w", sr.Prefix, err)
		}
		if err := e.applyComments(tx, sr, res); err != nil {
			return fmt.Errorf("apply comments for %s: %w", sr.Prefix, err)
		}
		if err := e.applyDocuments(tx, sr, repo, res); err != nil {
			return fmt.Errorf("apply documents for %s: %w", sr.Prefix, err)
		}
	}
	// Phase 4: deletions. Bootstrap flows (sync init attach, sync
	// clone) skip this — they treat import as an additive merge so
	// local-only records aren't wiped when the sync-repo working
	// tree doesn't yet reflect them. Steady-state sync leaves the
	// flag false and runs propagateDeletes normally.
	if !e.SkipPropagateDeletes {
		if err := e.propagateDeletes(tx, scan, res); err != nil {
			return fmt.Errorf("propagate deletes: %w", err)
		}
	}
	return nil
}

// upsertRepo resolves the parsed repo to a *model.Repo, creating a
// phantom row when no DB row matches the uuid. Real (path != '')
// repos are upgraded if they were previously phantom; otherwise we
// just patch name/remote_url/next_issue_number from the file.
//
// Returns the resolved repo so per-record passes can use repo.ID.
func (e *Engine) upsertRepo(tx *sql.Tx, sr *scannedRepo, res *ImportResult) (*model.Repo, error) {
	parsed := sr.Parsed
	existing, err := e.getRepoByUUIDTx(tx, parsed.UUID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	now := time.Now().UTC()
	hash := contentHashRepo(parsed)
	if existing == nil {
		// No DB row for this uuid. Check for prefix conflict — if
		// some other repo already owns this prefix locally with a
		// different uuid, that's the catastrophic-merge case from
		// the design doc; refuse rather than silently corrupt.
		var conflictUUID sql.NullString
		err := tx.QueryRow(`SELECT uuid FROM repos WHERE prefix = ?`, parsed.Prefix).Scan(&conflictUUID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if conflictUUID.Valid && conflictUUID.String != parsed.UUID {
			return nil, fmt.Errorf("prefix %q is already used by repo %s locally; refusing to merge with uuid %s",
				parsed.Prefix, conflictUUID.String, parsed.UUID)
		}
		// Insert a phantom row. The CLI doesn't have a "current
		// working tree" for an arbitrary imported prefix, so path
		// stays empty — resolveRepo() will only pick a phantom up
		// once the user runs bacio from inside the matching tree.
		// created_at / updated_at come from repo.yaml so the
		// round-trip preserves history.
		if _, err := tx.Exec(
			`INSERT INTO repos (uuid, prefix, name, path, remote_url, next_issue_number, created_at, updated_at) VALUES (?, ?, ?, '', ?, ?, ?, ?)`,
			parsed.UUID, parsed.Prefix, parsed.Name, parsed.RemoteURL, parsed.NextIssueNumber,
			sqliteTimestamp(parsed.CreatedAt), sqliteTimestamp(parsed.UpdatedAt),
		); err != nil {
			return nil, fmt.Errorf("insert phantom repo %s: %w", parsed.Prefix, err)
		}
		res.Inserted++
		repo, err := e.getRepoByUUIDTx(tx, parsed.UUID)
		if err != nil {
			return nil, err
		}
		if err := e.markSyncedTx(tx, parsed.UUID, store.SyncKindRepo, hash, now); err != nil {
			return nil, err
		}
		return repo, nil
	}
	// DB row exists. Patch fields that may have shifted on the
	// remote: name, remote_url, next_issue_number.
	if existing.Name != parsed.Name || existing.RemoteURL != parsed.RemoteURL || existing.NextIssueNumber != parsed.NextIssueNumber {
		if _, err := tx.Exec(
			`UPDATE repos SET name = ?, remote_url = ?, next_issue_number = ?, updated_at = ? WHERE uuid = ?`,
			parsed.Name, parsed.RemoteURL, parsed.NextIssueNumber, sqliteTimestamp(now), parsed.UUID,
		); err != nil {
			return nil, fmt.Errorf("update repo %s: %w", parsed.Prefix, err)
		}
		res.Updated++
	} else {
		res.NoOp++
	}
	if err := e.markSyncedTx(tx, parsed.UUID, store.SyncKindRepo, hash, now); err != nil {
		return nil, err
	}
	return existing, nil
}

// propagateDeletes is phase 4. Iterate every uuid in sync_state by
// kind; if it isn't in scan.seenUUIDs[kind], drop the DB row and the
// sync_state row.
func (e *Engine) propagateDeletes(tx *sql.Tx, scan *scanResult, res *ImportResult) error {
	kinds := []struct {
		Name string
		Tbl  string
	}{
		{store.SyncKindComment, "comments"},
		{store.SyncKindFeatureComment, "feature_comments"},
		{store.SyncKindIssue, "issues"},
		{store.SyncKindFeature, "features"},
		{store.SyncKindDocument, "documents"},
	}
	for _, k := range kinds {
		rows, err := tx.Query(`SELECT uuid FROM sync_state WHERE kind = ?`, k.Name)
		if err != nil {
			return err
		}
		var uuids []string
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			uuids = append(uuids, u)
		}
		rows.Close()
		for _, u := range uuids {
			if _, seen := scan.seenUUIDs[k.Name][u]; seen {
				continue
			}
			label, _ := e.fetchLabelForUUIDTx(tx, k.Tbl, u)
			if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE uuid = ?`, k.Tbl), u); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM sync_state WHERE uuid = ?`, u); err != nil {
				return err
			}
			res.Deleted = append(res.Deleted, DeletionEntry{
				Kind:  k.Name,
				UUID:  u,
				Label: label,
			})
		}
	}
	return nil
}

// Helpers ----------------------------------------------------------

// markSyncedTx wraps the store-side helper so the sync package can
// keep its hash-related logic colocated with the rest of the
// pipeline.
func (e *Engine) markSyncedTx(tx *sql.Tx, uuid, kind, hash string, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO sync_state (uuid, kind, last_synced_at, last_synced_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			kind = excluded.kind,
			last_synced_at = excluded.last_synced_at,
			last_synced_hash = excluded.last_synced_hash`,
		uuid, kind, sqliteTimestamp(now), hash,
	)
	return err
}

func (e *Engine) issueIDByUUIDTx(tx *sql.Tx, uuid string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM issues WHERE uuid = ?`, uuid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	return id, err
}

func (e *Engine) featureIDByUUIDTx(tx *sql.Tx, uuid string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM features WHERE uuid = ?`, uuid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	return id, err
}

func (e *Engine) getRepoByUUIDTx(tx *sql.Tx, uuid string) (*model.Repo, error) {
	var r model.Repo
	err := tx.QueryRow(
		`SELECT id, uuid, prefix, name, path, remote_url, next_issue_number, created_at, updated_at FROM repos WHERE uuid = ?`,
		uuid,
	).Scan(&r.ID, &r.UUID, &r.Prefix, &r.Name, &r.Path, &r.RemoteURL, &r.NextIssueNumber, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// fetchLabelForUUIDTx returns the human-friendly label for a uuid in
// the given table — used for the deletion audit log so the user
// sees "deleted MINI-7" rather than just a uuid.
func (e *Engine) fetchLabelForUUIDTx(tx *sql.Tx, table, uuid string) (string, error) {
	switch table {
	case "issues":
		var prefix string
		var number int64
		err := tx.QueryRow(`
			SELECT r.prefix, i.number
			FROM issues i
			JOIN repos r ON r.id = i.repo_id
			WHERE i.uuid = ?`, uuid).Scan(&prefix, &number)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%d", prefix, number), nil
	case "features":
		var slug string
		err := tx.QueryRow(`SELECT slug FROM features WHERE uuid = ?`, uuid).Scan(&slug)
		if err != nil {
			return "", err
		}
		return slug, nil
	case "documents":
		var filename string
		err := tx.QueryRow(`SELECT filename FROM documents WHERE uuid = ?`, uuid).Scan(&filename)
		if err != nil {
			return "", err
		}
		return filename, nil
	case "comments":
		// Comments don't have a friendly label; fall back to uuid.
		return "", nil
	}
	return "", nil
}

// sqliteTimestamp formats a time.Time as SQLite's preferred
// `YYYY-MM-DD HH:MM:SS` string, in UTC. Stays consistent with the
// existing CURRENT_TIMESTAMP defaults.
func sqliteTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullableEqualInt64 compares a nullable DB column to a Go *int64.
// Two nulls are equal; a null and a present value are not. Used by
// applyIssues' "did anything change?" check so a re-import of an
// unchanged feature link reports `noop` rather than `updated`.
func nullableEqualInt64(db sql.NullInt64, want *int64) bool {
	if !db.Valid {
		return want == nil
	}
	if want == nil {
		return false
	}
	return db.Int64 == *want
}

// nullableSqliteTimestamp formats a *time.Time as the SQLite-flavoured
// string or returns nil so the driver writes a SQL NULL. Used to round-
// trip archived_at on the BACI-68 sync path.
func nullableSqliteTimestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return sqliteTimestamp(*t)
}

// nullableTimeEqual compares a nullable DB column to a Go *time.Time.
// Two nulls are equal; a null and a present value are not. Used by the
// sync apply-step's "did archived_at change?" check.
func nullableTimeEqual(db sql.NullTime, want *time.Time) bool {
	if !db.Valid {
		return want == nil
	}
	if want == nil {
		return false
	}
	return db.Time.Equal(*want)
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Content hashes per record kind. The export side computes these as
// "hash of canonical YAML"; for sync_state we only need a stable
// identity per record so we just hash the same canonical bytes
// here too. This intentionally diverges from the export-side hash
// computation because import doesn't re-emit YAML — we hash the
// parsed struct's salient fields instead.

func contentHashRepo(p *ParsedRepo) string {
	return ContentHash([]byte(fmt.Sprintf("repo|%s|%s|%s|%s|%d", p.UUID, p.Prefix, p.Name, p.RemoteURL, p.NextIssueNumber)))
}

func contentHashFeature(sf *scannedFeature) string {
	return ContentHash([]byte(fmt.Sprintf("feature|%s|%s|%s|%s|%s",
		sf.Parsed.UUID, sf.Parsed.Slug, sf.Parsed.Title, sf.BodyHash,
		hashableArchived(sf.Parsed.ArchivedAt))))
}

func contentHashIssue(si *scannedIssue) string {
	return ContentHash([]byte(fmt.Sprintf("issue|%s|%d|%s|%s|%s|%s|%s|%s",
		si.Parsed.UUID, si.Parsed.Number, si.Parsed.State, si.Parsed.Assignee,
		si.Parsed.Title, si.BodyHash, strings.Join(si.Parsed.Tags, ","),
		hashableArchived(si.Parsed.ArchivedAt))))
}

func contentHashComment(sc *scannedComment) string {
	return ContentHash([]byte(fmt.Sprintf("comment|%s|%s|%s",
		sc.Parsed.UUID, sc.Parsed.Author, sc.BodyHash)))
}

// contentHashFeatureComment distinguishes feature comments from issue
// comments in the sync_state hash space (BACI-124). The kind=feature_comment
// prefix means a colliding uuid between an issue comment and a feature
// comment (astronomically unlikely with UUIDv7, but defensive) would
// surface as a hash change rather than a silent no-op.
func contentHashFeatureComment(sc *scannedFeatureComment) string {
	return ContentHash([]byte(fmt.Sprintf("feature_comment|%s|%s|%s",
		sc.Parsed.UUID, sc.Parsed.Author, sc.BodyHash)))
}

func contentHashDocument(sd *scannedDocument) string {
	return ContentHash([]byte(fmt.Sprintf("document|%s|%s|%s|%s|%s",
		sd.Parsed.UUID, sd.Parsed.Filename, sd.Parsed.Type, sd.ContentHash,
		hashableArchived(sd.Parsed.ArchivedAt))))
}

// hashableArchived stringifies *time.Time for inclusion in a content
// hash. Empty for a live row, RFC3339 UTC for an archived one, so
// flipping the flag in either direction changes the hash and the next
// markSynced writes the new value.
func hashableArchived(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
