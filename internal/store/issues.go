package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

var issueKeyRe = regexp.MustCompile(`^([A-Za-z0-9]{4})-(\d+)$`)

// ParseIssueKey splits "MINI-42" into prefix and number.
func ParseIssueKey(key string) (prefix string, number int64, err error) {
	m := issueKeyRe.FindStringSubmatch(strings.TrimSpace(key))
	if m == nil {
		return "", 0, fmt.Errorf("invalid issue key %q (expected like MINI-42)", key)
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, err
	}
	return strings.ToUpper(m[1]), n, nil
}

// CreateIssue is fully atomic: the counter peek, INSERT, tag writes, and
// counter bump all live in a single transaction. We deliberately bump the
// counter LAST (right before Commit) so that any failure in the issue or
// tag inserts means the counter never even gets touched on disk — that
// way a phantom-number gap requires a Commit to actually succeed, which
// only happens when every preceding step succeeded.
func (s *Store) CreateIssue(repoID int64, featureID *int64, title, description string, state model.State, tags []string) (*model.Issue, error) {
	title, err := ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var num int64
	if err := tx.QueryRow(`SELECT next_issue_number FROM repos WHERE id = ?`, repoID).Scan(&num); err != nil {
		return nil, err
	}

	res, err := tx.Exec(
		`INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		identity.New(), repoID, num, nullableInt(featureID), title, description, string(state),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := s.addTagsTx(tx, id, tags); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`UPDATE repos SET next_issue_number = next_issue_number + 1 WHERE id = ?`, repoID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

func (s *Store) GetIssueByID(id int64) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+` WHERE i.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return s.attachTags(iss)
}

func (s *Store) GetIssueByKey(prefix string, number int64) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+` WHERE r.prefix = ? AND i.number = ?`, prefix, number))
	if err != nil {
		return nil, err
	}
	return s.attachTags(iss)
}

// GetIssueByUUID is the sync-side lookup: import resolves cross-references
// (blocks/relates_to/duplicate_of, doc links, etc.) and incoming records
// by uuid, never by the (mutable) issue key.
func (s *Store) GetIssueByUUID(uuid string) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+` WHERE i.uuid = ?`, uuid))
	if err != nil {
		return nil, err
	}
	return s.attachTags(iss)
}

func (s *Store) attachTags(iss *model.Issue) (*model.Issue, error) {
	tagMap, err := s.loadTagsForIssues([]int64{iss.ID})
	if err != nil {
		return nil, err
	}
	iss.Tags = tagMap[iss.ID]
	if iss.Tags == nil {
		iss.Tags = []string{}
	}
	return iss, nil
}

type IssueFilter struct {
	RepoID    *int64
	States    []model.State
	FeatureID *int64
	Tags      []string // AND semantics: issue must have ALL of these tags
	AllRepos  bool

	// IncludeDescription, when true, leaves the heavy `description` field
	// populated on each returned issue. Defaults to false so list responses
	// stay lean — full bodies are available via `bacio issue show` / `brief`.
	IncludeDescription bool
}

