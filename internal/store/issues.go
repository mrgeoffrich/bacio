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

// ResolveCreateIssueFeatureID is the shared "what feature does this
// new issue belong to" resolver used by both `bacio issue add` and
// POST /repos/{prefix}/issues at the store boundary (BACI-235). When
// suppliedSlug is non-empty, it's resolved via GetFeatureBySlug — the
// same path the call sites used to do inline. When suppliedSlug is
// empty, the repo's per-repo `default_feature` setting is consulted:
// if set, the resolver returns the default feature's id (auto-apply
// semantic); if unset (NULL), the resolver returns nil (the legacy
// featureless behaviour).
//
// The returned *model.Feature is non-nil iff a feature was resolved
// (either explicit slug or default); the dry-run projection on both
// call sites uses it to populate FeatureSlug so the rehearsal output
// matches what the real call would produce. The returned *int64 is
// the same pointer shape the existing CreateIssue takes — nil means
// featureless.
//
// On an explicit-slug miss this wraps with the canonical
// `feature %q: %w` shape every other call site uses. A dangling
// default_feature_id is impossible at read time — the FK is
// ON DELETE SET NULL — but a defensive lookup error on the default
// path is surfaced verbatim (without slug context, since we resolved
// by id, not slug).
func (s *Store) ResolveCreateIssueFeatureID(repoID int64, suppliedSlug string) (*int64, *model.Feature, error) {
	if suppliedSlug != "" {
		feat, err := s.GetFeatureBySlug(repoID, suppliedSlug)
		if err != nil {
			return nil, nil, fmt.Errorf("feature %q: %w", suppliedSlug, err)
		}
		return &feat.ID, feat, nil
	}
	settings, err := s.GetRepoSettings(repoID)
	if err != nil {
		return nil, nil, err
	}
	if settings.DefaultFeatureID == nil {
		return nil, nil, nil
	}
	feat, err := s.GetFeatureByID(*settings.DefaultFeatureID)
	if err != nil {
		return nil, nil, err
	}
	return &feat.ID, feat, nil
}

