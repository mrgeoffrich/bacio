package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	Repos     int `json:"repos"`
	Features  int `json:"features"`
	Issues    int `json:"issues"`
	Comments  int `json:"comments"`
	Documents int `json:"documents"`

	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	NoOp     int `json:"noop"`

	Renumbered []RenumberEntry `json:"renumbered,omitempty"`
	Renamed    []RenameEntry   `json:"renamed,omitempty"`
	Deleted    []DeletionEntry `json:"deleted,omitempty"`
	Dangling   []DanglingRef   `json:"dangling_refs,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
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
//  2. Resolve label collisions: for each kind, find DB rows whose
//     label matches an incoming label but whose uuid differs. Those
//     rows are local-only (otherwise git would have produced a
//     folder conflict). Renumber/rename them, append to
//     redirects.yaml, log audit ops.
//  3. Apply: walk every scanned record and dispatch on the
//     "DB row × sync_state row × seen-on-disk" case table.
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
	// Phase 4: deletions.
	if err := e.propagateDeletes(tx, scan, res); err != nil {
		return fmt.Errorf("propagate deletes: %w", err)
	}
	return nil
}

// scanWorkingTree walks `source/repos/<prefix>` and parses every
// record into memory. Phase 1 of the design doc — filesystem-only,
// no DB writes.
func (e *Engine) scanWorkingTree(ctx context.Context, source string) (*scanResult, error) {
	scan := &scanResult{
		repos: make(map[string]*scannedRepo),
		seenUUIDs: map[string]map[string]struct{}{
			store.SyncKindIssue:    {},
			store.SyncKindFeature:  {},
			store.SyncKindDocument: {},
			store.SyncKindComment:  {},
			store.SyncKindRepo:     {},
		},
	}
	reposRoot := filepath.Join(source, "repos")
	entries, err := os.ReadDir(reposRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// No repos folder — empty scan, not an error. The user
			// might be importing into a fresh sync repo with no
			// data yet.
			return scan, nil
		}
		return nil, fmt.Errorf("read repos dir: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		prefix := entry.Name()
		sr, err := e.scanRepoFolder(source, prefix, scan)
		if err != nil {
			return nil, fmt.Errorf("scan repo %s: %w", prefix, err)
		}
		if sr != nil {
			scan.repos[prefix] = sr
		}
	}
	return scan, nil
}

func (e *Engine) scanRepoFolder(source, prefix string, scan *scanResult) (*scannedRepo, error) {
	folder := RepoFolder(prefix)
	repoYAMLPath := filepath.Join(source, filepath.FromSlash(RepoYAMLFile(prefix)))
	repoBytes, err := os.ReadFile(repoYAMLPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A directory under repos/ without a repo.yaml is
			// suspicious — could be a stray folder. Skip rather
			// than error out, since import is best-effort.
			return nil, nil
		}
		return nil, fmt.Errorf("read repo.yaml: %w", err)
	}
	parsedRepo, err := ParseRepoYAML(repoBytes)
	if err != nil {
		return nil, err
	}
	scan.seenUUIDs[store.SyncKindRepo][parsedRepo.UUID] = struct{}{}

	redirects, err := LoadRedirects(source, prefix)
	if err != nil {
		return nil, fmt.Errorf("load redirects: %w", err)
	}

	sr := &scannedRepo{
		Prefix:    parsedRepo.Prefix,
		Parsed:    parsedRepo,
		Folder:    folder,
		Redirects: redirects,
		Features:  make(map[string]*scannedFeature),
		Issues:    make(map[string]*scannedIssue),
		Documents: make(map[string]*scannedDocument),
	}

	// Features.
	if err := e.scanFeatures(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	// Issues (and their comments).
	if err := e.scanIssues(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	// Documents.
	if err := e.scanDocuments(source, prefix, sr, scan); err != nil {
		return nil, err
	}
	return sr, nil
}

func (e *Engine) scanFeatures(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "features")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read features dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		folder := FeatureFolder(prefix, slug)
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(FeatureYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read feature %s: %w", slug, err)
		}
		parsed, err := ParseFeatureYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse feature %s: %w", slug, err)
		}
		body, err := readBody(filepath.Join(source, filepath.FromSlash(FeatureDescriptionFile(folder))))
		if err != nil {
			return err
		}
		sr.Features[parsed.UUID] = &scannedFeature{
			Parsed:      parsed,
			Folder:      folder,
			Description: string(body),
			BodyHash:    ContentHash(body),
		}
		scan.seenUUIDs[store.SyncKindFeature][parsed.UUID] = struct{}{}
	}
	return nil
}

func (e *Engine) scanIssues(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "issues")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read issues dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		label := entry.Name()
		// Issue folder names look like MINI-7. Number is parsed
		// from issue.yaml (the YAML is authoritative); the folder
		// name is just an aid.
		_ = label
		var folder = "repos/" + prefix + "/issues/" + label
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(IssueYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read issue %s: %w", label, err)
		}
		parsed, err := ParseIssueYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse issue %s: %w", label, err)
		}
		body, err := readBody(filepath.Join(source, filepath.FromSlash(IssueDescriptionFile(folder))))
		if err != nil {
			return err
		}
		si := &scannedIssue{
			Parsed:      parsed,
			Folder:      folder,
			Description: string(body),
			BodyHash:    ContentHash(body),
		}
		// Comments.
		commentsDir := filepath.Join(source, filepath.FromSlash(folder), "comments")
		commentEntries, err := os.ReadDir(commentsDir)
		if err == nil {
			for _, ce := range commentEntries {
				if ce.IsDir() {
					continue
				}
				name := ce.Name()
				if !strings.HasSuffix(name, ".yaml") {
					continue
				}
				yamlPath := filepath.Join(commentsDir, name)
				mdPath := strings.TrimSuffix(yamlPath, ".yaml") + ".md"
				cBytes, err := os.ReadFile(yamlPath)
				if err != nil {
					return fmt.Errorf("read comment %s: %w", name, err)
				}
				cParsed, err := ParseCommentYAML(cBytes)
				if err != nil {
					return fmt.Errorf("parse comment %s: %w", name, err)
				}
				cBody, err := readBody(mdPath)
				if err != nil {
					return err
				}
				si.Comments = append(si.Comments, &scannedComment{
					Parsed:   cParsed,
					YAMLPath: yamlPath,
					MDPath:   mdPath,
					Body:     string(cBody),
					BodyHash: ContentHash(cBody),
				})
				scan.seenUUIDs[store.SyncKindComment][cParsed.UUID] = struct{}{}
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read comments dir: %w", err)
		}
		sr.Issues[parsed.UUID] = si
		scan.seenUUIDs[store.SyncKindIssue][parsed.UUID] = struct{}{}
	}
	return nil
}

func (e *Engine) scanDocuments(source, prefix string, sr *scannedRepo, scan *scanResult) error {
	dir := filepath.Join(source, "repos", prefix, "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read docs dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		filename := entry.Name()
		folder := DocumentFolder(prefix, filename)
		yamlBytes, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(DocumentYAMLFile(folder))))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read doc %s: %w", filename, err)
		}
		parsed, err := ParseDocumentYAML(yamlBytes)
		if err != nil {
			return fmt.Errorf("parse doc %s: %w", filename, err)
		}
		body, err := readBody(filepath.Join(source, filepath.FromSlash(DocumentContentFile(folder))))
		if err != nil {
			return err
		}
		sr.Documents[parsed.UUID] = &scannedDocument{
			Parsed:      parsed,
			Folder:      folder,
			Content:     string(body),
			ContentHash: ContentHash(body),
		}
		scan.seenUUIDs[store.SyncKindDocument][parsed.UUID] = struct{}{}
	}
	return nil
}

// readBody reads a markdown sibling and normalises line endings to
// LF. A missing file is fine — empty body. Caller hashes the result.
func readBody(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	return NormalizeBody(b), nil
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

// resolveCollisions handles phase 2 — for each kind, if an incoming
// label maps to one uuid and the local DB already has a row using
// that label with a *different* uuid, the local row gives up the
// label per the "already-in-git wins" rule.
func (e *Engine) resolveCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	// Issues: collide on `(repo_id, number)`.
	incomingNumbers := map[int64]string{}
	for uuid, si := range sr.Issues {
		incomingNumbers[si.Parsed.Number] = uuid
	}
	if err := e.resolveIssueCollisions(tx, source, sr, repo, incomingNumbers, res, now); err != nil {
		return err
	}
	// Features: collide on `(repo_id, slug)`.
	incomingSlugs := map[string]string{}
	for uuid, sf := range sr.Features {
		incomingSlugs[sf.Parsed.Slug] = uuid
	}
	if err := e.resolveFeatureCollisions(tx, source, sr, repo, incomingSlugs, res, now); err != nil {
		return err
	}
	// Documents: collide on `(repo_id, filename)`.
	incomingFilenames := map[string]string{}
	for uuid, sd := range sr.Documents {
		incomingFilenames[sd.Parsed.Filename] = uuid
	}
	if err := e.resolveDocumentCollisions(tx, source, sr, repo, incomingFilenames, res, now); err != nil {
		return err
	}
	return nil
}

func (e *Engine) resolveIssueCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[int64]string, res *ImportResult, now time.Time) error {
	for number, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM issues WHERE repo_id = ? AND number = ?`,
			repo.ID, number,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue // same record, no collision
		}
		// Local row owns the number but isn't the just-imported
		// uuid — it must be local-only. Reallocate.
		newNumber, err := e.nextIssueNumberTx(tx, repo.ID, sr, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE issues SET number = ?, updated_at = ? WHERE id = ?`,
			newNumber, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		// Bump cached counter so subsequent local creates don't reuse.
		if _, err := tx.Exec(
			`UPDATE repos SET next_issue_number = MAX(next_issue_number, ? + 1), updated_at = ? WHERE id = ?`,
			newNumber, sqliteTimestamp(now), repo.ID,
		); err != nil {
			return err
		}
		oldLabel := fmt.Sprintf("%s-%d", repo.Prefix, number)
		newLabel := fmt.Sprintf("%s-%d", repo.Prefix, newNumber)
		// Append redirect.
		if !e.DryRun {
			r := Redirect{
				Kind:      "issue",
				Old:       oldLabel,
				New:       newLabel,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", oldLabel, err))
			}
		}
		res.Renumbered = append(res.Renumbered, RenumberEntry{
			Prefix:    repo.Prefix,
			UUID:      existingUUID,
			OldNumber: number,
			NewNumber: newNumber,
		})
	}
	return nil
}

// nextIssueNumberTx returns the smallest unused issue number for
// repo, skipping any number that's also in `incoming` (the imported
// set). Without that skip, the renumber could land on a value that
// the very next-applied incoming issue is about to claim.
func (e *Engine) nextIssueNumberTx(tx *sql.Tx, repoID int64, sr *scannedRepo, incoming map[int64]string) (int64, error) {
	var n sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(number) FROM issues WHERE repo_id = ?`, repoID).Scan(&n); err != nil {
		return 0, err
	}
	candidate := int64(1)
	if n.Valid {
		candidate = n.Int64 + 1
	}
	for {
		// Avoid colliding with another incoming issue.
		if _, taken := incoming[candidate]; !taken {
			return candidate, nil
		}
		candidate++
	}
}

