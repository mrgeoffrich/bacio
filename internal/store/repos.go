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

const repoCols = `id, uuid, prefix, name, path, remote_url, next_issue_number, created_at, updated_at`

func (s *Store) CreateRepo(prefix, name, path, remoteURL string) (*model.Repo, error) {
	res, err := s.DB.Exec(
		`INSERT INTO repos (uuid, prefix, name, path, remote_url) VALUES (?, ?, ?, ?, ?)`,
		identity.New(), prefix, name, path, remoteURL,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetRepoByID(id)
}

func (s *Store) GetRepoByID(id int64) (*model.Repo, error) {
	return scanRepo(s.DB.QueryRow(`SELECT `+repoCols+` FROM repos WHERE id = ?`, id))
}

func (s *Store) GetRepoByPath(path string) (*model.Repo, error) {
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
func (s *Store) CreatePhantomRepo(uuid, prefix, name, remoteURL string) (*model.Repo, error) {
	if uuid == "" || prefix == "" {
		return nil, fmt.Errorf("CreatePhantomRepo: uuid and prefix are required")
	}
	res, err := s.DB.Exec(
		`INSERT INTO repos (uuid, prefix, name, path, remote_url) VALUES (?, ?, ?, '', ?)`,
		uuid, prefix, name, remoteURL,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetRepoByID(id)
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
	var existing string
	if err := tx.QueryRow(`SELECT path FROM repos WHERE uuid = ?`, uuid).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
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
type RepoCascadeCounts struct {
	Issues        int `json:"issues"`
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	Tags          int `json:"tags"`
	Features      int `json:"features"`
	Documents     int `json:"documents"`
	DocumentLinks int `json:"document_links"`
	TUISettings   int `json:"tui_settings"`
	History       int `json:"history"`
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
		{&c.TUISettings, `SELECT COUNT(*) FROM tui_settings WHERE repo_id = ?`},
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
// that references repos(id) (features, issues, documents,
// tui_settings) and transitively through everything below those
// (comments, issue_tags, issue_relations, issue_pull_requests,
// document_links). History rows are NOT covered by the cascade —
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

func (s *Store) ListRepos() ([]*model.Repo, error) {
	rows, err := s.DB.Query(`SELECT ` + repoCols + ` FROM repos ORDER BY prefix`)
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
	err := row.Scan(&r.ID, &r.UUID, &r.Prefix, &r.Name, &r.Path, &r.RemoteURL, &r.NextIssueNumber, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan repo: %w", err)
	}
	return &r, nil
}