// CreateIssue is fully atomic: the counter peek, INSERT, tag writes, and
// counter bump all live in a single transaction. We deliberately bump the
// counter LAST (right before Commit) so that any failure in the issue or
// tag inserts means the counter never even gets touched on disk — that
// way a phantom-number gap requires a Commit to actually succeed, which
// only happens when every preceding step succeeded.
//
// baseBranch (BACI-232) is the per-issue override for the PR base
// branch — "" → NULL → inherit from the feature (and ultimately main).
func (s *Store) CreateIssue(repoID int64, featureID *int64, title, description string, state model.State, tags []string, baseBranch string) (*model.Issue, error) {
	// The issues.state CHECK was dropped (migrateIssuesStateCheck) so the
	// growing Pipeline state set doesn't need a migration each time; the
	// enum is now enforced here at the store boundary instead.
	if _, err := model.ParseState(string(state)); err != nil {
		return nil, err
	}
	title, err := ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	baseBranch, err = ValidateBranchName(baseBranch)
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

	// BACI-138: terminal_at is seeded on insert when the issue lands
	// directly in a terminal state, so a `bacio issue add --state done`
	// shows up on the Done column with a populated sort key from the
	// first read. terminalAtClause yields CURRENT_TIMESTAMP for
	// done/cancelled and NULL otherwise.
	res, err := tx.Exec(
		`INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, base_branch, terminal_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, `+terminalAtClause(state)+`)`,
		identity.New(), repoID, num, nullableInt(featureID), title, description, string(state),
		sql.NullString{String: baseBranch, Valid: baseBranch != ""},
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

// TopShippingIssue returns the next-to-ship card in the repo — the
// lowest-priority (position 1 = top of the FIFO) non-archived
// to_be_shipped issue — or nil when the Shipping column is empty. Used
// by the auto-ship ticker, which acts on one card at a time.
func (s *Store) TopShippingIssue(repoID int64) (*model.Issue, error) {
	iss, err := scanIssue(s.DB.QueryRow(issueSelect+
		` WHERE i.repo_id = ? AND i.state = ? AND i.archived_at IS NULL ORDER BY i.priority ASC, i.number ASC LIMIT 1`,
		repoID, string(model.StateToBeShipped)))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
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
	// IncludeArchived (BACI-68), when true, includes rows with a
	// non-NULL archived_at. Defaults to false — archived issues are
	// hidden from default lists (board, kanban, CLI / API JSON) and
	// only surface via a per-call `--include-archived` or via the
	// `display.show_archived` setting being on. Single-item lookups
	// (GetIssueByID / GetIssueByKey) always return the row regardless.
	IncludeArchived bool
	// HiddenFeatureSlugs (BACI-177) excludes issues whose feature row
	// has a slug in this list — the per-feature "Show on board" toggle
	// flipped off on the Features screen. Empty slice = no filter;
	// unknown slugs are silently ignored (the subselect simply doesn't
	// match any feature row). Issues with feature_id IS NULL pass
	// through regardless; the toggle is per-feature, not per-issue,
	// so an unattached issue can't be hidden via this path.
	HiddenFeatureSlugs []string
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
	if !f.IncludeArchived {
		// BACI-68: archived rows are hidden by default. The caller can
		// flip IncludeArchived on (CLI --include-archived, API
		// ?include_archived=1, or display.show_archived=true at the
		// surface) to inflate the list.
		where = append(where, "i.archived_at IS NULL")
	}
	if len(f.HiddenFeatureSlugs) > 0 {
		// BACI-177: drop any issue whose feature_id resolves to a slug
		// the user has hidden via the per-feature "Show on board"
		// toggle. The set is per-repo in tui_settings (boardHiddenFeaturesKey)
		// — the board / web / TUI surfaces all hit this same KV row.
		// Issues with feature_id IS NULL pass through: the toggle is
		// per-feature, so unattached issues aren't addressable.
		ph := make([]string, len(f.HiddenFeatureSlugs))
		for i, slug := range f.HiddenFeatureSlugs {
			ph[i] = "?"
			args = append(args, slug)
		}
		where = append(where, fmt.Sprintf(
			`(i.feature_id IS NULL OR i.feature_id NOT IN (
				SELECT id FROM features WHERE repo_id = i.repo_id AND slug IN (%s)
			))`,
			strings.Join(ph, ","),
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

// UpdateIssue applies the non-nil patch fields to the issue row.
//
// baseBranch (BACI-232) follows the same pointer-vs-presence dance as
// Feature.UpdateFeature's branchName: nil pointer = no change; non-nil
// empty string = clear (write NULL, inherit from feature); non-nil
// non-empty = set + validate.
func (s *Store) UpdateIssue(id int64, title, description *string, featureID **int64, baseBranch *string) error {
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
	if baseBranch != nil {
		clean, err := ValidateBranchName(*baseBranch)
		if err != nil {
			return err
		}
		sets = append(sets, "base_branch = ?")
		// Empty string clears the column to NULL — keeps the legacy
		// "inherit from feature" behaviour reachable from an edit.
		args = append(args, sql.NullString{String: clean, Valid: clean != ""})
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
	// Engine-governed gate (Pipeline): a card in a Pipeline column
	// (in_pipeline / to_be_shipped) has its state owned by the controller
	// engine. A write that would flip it into a legacy processing state
	// (in_progress / needs_action / in_review) is ignored — the engine
	// signals "waiting on the user" via an open question on the job row,
	// not a state change. Deliberate column moves (todo / in_pipeline /
	// to_be_shipped / done / cancelled) still apply. A missing id is a
	// no-op, preserving the pre-guard behaviour (UPDATE affecting 0 rows).
	var current string
	var repoID int64
	switch err := s.DB.QueryRow(`SELECT state, repo_id FROM issues WHERE id = ?`, id).Scan(&current, &repoID); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	if isEngineGovernedState(model.State(current)) && isProcessingState(state) {
		return nil
	}
	// Pipeline teardown: a card actually leaving in_pipeline for a
	// non-pipeline column (a user drag back to Backlog, or a move to
	// done/cancelled) abandons its process run. The engine governs only
	// in_pipeline cards, so once this card leaves it can never reconcile a
	// still-running job — cancel the in-flight job + its dispatch here so a
	// worker isn't orphaned and a stale `running` row can't confuse a later
	// re-entry. Reaching this point means the move is real: a processing-
	// state target on an in_pipeline card already returned at the guard
	// above, so `state` here is todo/done/cancelled. The engine's own
	// hand-off (in_pipeline → to_be_shipped) is excluded by the
	// to_be_shipped check, and auto-ship (to_be_shipped → done) by the
	// in_pipeline source check.
	if model.State(current) == model.StateInPipeline && state != model.StateInPipeline && state != model.StateToBeShipped {
		if err := s.cancelRunningPipelineJob(id); err != nil {
			return err
		}
	}
	// BACI-138: terminal_at follows the state column — stamped on a
	// transition INTO done/cancelled, cleared on a transition OUT.
	// The terminalAtClause helper builds a CASE expression that
	// inspects the NEW state (not the current row), which is the only
	// state info we have without a SELECT-then-UPDATE round trip.
	//
	// BACI-275: a card *entering* to_be_shipped (current state differs)
	// is appended to the back of the Shipping FIFO — it gets
	// MAX(priority)+1 across the repo's non-archived to_be_shipped band
	// so it sorts strictly behind every card already queued, rather than
	// keeping its default priority 0 and tie-breaking by number ahead of
	// older arrivals. Re-asserting to_be_shipped (the engine's idempotent
	// no-op hand-off, or a same-state write) leaves priority untouched so
	// an already-queued card never re-shuffles. The band query matches
	// the WHERE shape TopShippingIssue / ReorderIssue use, and excludes
	// the card itself (not yet written to to_be_shipped at this point).
	if model.State(current) != model.StateToBeShipped && state == model.StateToBeShipped {
		var priority int64
		if err := s.DB.QueryRow(
			`SELECT COALESCE(MAX(priority), -1) + 1 FROM issues WHERE repo_id = ? AND state = ? AND archived_at IS NULL`,
			repoID, string(model.StateToBeShipped),
		).Scan(&priority); err != nil {
			return err
		}
		_, err := s.DB.Exec(
			`UPDATE issues SET state = ?, priority = ?, updated_at = CURRENT_TIMESTAMP, terminal_at = `+terminalAtClause(state)+` WHERE id = ?`,
			string(state), priority, id,
		)
		return err
	}
	_, err := s.DB.Exec(
		`UPDATE issues SET state = ?, updated_at = CURRENT_TIMESTAMP, terminal_at = `+terminalAtClause(state)+` WHERE id = ?`,
		string(state), id,
	)
	return err
}

// terminalAtClause returns the SQL expression to assign to terminal_at
// when the row's state column is being set to `target`. Terminal
// targets evaluate to CURRENT_TIMESTAMP (stamping the fresh entry into
// the terminal state); non-terminal targets evaluate to NULL (clearing
// the column so a reopened issue no longer sorts as completed).
//
// Returned as a literal SQL fragment rather than a value bound through
// `?` because SQLite parameter binding can't carry CURRENT_TIMESTAMP
// the way a literal can, and the two-branch decision is a closed set
// driven by the caller's typed model.State input — there's no
// SQL-injection surface.
func terminalAtClause(target model.State) string {
	if target == model.StateDone || target == model.StateCancelled {
		return "CURRENT_TIMESTAMP"
	}
	return "NULL"
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

// SetIssueArchived stamps or clears the issue's archived_at column
// (BACI-68). When archived is true the column is set to
// CURRENT_TIMESTAMP if it's currently NULL — re-archiving a row that's
// already archived is a no-op so the audit timestamp doesn't drift on
// idempotent calls. When archived is false the column is unconditionally
// cleared.
//
// updated_at is bumped by the bump_issue_updated_on_archive_change
// schema trigger (BACI-189), NOT here — the trigger fires only when
// the caller did not also write updated_at, which is the case for
// every archive verb but not for the sync importer's authoritative
// `(archived_at, updated_at)` pair UPDATE. The trigger is what keeps
// the next sync round-trip from clobbering the local archive stamp
// via LWW. The sweep's eligibility predicate keys on terminal_at, not
// updated_at (see archive.go), so bumping updated_at on archive does
// not interfere with the retention window.
func (s *Store) SetIssueArchived(issueID int64, archived bool) error {
	if archived {
		_, err := s.DB.Exec(`UPDATE issues SET archived_at = CURRENT_TIMESTAMP WHERE id = ? AND archived_at IS NULL`, issueID)
		return err
	}
	_, err := s.DB.Exec(`UPDATE issues SET archived_at = NULL WHERE id = ?`, issueID)
	return err
}

// ReorderIssue moves the issue to the given 1-based position within its
// (repo, state) ordering band and renumbers the band's priorities to a
// dense 0..n-1 sequence — position 1 → priority 0 → top of the column /
// next to go. Backs the Pipeline Backlog (todo) and Shipping
// (to_be_shipped) drag-to-reorder, whose order is persisted rather than
// living in board-local display state. position is clamped to [1, n].
//
// Archived issues are excluded from the band (they are hidden from the
// queue) and keep whatever priority they had. Renumbering does NOT bump
// updated_at: priority is local queue-ordering metadata, not user-edited
// content, so a reorder must not churn the sync last-writer-wins gate.
func (s *Store) ReorderIssue(issueID int64, position int) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var repoID int64
	var state string
	err = tx.QueryRow(`SELECT repo_id, state FROM issues WHERE id = ?`, issueID).Scan(&repoID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	// Current order of the band: by stored priority, then number as a
	// stable tiebreak (every row starts at priority 0, so number is the
	// initial order until the first reorder writes dense values).
	rows, err := tx.Query(
		`SELECT id FROM issues WHERE repo_id = ? AND state = ? AND archived_at IS NULL ORDER BY priority ASC, number ASC`,
		repoID, state,
	)
	if err != nil {
		return err
	}
	var others []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if id != issueID {
			others = append(others, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	idx := position - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(others) {
		idx = len(others)
	}
	ordered := make([]int64, 0, len(others)+1)
	ordered = append(ordered, others[:idx]...)
	ordered = append(ordered, issueID)
	ordered = append(ordered, others[idx:]...)

	for i, id := range ordered {
		if _, err := tx.Exec(`UPDATE issues SET priority = ? WHERE id = ?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// nextCandidateQ picks the lowest-numbered ready issue in a feature: state='todo',
// no assignee, archived_at IS NULL (BACI-68 — an archived row is hidden from
// the auto-pick path so a manually-archived todo can't be silently revived),
// and every `blocks`-blocker in a terminal state. Shared between PeekNextIssue
// (read-only) and ClaimNextIssue (claim).
const nextCandidateQ = `
	SELECT i.id
	FROM issues i
	WHERE i.repo_id = ?
	  AND i.feature_id = ?
	  AND i.state = 'todo'
	  AND i.assignee = ''
	  AND i.archived_at IS NULL
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

// ClaimNextIssue atomically picks the next ready issue in a feature and
// stamps it with the given assignee (leaving it in todo — claiming is a
// focus marker since BACI-300, not a state move). "Ready" means:
// state='todo', assignee='', and every `blocks`-blocker is in a terminal
// state (done/cancelled). Returns nil, nil when nothing is currently claimable
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

	// BACI-300: claiming the next ready issue is a focus marker, not a
	// state move — it stamps the assignee and leaves the card in `todo`
	// (the legacy in_progress flip was retired alongside the state). The
	// predicate still only matches an unassigned `todo` row, so this is
	// an atomic "grab the next ready issue" without churning its column.
	res, err := tx.Exec(`
		UPDATE issues
		SET assignee = ?, updated_at = CURRENT_TIMESTAMP
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
		// BACI-138: keep terminal_at in lockstep with state on the
		// sync-import path too. terminalAtClause is a SQL literal
		// (NULL / CURRENT_TIMESTAMP), not a bound value — see the
		// SetIssueState comment for the rationale.
		sets = append(sets, "terminal_at = "+terminalAtClause(*p.State))
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
		// Replace tags wholesale: clear existing, re-insert. The
		// bump_issue_updated_on_tag_insert / _delete schema triggers
		// advance issues.updated_at on every affected row, so an
		// explicit body UPDATE here would be redundant. See BACI-144.
		if _, err := tx.Exec(`DELETE FROM issue_tags WHERE issue_id = ?`, id); err != nil {
			return err
		}
		if err := s.addTagsTx(tx, id, *p.Tags); err != nil {
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

	// BACI-138: seed terminal_at when the imported row is already in
	// a terminal state. terminalAtClause yields CURRENT_TIMESTAMP for
	// done/cancelled and NULL otherwise — embedded as a SQL literal
	// (not a bound `?` value) because CURRENT_TIMESTAMP only resolves
	// from a literal at parse time.
	q := `INSERT INTO issues (uuid, repo_id, number, feature_id, title, description, state, assignee, terminal_at`
	vals := `?, ?, ?, ?, ?, ?, ?, ?, ` + terminalAtClause(state)
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

// issueSelect joins each issue to its repo + (optional) feature, and
// derives the `taken` flag via an EXISTS subquery against open agent
// claims held by alive sessions — the same predicate
// OpenClaimsBySession uses, just folded into the row read so list /
// show / brief responses all carry the signal inline without a second
// round trip.
const issueSelect = `
SELECT i.id, i.uuid, i.repo_id, i.number, r.prefix, i.feature_id, COALESCE(f.slug, ''), COALESCE(f.emoji, ''), COALESCE(f.branch_name, ''),
       i.title, i.description, i.state, i.assignee,
       EXISTS (
         SELECT 1 FROM agent_claims c
         JOIN agent_sessions s ON s.id = c.session_pk
         WHERE c.issue_id = i.id
           AND c.released_at IS NULL
           AND s.ended_at IS NULL
       ) AS taken,
       i.archived_at, i.terminal_at, i.base_branch,
       i.priority, i.engine_mode, i.engine_pause_reason, i.created_at, i.updated_at
FROM issues i
JOIN repos r ON r.id = i.repo_id
LEFT JOIN features f ON f.id = i.feature_id`

func scanIssue(row rowScanner) (*model.Issue, error) {
	var (
		i              model.Issue
		prefix         string
		featureID      sql.NullInt64
		featSlug       string
		featEmoji      string
		featBranchName string
		state          string
		archivedAt     sql.NullTime
		terminalAt     sql.NullTime
		baseBranch     sql.NullString
		engineMode     string
	)
	err := row.Scan(&i.ID, &i.UUID, &i.RepoID, &i.Number, &prefix, &featureID, &featSlug, &featEmoji, &featBranchName,
		&i.Title, &i.Description, &state, &i.Assignee, &i.Taken,
		&archivedAt, &terminalAt, &baseBranch,
		&i.Priority, &engineMode, &i.EnginePauseReason, &i.CreatedAt, &i.UpdatedAt)
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
		// FeatureEmoji is only populated when there's a feature; the
		// COALESCE protects against NULL when there's no join match
		// (i.e. issue with no feature_id).
		i.FeatureEmoji = featEmoji
		// FeatureBranchName mirrors FeatureEmoji's join: COALESCE'd to
		// '' for no-feature rows AND for feature rows whose
		// branch_name is NULL (the default "ship to main" case). The
		// BoardCard denorm reads this directly so the kanban chip and
		// the ActivityTray grouping don't need a second lookup.
		i.FeatureBranchName = featBranchName
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		i.ArchivedAt = &t
	}
	if terminalAt.Valid {
		t := terminalAt.Time
		i.TerminalAt = &t
	}
	if baseBranch.Valid {
		b := baseBranch.String
		i.BaseBranch = &b
	}
	i.EngineMode = model.EngineMode(engineMode)
	return &i, nil
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