func (e *Engine) resolveFeatureCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[string]string, res *ImportResult, now time.Time) error {
	for slug, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM features WHERE repo_id = ? AND slug = ?`,
			repo.ID, slug,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue
		}
		newSlug, err := e.nextFreeSlugTx(tx, repo.ID, slug, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE features SET slug = ?, updated_at = ? WHERE id = ?`,
			newSlug, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		if !e.DryRun {
			r := Redirect{
				Kind:      "feature",
				Old:       slug,
				New:       newSlug,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", slug, err))
			}
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   "feature",
			Prefix: repo.Prefix,
			UUID:   existingUUID,
			Old:    slug,
			New:    newSlug,
		})
	}
	return nil
}

// nextFreeSlugTx returns base-N (the smallest N >= 2) such that no
// feature in the repo uses it and it's not in `incoming`.
func (e *Engine) nextFreeSlugTx(tx *sql.Tx, repoID int64, base string, incoming map[string]string) (string, error) {
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := incoming[candidate]; taken {
			continue
		}
		var existing int64
		err := tx.QueryRow(
			`SELECT id FROM features WHERE repo_id = ? AND slug = ?`,
			repoID, candidate,
		).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func (e *Engine) resolveDocumentCollisions(tx *sql.Tx, source string, sr *scannedRepo, repo *model.Repo, incoming map[string]string, res *ImportResult, now time.Time) error {
	for filename, incomingUUID := range incoming {
		var (
			existingUUID string
			existingID   int64
		)
		err := tx.QueryRow(
			`SELECT uuid, id FROM documents WHERE repo_id = ? AND filename = ?`,
			repo.ID, filename,
		).Scan(&existingUUID, &existingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingUUID == incomingUUID {
			continue
		}
		newFilename, err := e.nextFreeFilenameTx(tx, repo.ID, filename, incoming)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE documents SET filename = ?, updated_at = ? WHERE id = ?`,
			newFilename, sqliteTimestamp(now), existingID,
		); err != nil {
			return err
		}
		if !e.DryRun {
			r := Redirect{
				Kind:      "document",
				Old:       filename,
				New:       newFilename,
				UUID:      existingUUID,
				ChangedAt: now,
				Reason:    ReasonLabelCollision,
			}
			if err := AppendRedirect(source, repo.Prefix, r); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("append redirect for %s: %v", filename, err))
			}
		}
		res.Renamed = append(res.Renamed, RenameEntry{
			Kind:   "document",
			Prefix: repo.Prefix,
			UUID:   existingUUID,
			Old:    filename,
			New:    newFilename,
		})
	}
	return nil
}

// nextFreeFilenameTx suffixes the basename with -N before the
// extension, e.g. auth-overview.md → auth-overview-2.md.
func (e *Engine) nextFreeFilenameTx(tx *sql.Tx, repoID int64, base string, incoming map[string]string) (string, error) {
	dot := strings.LastIndex(base, ".")
	stem, ext := base, ""
	if dot > 0 {
		stem, ext = base[:dot], base[dot:]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if _, taken := incoming[candidate]; taken {
			continue
		}
		var existing int64
		err := tx.QueryRow(
			`SELECT id FROM documents WHERE repo_id = ? AND filename = ?`,
			repoID, candidate,
		).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

// applyFeatures applies phase 3 to features.
func (e *Engine) applyFeatures(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	// Sort by uuid for deterministic processing order — important
	// for byte-stable test runs and reproducible audit logs.
	uuids := mapKeys(sr.Features)
	sort.Strings(uuids)
	for _, uuid := range uuids {
		sf := sr.Features[uuid]
		hash := contentHashFeature(sf)
		var existingID int64
		var existingSlug, existingTitle, existingDescription string
		err := tx.QueryRow(
			`SELECT id, slug, title, description FROM features WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingSlug, &existingTitle, &existingDescription)
		if errors.Is(err, sql.ErrNoRows) {
			// Insert.
			if _, err := tx.Exec(
				`INSERT INTO features (uuid, repo_id, slug, title, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sf.Parsed.Slug, sf.Parsed.Title, sf.Description,
				sqliteTimestamp(sf.Parsed.CreatedAt), sqliteTimestamp(sf.Parsed.UpdatedAt),
			); err != nil {
				return fmt.Errorf("insert feature %s: %w", sf.Parsed.Slug, err)
			}
			res.Inserted++
			if err := e.markSyncedTx(tx, uuid, store.SyncKindFeature, hash, now); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		// Update if any field differs.
		if existingSlug != sf.Parsed.Slug || existingTitle != sf.Parsed.Title || existingDescription != sf.Description {
			if _, err := tx.Exec(
				`UPDATE features SET slug = ?, title = ?, description = ?, updated_at = ? WHERE id = ?`,
				sf.Parsed.Slug, sf.Parsed.Title, sf.Description, sqliteTimestamp(sf.Parsed.UpdatedAt), existingID,
			); err != nil {
				return fmt.Errorf("update feature %s: %w", sf.Parsed.Slug, err)
			}
			res.Updated++
		} else {
			res.NoOp++
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindFeature, hash, now); err != nil {
			return err
		}
	}
	return nil
}

// applyIssues applies phase 3 to issues in two passes. Pass 1
// inserts/updates the issue rows themselves so every uuid mentioned
// in any other issue's `relations` block is already resolvable in
// the DB. Pass 2 walks the same set and applies the side-data:
// tags, PRs, and relations (the last of which is what required the
// split — a single-pass apply would mark perfectly-fine
// inter-issue links as dangling because the target hadn't been
// inserted yet).
//
// Feature references can stay in pass 1: applyFeatures already ran
// before applyIssues, so feature uuids are guaranteed resolvable.
func (e *Engine) applyIssues(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	uuids := mapKeys(sr.Issues)
	sort.Strings(uuids)
	// Track which issues were inserted (vs updated) so the
	// next_issue_number bump only fires when the issue is new.
	idByUUID := make(map[string]int64, len(uuids))

	// Pass 1: insert/update issue rows.
	for _, uuid := range uuids {
		si := sr.Issues[uuid]
		var featureID *int64
		if si.Parsed.Feature != nil && si.Parsed.Feature.UUID != "" {
			id, err := e.featureIDByUUIDTx(tx, si.Parsed.Feature.UUID)
			if err == nil {
				featureID = &id
			} else if errors.Is(err, store.ErrNotFound) {
				res.Dangling = append(res.Dangling, DanglingRef{
					From:        fmt.Sprintf("%s-%d", repo.Prefix, si.Parsed.Number),
					FromUUID:    uuid,
					Kind:        "feature",
					TargetLabel: si.Parsed.Feature.Label,
					TargetUUID:  si.Parsed.Feature.UUID,
				})
			} else {
				return err
			}
		}

		var (
			existingID          int64
			existingNumber      int64
			existingFeatureID   sql.NullInt64
			existingTitle       string
			existingDescription string
			existingState       string
			existingAssignee    string
		)
		err := tx.QueryRow(
			`SELECT id, number, feature_id, title, description, state, assignee FROM issues WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingNumber, &existingFeatureID, &existingTitle, &existingDescription, &existingState, &existingAssignee)
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, assignee, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, si.Parsed.Number, nullableInt64(featureID),
				si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee,
				sqliteTimestamp(si.Parsed.CreatedAt), sqliteTimestamp(si.Parsed.UpdatedAt),
			)
			if err != nil {
				return fmt.Errorf("insert issue %s: %w", uuid, err)
			}
			id, _ := res2.LastInsertId()
			idByUUID[uuid] = id
			res.Inserted++
		} else if err != nil {
			return err
		} else {
			// Skip the UPDATE if every field is already equal — this
			// keeps a re-import of an unchanged sync repo reporting
			// `noop` rather than `updated`. Side-data (tags, PRs,
			// relations) is checked separately in pass 2; we don't
			// gate the update on those because pass 2 deletes-and-
			// reinserts them wholesale and is its own no-op when the
			// sets match.
			changed := existingNumber != si.Parsed.Number ||
				!nullableEqualInt64(existingFeatureID, featureID) ||
				existingTitle != si.Parsed.Title ||
				existingDescription != si.Description ||
				existingState != si.Parsed.State ||
				existingAssignee != si.Parsed.Assignee
			if changed {
				if _, err := tx.Exec(
					`UPDATE issues SET number = ?, feature_id = ?, title = ?, description = ?, state = ?, assignee = ?, updated_at = ? WHERE id = ?`,
					si.Parsed.Number, nullableInt64(featureID),
					si.Parsed.Title, si.Description, si.Parsed.State, si.Parsed.Assignee,
					sqliteTimestamp(si.Parsed.UpdatedAt), existingID,
				); err != nil {
					return fmt.Errorf("update issue %s: %w", uuid, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
			idByUUID[uuid] = existingID
		}
	}

	// Pass 2: side-data (tags, PRs, relations). Every issue uuid
	// referenced by another issue's relations now has a row, so
	// resolveRelationsTx won't trip a false dangling.
	for _, uuid := range uuids {
		si := sr.Issues[uuid]
		issueID := idByUUID[uuid]
		hash := contentHashIssue(si)
		if err := e.replaceIssueTagsTx(tx, issueID, si.Parsed.Tags); err != nil {
			return fmt.Errorf("replace tags for %s: %w", uuid, err)
		}
		if err := e.replacePRsTx(tx, issueID, si.Parsed.PRs); err != nil {
			return fmt.Errorf("replace prs for %s: %w", uuid, err)
		}
		if err := e.replaceRelationsTx(tx, issueID, uuid, repo.Prefix, si.Parsed.Number, si.Parsed.Relations, res); err != nil {
			return fmt.Errorf("replace relations for %s: %w", uuid, err)
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindIssue, hash, now); err != nil {
			return err
		}
	}

	// Bump next_issue_number to the max number we just imported, if
	// higher than the cached value. We do this once per repo,
	// outside the per-issue loop, to avoid bumping repos.updated_at
	// on every iteration.
	maxNumber := int64(0)
	for _, uuid := range uuids {
		if n := sr.Issues[uuid].Parsed.Number; n > maxNumber {
			maxNumber = n
		}
	}
	if maxNumber > 0 {
		var current int64
		if err := tx.QueryRow(`SELECT next_issue_number FROM repos WHERE id = ?`, repo.ID).Scan(&current); err != nil {
			return err
		}
		if maxNumber+1 > current {
			if _, err := tx.Exec(
				`UPDATE repos SET next_issue_number = ?, updated_at = ? WHERE id = ?`,
				maxNumber+1, sqliteTimestamp(now), repo.ID,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) replaceIssueTagsTx(tx *sql.Tx, issueID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_tags WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_tags (issue_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	seen := map[string]bool{}
	for _, t := range tags {
		clean, err := store.NormalizeTag(t)
		if err != nil {
			return err
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if _, err := stmt.Exec(issueID, clean); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replacePRsTx(tx *sql.Tx, issueID int64, urls []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_pull_requests WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range urls {
		// We don't validate strictly here — the user could have
		// hand-edited the file. Schema cap is the only hard limit,
		// and the existing ValidatePRURLStrict would reject weird
		// URLs we'd rather just round-trip. The trade-off mirrors
		// the design doc's "files are authoritative" rule.
		if _, err := stmt.Exec(issueID, u); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replaceRelationsTx(tx *sql.Tx, fromID int64, fromUUID, fromPrefix string, fromNumber int64, rels ParsedRelations, res *ImportResult) error {
	if _, err := tx.Exec(`DELETE FROM issue_relations WHERE from_issue_id = ?`, fromID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	emit := func(refs []ParsedRef, kind model.RelationType) error {
		for _, r := range refs {
			toID, err := e.issueIDByUUIDTx(tx, r.UUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        fmt.Sprintf("%s-%d", fromPrefix, fromNumber),
						FromUUID:    fromUUID,
						Kind:        string(kind),
						TargetLabel: r.Label,
						TargetUUID:  r.UUID,
					})
					continue
				}
				return err
			}
			if toID == fromID {
				// Self-loop — schema CHECK forbids; skip silently.
				continue
			}
			if _, err := stmt.Exec(fromID, toID, string(kind)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := emit(rels.Blocks, model.RelBlocks); err != nil {
		return err
	}
	if err := emit(rels.RelatesTo, model.RelRelatesTo); err != nil {
		return err
	}
	if err := emit(rels.DuplicateOf, model.RelDuplicateOf); err != nil {
		return err
	}
	return nil
}

func (e *Engine) applyComments(tx *sql.Tx, sr *scannedRepo, res *ImportResult) error {
	now := time.Now().UTC()
	for issUUID, si := range sr.Issues {
		issueID, err := e.issueIDByUUIDTx(tx, issUUID)
		if err != nil {
			// Issue insert failed earlier? skip; we already accounted for it.
			continue
		}
		// Sort comments by uuid for stable processing order.
		sort.Slice(si.Comments, func(i, j int) bool {
			return si.Comments[i].Parsed.UUID < si.Comments[j].Parsed.UUID
		})
		for _, sc := range si.Comments {
			hash := contentHashComment(sc)
			var (
				existingID     int64
				existingAuthor string
				existingBody   string
			)
			err := tx.QueryRow(
				`SELECT id, author, body FROM comments WHERE uuid = ?`,
				sc.Parsed.UUID,
			).Scan(&existingID, &existingAuthor, &existingBody)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec(
					`INSERT INTO comments (uuid, issue_id, author, body, created_at) VALUES (?, ?, ?, ?, ?)`,
					sc.Parsed.UUID, issueID, sc.Parsed.Author, sc.Body,
					sqliteTimestamp(sc.Parsed.CreatedAt),
				); err != nil {
					return fmt.Errorf("insert comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Inserted++
			} else if err != nil {
				return err
			} else if existingAuthor != sc.Parsed.Author || existingBody != sc.Body {
				// Update body / author.
				if _, err := tx.Exec(
					`UPDATE comments SET author = ?, body = ? WHERE id = ?`,
					sc.Parsed.Author, sc.Body, existingID,
				); err != nil {
					return fmt.Errorf("update comment %s: %w", sc.Parsed.UUID, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
			if err := e.markSyncedTx(tx, sc.Parsed.UUID, store.SyncKindComment, hash, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) applyDocuments(tx *sql.Tx, sr *scannedRepo, repo *model.Repo, res *ImportResult) error {
	now := time.Now().UTC()
	uuids := mapKeys(sr.Documents)
	sort.Strings(uuids)
	for _, uuid := range uuids {
		sd := sr.Documents[uuid]
		hash := contentHashDocument(sd)
		var (
			existingID         int64
			existingFilename   string
			existingType       string
			existingContent    string
			existingSourcePath string
		)
		err := tx.QueryRow(
			`SELECT id, filename, type, content, source_path FROM documents WHERE uuid = ?`,
			uuid,
		).Scan(&existingID, &existingFilename, &existingType, &existingContent, &existingSourcePath)
		if errors.Is(err, sql.ErrNoRows) {
			res2, err := tx.Exec(
				`INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes, source_path, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid, repo.ID, sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
				sqliteTimestamp(sd.Parsed.CreatedAt), sqliteTimestamp(sd.Parsed.UpdatedAt),
			)
			if err != nil {
				return fmt.Errorf("insert document %s: %w", sd.Parsed.Filename, err)
			}
			id, _ := res2.LastInsertId()
			existingID = id
			res.Inserted++
		} else if err != nil {
			return err
		} else {
			// Same noop-vs-update gate as applyFeatures / applyIssues:
			// only run the UPDATE if any salient field actually
			// differs. Doc-link side-data is replaced wholesale below
			// regardless; that's its own no-op when sets match.
			changed := existingFilename != sd.Parsed.Filename ||
				existingType != sd.Parsed.Type ||
				existingContent != sd.Content ||
				existingSourcePath != sd.Parsed.SourcePath
			if changed {
				if _, err := tx.Exec(
					`UPDATE documents SET filename = ?, type = ?, content = ?, size_bytes = ?, source_path = ?, updated_at = ? WHERE id = ?`,
					sd.Parsed.Filename, sd.Parsed.Type, sd.Content, len(sd.Content), sd.Parsed.SourcePath,
					sqliteTimestamp(sd.Parsed.UpdatedAt), existingID,
				); err != nil {
					return fmt.Errorf("update document %s: %w", sd.Parsed.Filename, err)
				}
				res.Updated++
			} else {
				res.NoOp++
			}
		}
		// Replace links wholesale.
		if err := e.replaceDocLinksTx(tx, existingID, sd.Parsed.Filename, uuid, sd.Parsed.Links, res); err != nil {
			return fmt.Errorf("replace doc links for %s: %w", sd.Parsed.Filename, err)
		}
		if err := e.markSyncedTx(tx, uuid, store.SyncKindDocument, hash, now); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replaceDocLinksTx(tx *sql.Tx, docID int64, docFilename, docUUID string, links []ParsedDocLink, res *ImportResult) error {
	if _, err := tx.Exec(`DELETE FROM document_links WHERE document_id = ?`, docID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, l := range links {
		switch l.Kind {
		case "issue":
			id, err := e.issueIDByUUIDTx(tx, l.TargetUUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        docFilename,
						FromUUID:    docUUID,
						Kind:        "doc_link",
						TargetLabel: l.TargetLabel,
						TargetUUID:  l.TargetUUID,
					})
					continue
				}
				return err
			}
			if _, err := stmt.Exec(docID, id, nil, ""); err != nil {
				return err
			}
		case "feature":
			id, err := e.featureIDByUUIDTx(tx, l.TargetUUID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					res.Dangling = append(res.Dangling, DanglingRef{
						From:        docFilename,
						FromUUID:    docUUID,
						Kind:        "doc_link",
						TargetLabel: l.TargetLabel,
						TargetUUID:  l.TargetUUID,
					})
					continue
				}
				return err
			}
			if _, err := stmt.Exec(docID, nil, id, ""); err != nil {
				return err
			}
		default:
			res.Warnings = append(res.Warnings, fmt.Sprintf("unknown doc_link kind %q on %s", l.Kind, docFilename))
		}
	}
	return nil
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
	return ContentHash([]byte(fmt.Sprintf("feature|%s|%s|%s|%s",
		sf.Parsed.UUID, sf.Parsed.Slug, sf.Parsed.Title, sf.BodyHash)))
}

func contentHashIssue(si *scannedIssue) string {
	return ContentHash([]byte(fmt.Sprintf("issue|%s|%d|%s|%s|%s|%s|%s",
		si.Parsed.UUID, si.Parsed.Number, si.Parsed.State, si.Parsed.Assignee,
		si.Parsed.Title, si.BodyHash, strings.Join(si.Parsed.Tags, ","))))
}

func contentHashComment(sc *scannedComment) string {
	return ContentHash([]byte(fmt.Sprintf("comment|%s|%s|%s",
		sc.Parsed.UUID, sc.Parsed.Author, sc.BodyHash)))
}

func contentHashDocument(sd *scannedDocument) string {
	return ContentHash([]byte(fmt.Sprintf("document|%s|%s|%s|%s",
		sd.Parsed.UUID, sd.Parsed.Filename, sd.Parsed.Type, sd.ContentHash)))
}