func (s *Store) ListIssues(f IssueFilter) ([]*model.Issue, error) {
	var (
		where []string
		args  []any
	)
	if !f.AllRepos && f.RepoID != nil {
		where = append(where, "i.repo_id = ?")
		args = append(args, *f.RepoID)
	}
	if f.FeatureID != nil {
		where = append(where, "i.feature_id = ?")
		args = append(args, *f.FeatureID)
	}
	if len(f.States) > 0 {
		ph := make([]string, len(f.States))
		for i, st := range f.States {
			ph[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "i.state IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.Tags) > 0 {
		ph := make([]string, len(f.Tags))
		for i, t := range f.Tags {
			ph[i] = "?"
			args = append(args, t)
		}
		// All requested tags must be present on the issue (AND semantics).
		where = append(where, fmt.Sprintf(
			`i.id IN (SELECT issue_id FROM issue_tags WHERE tag IN (%s) GROUP BY issue_id HAVING COUNT(DISTINCT tag) = %d)`,
			strings.Join(ph, ","), len(f.Tags),
		))
	}
	q := issueSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.prefix, i.number"
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Issue
	var ids []int64
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
		ids = append(ids, iss.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tagMap, err := s.loadTagsForIssues(ids)
	if err != nil {
		return nil, err
	}
	for _, iss := range out {
		iss.Tags = tagMap[iss.ID]
		if iss.Tags == nil {
			iss.Tags = []string{}
		}
		if !f.IncludeDescription {
			iss.Description = ""
		}
	}
	return out, nil
}

func (s *Store) UpdateIssue(id int64, title, description *string, featureID **int64) error {
	sets := []string{}
	args := []any{}
	if title != nil {
		clean, err := ValidateTitle(*title, "title")
		if err != nil {
			return err
		}
		sets = append(sets, "title = ?")
		args = append(args, clean)
	}
	if description != nil {
		clean, err := ValidateBody(*description, "description", false)
		if err != nil {
			return err
		}
		sets = append(sets, "description = ?")
		args = append(args, clean)
	}
	if featureID != nil {
		sets = append(sets, "feature_id = ?")
		args = append(args, nullableInt(*featureID))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE issues SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

func (s *Store) SetIssueState(id int64, state model.State) error {
	_, err := s.DB.Exec(`UPDATE issues SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(state), id)
	return err
}

// SetIssueAssignee writes the assignee field. An empty string clears it.
func (s *Store) SetIssueAssignee(id int64, assignee string) error {
	if assignee != "" {
		clean, err := ValidateName(assignee, "assignee")
		if err != nil {
			return err
		}
		assignee = clean
	}
	_, err := s.DB.Exec(`UPDATE issues SET assignee = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, assignee, id)
	return err
}

// SetWaitingForClaim toggles the issue's waiting_for_claim flag — the
// ephemeral "a dispatch is queued but no agent has claimed yet" signal.
// Set by AddDispatch, cleared by AddAgentClaim / CancelDispatch.
// Deliberately does NOT bump updated_at: this is runtime state, like the
// derived `taken` flag, and bumping updated_at would make git-backed
// sync churn on every dispatch/claim.
func (s *Store) SetWaitingForClaim(issueID int64, waiting bool) error {
	_, err := s.DB.Exec(`UPDATE issues SET waiting_for_claim = ? WHERE id = ?`, waiting, issueID)
	return err
}

// nextCandidateQ picks the lowest-numbered ready issue in a feature: state='todo',
// no assignee, and every `blocks`-blocker in a terminal state. Shared between
// PeekNextIssue (read-only) and ClaimNextIssue (claim).
const nextCandidateQ = `
	SELECT i.id
	FROM issues i
	WHERE i.repo_id = ?
	  AND i.feature_id = ?
	  AND i.state = 'todo'
	  AND i.assignee = ''
	  AND NOT EXISTS (
	    SELECT 1
	    FROM issue_relations ir
	    JOIN issues b ON b.id = ir.from_issue_id
	    WHERE ir.type = 'blocks'
	      AND ir.to_issue_id = i.id
	      AND b.state NOT IN ('done','cancelled')
	  )
	ORDER BY i.number
	LIMIT 1`

// PeekNextIssue returns the issue ClaimNextIssue would pick, without mutating
// state. Returns nil, nil when nothing is currently claimable.
func (s *Store) PeekNextIssue(repoID int64, featureID int64) (*model.Issue, error) {
	var id int64
	err := s.DB.QueryRow(nextCandidateQ, repoID, featureID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

// ClaimNextIssue atomically picks the next ready issue in a feature and flips
// it to in_progress with the given assignee. "Ready" means: state='todo',
// assignee='', and every `blocks`-blocker is in a terminal state
// (done/cancelled). Returns nil, nil when nothing is currently claimable
// (the caller should treat this as "wait and retry"). The picked row is
// the lowest-numbered candidate, matching the order produced by
// `feature plan`.
//
// Concurrency: the SELECT + UPDATE run inside a single transaction, and the
// UPDATE re-asserts the claim predicates so a concurrent claimer that
// commits first causes our UPDATE to affect zero rows (treated as
// "nothing claimable, try again").
func (s *Store) ClaimNextIssue(repoID int64, featureID int64, assignee string) (*model.Issue, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(nextCandidateQ, repoID, featureID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	res, err := tx.Exec(`
		UPDATE issues
		SET state = 'in_progress', assignee = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND state = 'todo' AND assignee = ''`, assignee, id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Lost the race with another claimer; caller can retry.
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

func (s *Store) DeleteIssue(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM issues WHERE id = ?`, id)
	return err
}

// DeleteIssueByUUID removes the issue identified by uuid. Used by
// the sync importer when propagating a remote deletion. Returns
// ErrNotFound when no row matches so the caller can decide whether
// the absence is a problem (it usually isn't — concurrent deletes
// are race-tolerant).
func (s *Store) DeleteIssueByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM issues WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MaxIssueNumber returns the largest `number` currently used by any
// issue in the given repo, or 0 when the repo has no issues. Used by
// the sync importer to allocate the loser's number during collision
// resolution: the local-only issue that's giving up its number gets
// MaxIssueNumber + 1.
func (s *Store) MaxIssueNumber(repoID int64) (int64, error) {
	var n sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(number) FROM issues WHERE repo_id = ?`, repoID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if !n.Valid {
		return 0, nil
	}
	return n.Int64, nil
}

// RenumberIssue assigns a new `number` to the issue identified by
// uuid. Used by the sync importer when the local row collides on
// `(repo_id, number)` with an incoming uuid: the local row (already
// in DB but not yet in git) gives up its number for a fresh one,
// and the just-imported uuid keeps its label.
//
// Returns an error if newNumber would collide with another issue in
// the same repo, or if the issue doesn't exist. Bumps updated_at
// and pulls repos.next_issue_number forward when newNumber overshoots
// the current cache.
func (s *Store) RenumberIssue(uuid string, newNumber int64) error {
	if uuid == "" {
		return fmt.Errorf("RenumberIssue: uuid is required")
	}
	if newNumber <= 0 {
		return fmt.Errorf("RenumberIssue: newNumber must be positive, got %d", newNumber)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		id     int64
		repoID int64
	)
	if err := tx.QueryRow(`SELECT id, repo_id FROM issues WHERE uuid = ?`, uuid).Scan(&id, &repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// UNIQUE(repo_id, number) will enforce collision-rejection on the
	// UPDATE itself, but a clear pre-check produces a better error
	// message for the import-pipeline caller.
	var collide int64
	err = tx.QueryRow(`SELECT id FROM issues WHERE repo_id = ? AND number = ? AND id <> ?`, repoID, newNumber, id).Scan(&collide)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if collide != 0 {
		return fmt.Errorf("RenumberIssue: number %d already used by another issue in this repo", newNumber)
	}
	if _, err := tx.Exec(
		`UPDATE issues SET number = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		newNumber, id,
	); err != nil {
		return err
	}
	// Bump next_issue_number if the new value overshoots the cached
	// counter. This avoids handing out a duplicate number on the next
	// `bacio issue create`.
	if _, err := tx.Exec(
		`UPDATE repos SET next_issue_number = MAX(next_issue_number, ? + 1), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		newNumber, repoID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// IssuePatch carries the import-side fields that flow from issue.yaml
// into a DB row identified by uuid. Pointers distinguish "leave alone"
// (nil) from "clear to empty" (non-nil empty string).
//
// FeatureID uses **int64: nil pointer means "don't touch", a non-nil
// pointer to nil means "clear", and a non-nil pointer to a non-nil
// int64 sets the FK.
type IssuePatch struct {
	Title       *string
	Description *string
	State       *model.State
	Assignee    *string
	FeatureID   **int64
	Tags        *[]string
}

// UpdateIssueByUUID applies an IssuePatch to the issue identified by
// uuid. Mirrors UpdateIssue's shape (which keys by id) for the import
// path, where the importer resolves the row by its on-disk uuid and
// then patches the mutable fields. Tags are replaced wholesale when
// Tags is non-nil — the design rule for sync is "files are
// authoritative on field-level overwrite".
func (s *Store) UpdateIssueByUUID(uuid string, p IssuePatch) error {
	if uuid == "" {
		return fmt.Errorf("UpdateIssueByUUID: uuid is required")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	if err := tx.QueryRow(`SELECT id FROM issues WHERE uuid = ?`, uuid).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	sets := []string{}
	args := []any{}
	if p.Title != nil {
		clean, err := ValidateTitle(*p.Title, "title")
		if err != nil {
			return err
		}
		sets = append(sets, "title = ?")
		args = append(args, clean)
	}
	if p.Description != nil {
		clean, err := ValidateBody(*p.Description, "description", false)
		if err != nil {
			return err
		}
		sets = append(sets, "description = ?")
		args = append(args, clean)
	}
	if p.State != nil {
		sets = append(sets, "state = ?")
		args = append(args, string(*p.State))
	}
	if p.Assignee != nil {
		clean := *p.Assignee
		if clean != "" {
			c, err := ValidateName(clean, "assignee")
			if err != nil {
				return err
			}
			clean = c
		}
		sets = append(sets, "assignee = ?")
		args = append(args, clean)
	}
	if p.FeatureID != nil {
		sets = append(sets, "feature_id = ?")
		args = append(args, nullableInt(*p.FeatureID))
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
		args = append(args, id)
		if _, err := tx.Exec(
			fmt.Sprintf(`UPDATE issues SET %s WHERE id = ?`, strings.Join(sets, ", ")),
			args...,
		); err != nil {
			return err
		}
	}

	if p.Tags != nil {
		// Replace tags wholesale: clear existing, re-insert.
		if _, err := tx.Exec(`DELETE FROM issue_tags WHERE issue_id = ?`, id); err != nil {
			return err
		}
		if err := s.addTagsTx(tx, id, *p.Tags); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// CreateIssueFromSync inserts an issue with caller-supplied uuid and
// number, used by the import pipeline when a uuid arrives from disk
// that bacio has never seen. It bypasses next_issue_number allocation
// (the file already specifies the number) and validates fields the
// same way the regular CreateIssue would.
//
// Bumps repos.next_issue_number forward when `number` overshoots the
// cache so the next `bacio issue create` doesn't reuse it.
func (s *Store) CreateIssueFromSync(repoID int64, uuid string, number int64, featureID *int64, title, description string, state model.State, assignee string, tags []string, createdAt, updatedAt sql.NullTime) (*model.Issue, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateIssueFromSync: uuid is required")
	}
	title, err := ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	if assignee != "" {
		assignee, err = ValidateName(assignee, "assignee")
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	q := `INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, assignee`
	vals := `?, ?, ?, ?, ?, ?, ?, ?`
	args := []any{uuid, repoID, number, nullableInt(featureID), title, description, string(state), assignee}
	if createdAt.Valid {
		q += `, created_at`
		vals += `, ?`
		args = append(args, createdAt.Time)
	}
	if updatedAt.Valid {
		q += `, updated_at`
		vals += `, ?`
		args = append(args, updatedAt.Time)
	}
	q += `) VALUES (` + vals + `)`

	res, err := tx.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := s.addTagsTx(tx, id, tags); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE repos SET next_issue_number = MAX(next_issue_number, ? + 1), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		number, repoID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetIssueByID(id)
}

// IssueStateCounts returns a map of state → count for issues in a repo, or
// across every repo if repoID is nil.
func (s *Store) IssueStateCounts(repoID *int64) (map[string]int, error) {
	q := `SELECT state, COUNT(*) FROM issues`
	args := []any{}
	if repoID != nil {
		q += ` WHERE repo_id = ?`
		args = append(args, *repoID)
	}
	q += ` GROUP BY state`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// CountFeatures returns the feature count for a repo.
func (s *Store) CountFeatures(repoID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM features WHERE repo_id = ?`, repoID).Scan(&n)
	return n, err
}

const issueSelect = `
SELECT i.id, i.uuid, i.repo_id, i.number, r.prefix, i.feature_id, COALESCE(f.slug, ''),
       i.title, i.description, i.state, i.assignee, i.waiting_for_claim, i.created_at, i.updated_at
FROM issues i
JOIN repos r ON r.id = i.repo_id
LEFT JOIN features f ON f.id = i.feature_id`

func scanIssue(row rowScanner) (*model.Issue, error) {
	var (
		i         model.Issue
		prefix    string
		featureID sql.NullInt64
		featSlug  string
		state     string
	)
	err := row.Scan(&i.ID, &i.UUID, &i.RepoID, &i.Number, &prefix, &featureID, &featSlug,
		&i.Title, &i.Description, &state, &i.Assignee, &i.WaitingForClaim, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan issue: %w", err)
	}
	i.Key = fmt.Sprintf("%s-%d", prefix, i.Number)
	i.State = model.State(state)
	if featureID.Valid {
		v := featureID.Int64
		i.FeatureID = &v
		i.FeatureSlug = featSlug
	}
	return &i, nil
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
