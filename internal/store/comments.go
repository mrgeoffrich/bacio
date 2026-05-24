package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// CreateCommentIn is the validated tuple CreateComment consumes. The
// Eval / AgentSessionID / DispatchID / Mode quartet (BACI-131) is the
// in-flight context the kanban quick-eval composer pins onto the row
// server-side via ResolveEvalContext; on normal comments the four
// fields stay at zero values. TranscriptEventRef (BACI-141) is the
// optional per-event anchor populated by the transcript viewer's
// per-event composer — empty for board-level eval notes.
type CreateCommentIn struct {
	IssueID            int64
	Author             string
	Body               string
	Eval               bool
	AgentSessionID     string
	DispatchID         *int64
	Mode               string
	TranscriptEventRef string
}

func (s *Store) CreateComment(in CreateCommentIn) (*model.Comment, error) {
	author, err := ValidateName(in.Author, "author")
	if err != nil {
		return nil, err
	}
	body, err := ValidateBody(in.Body, "body", true)
	if err != nil {
		return nil, err
	}
	sess, mode, err := validateEvalTriple(in.AgentSessionID, in.Mode)
	if err != nil {
		return nil, err
	}
	eventRef, err := validateTranscriptEventRef(in.TranscriptEventRef)
	if err != nil {
		return nil, err
	}
	evalInt := 0
	if in.Eval {
		evalInt = 1
	}
	res, err := s.DB.Exec(
		`INSERT INTO comments (uuid, issue_id, author, body, eval, agent_session_id, dispatch_id, mode, transcript_event_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		identity.New(), in.IssueID, author, body, evalInt, sess, nullableInt(in.DispatchID), mode, nullableString(eventRef),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCommentByID(id)
}

// commentCols / commentSelect drive every single-row read of a comment.
// agent_name is filled by the brief / issue-detail JOIN at a separate
// query — the read helpers here keep to the persisted columns.
const commentCols = `id, uuid, issue_id, author, body, eval, agent_session_id, dispatch_id, mode, transcript_event_ref, created_at`

// commentColsQualified mirrors commentCols but prefixes every column
// with the `c.` alias — needed by the ListComments JOIN where bare
// `id` would clash with agent_sessions.id and agents.id.
const commentColsQualified = `c.id, c.uuid, c.issue_id, c.author, c.body, c.eval, c.agent_session_id, c.dispatch_id, c.mode, c.transcript_event_ref, c.created_at`

func (s *Store) GetCommentByID(id int64) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id = ?`, id)
	return scanComment(row)
}

// GetCommentByUUID is the sync-side lookup. Comments live in their own
// timestamped files under each issue folder, so an importer needs to
// match incoming comment uuids against the DB.
func (s *Store) GetCommentByUUID(uuid string) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE uuid = ?`, uuid)
	return scanComment(row)
}

// CreateCommentFromSyncIn carries the sync-import payload. Mirrors
// CreateCommentIn but adds the caller-supplied uuid and createdAt that
// the importer recovered from the on-disk file. DispatchID is
// deliberately absent: dispatch_id is a local-DB integer FK that can't
// round-trip cross-machine, so the sync import path leaves it nil and
// rebuilds-by-implication from (AgentSessionID, Mode) if a reviewer
// needs it.
type CreateCommentFromSyncIn struct {
	IssueID            int64
	UUID               string
	Author             string
	Body               string
	CreatedAt          sql.NullTime
	Eval               bool
	AgentSessionID     string
	Mode               string
	TranscriptEventRef string
}

// CreateCommentFromSync inserts a comment with a caller-supplied
// uuid, for the sync import path. Mirrors CreateComment but bypasses
// auto-uuid generation and accepts createdAt to preserve the
// timestamp embedded in the on-disk filename.
func (s *Store) CreateCommentFromSync(in CreateCommentFromSyncIn) (*model.Comment, error) {
	if in.UUID == "" {
		return nil, fmt.Errorf("CreateCommentFromSync: uuid is required")
	}
	author, err := ValidateName(in.Author, "author")
	if err != nil {
		return nil, err
	}
	body, err := ValidateBody(in.Body, "body", true)
	if err != nil {
		return nil, err
	}
	sess, mode, err := validateEvalTriple(in.AgentSessionID, in.Mode)
	if err != nil {
		return nil, err
	}
	eventRef, err := validateTranscriptEventRef(in.TranscriptEventRef)
	if err != nil {
		return nil, err
	}
	evalInt := 0
	if in.Eval {
		evalInt = 1
	}
	cols := []string{"uuid", "issue_id", "author", "body", "eval", "agent_session_id", "mode"}
	placeholders := []string{"?", "?", "?", "?", "?", "?", "?"}
	args := []any{in.UUID, in.IssueID, author, body, evalInt, sess, mode}
	if eventRef != "" {
		cols = append(cols, "transcript_event_ref")
		placeholders = append(placeholders, "?")
		args = append(args, eventRef)
	}
	if in.CreatedAt.Valid {
		cols = append(cols, "created_at")
		placeholders = append(placeholders, "?")
		args = append(args, in.CreatedAt.Time)
	}
	q := `INSERT INTO comments (` + strings.Join(cols, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCommentByID(id)
}

// DeleteCommentByUUID is both the sync-side delete (the importer drops
// a row when the on-disk file disappears) and the user-facing delete
// primitive behind `bacio comment rm` and the API/UI delete affordance.
// Comments are leaf records — no children, relations or links — so a
// hard DELETE is the whole story; audit logging is the caller's
// responsibility, consistent with how CreateComment leaves the
// comment.add history entry to the client layer.
func (s *Store) DeleteCommentByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM comments WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateCommentByUUID rewrites the body / author of a comment by
// uuid. Comments are simpler than other records — there's no
// label-collision phase, no relations — so a single helper covers
// every import-side mutation.
func (s *Store) UpdateCommentByUUID(uuid string, author, body *string) error {
	if uuid == "" {
		return fmt.Errorf("UpdateCommentByUUID: uuid is required")
	}
	sets := []string{}
	args := []any{}
	if author != nil {
		clean, err := ValidateName(*author, "author")
		if err != nil {
			return err
		}
		sets = append(sets, "author = ?")
		args = append(args, clean)
	}
	if body != nil {
		clean, err := ValidateBody(*body, "body", true)
		if err != nil {
			return err
		}
		sets = append(sets, "body = ?")
		args = append(args, clean)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, uuid)
	res, err := s.DB.Exec(
		fmt.Sprintf(`UPDATE comments SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
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

func (s *Store) ListComments(issueID int64) ([]*model.Comment, error) {
	// Resolve the agent identity slug for each comment via a LEFT JOIN
	// from comments.agent_session_id -> agent_sessions.session_id ->
	// agents.name. The eval-footer copy on the issue-detail timeline
	// reads it ("during: planning · vivid-finch"); non-eval rows keep
	// agent_name empty by construction (their agent_session_id is '').
	// The two LEFT JOINs are O(N rows on this issue) — cheap.
	rows, err := s.DB.Query(`
		SELECT `+commentColsQualified+`, COALESCE(a.name, '')
		FROM comments c
		LEFT JOIN agent_sessions s ON s.session_id = c.agent_session_id AND c.agent_session_id != ''
		LEFT JOIN agents a ON a.id = s.agent_id
		WHERE c.issue_id = ? ORDER BY c.created_at, c.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Comment
	for rows.Next() {
		c, err := scanCommentWithAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanComment(row rowScanner) (*model.Comment, error) {
	var c model.Comment
	var sess, mode, eventRef sql.NullString
	var dispatchID sql.NullInt64
	err := row.Scan(
		&c.ID, &c.UUID, &c.IssueID, &c.Author, &c.Body,
		&c.Eval, &sess, &dispatchID, &mode, &eventRef, &c.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	c.AgentSessionID = sess.String
	c.Mode = mode.String
	c.TranscriptEventRef = eventRef.String
	if dispatchID.Valid {
		id := dispatchID.Int64
		c.DispatchID = &id
	}
	return &c, nil
}

// scanCommentWithAgent extends scanComment with the trailing
// agent_name column the ListComments JOIN appends.
func scanCommentWithAgent(row rowScanner) (*model.Comment, error) {
	var c model.Comment
	var sess, mode, eventRef, agentName sql.NullString
	var dispatchID sql.NullInt64
	err := row.Scan(
		&c.ID, &c.UUID, &c.IssueID, &c.Author, &c.Body,
		&c.Eval, &sess, &dispatchID, &mode, &eventRef, &c.CreatedAt,
		&agentName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	c.AgentSessionID = sess.String
	c.Mode = mode.String
	c.TranscriptEventRef = eventRef.String
	if dispatchID.Valid {
		id := dispatchID.Int64
		c.DispatchID = &id
	}
	c.AgentName = agentName.String
	return &c, nil
}

// validateEvalTriple normalises and validates the (agent_session_id,
// mode) pair the eval-comment context fields carry. Both fields are
// optional on the row (a non-eval comment leaves them empty); when
// present, agent_session_id must be a valid session-id shape and mode
// must be a valid dispatch-mode slug. Trims and returns the cleaned
// values to use in the INSERT.
func validateEvalTriple(sessionID, mode string) (string, string, error) {
	cleanSess := strings.TrimSpace(sessionID)
	if cleanSess != "" {
		v, err := ValidateSessionID(cleanSess)
		if err != nil {
			return "", "", err
		}
		cleanSess = v
	}
	cleanMode := strings.TrimSpace(mode)
	if cleanMode != "" {
		m, err := model.ParseDispatchMode(cleanMode)
		if err != nil {
			return "", "", err
		}
		cleanMode = string(m)
	}
	return cleanSess, cleanMode, nil
}

// validateTranscriptEventRef normalises and length-bounds the BACI-141
// per-event anchor — a short opaque string written into
// comments.transcript_event_ref. Empty is the unanchored default.
// Practical formats in use today: `tool_use_id:<id>` and
// `line_index:<n>`; the format is caller-defined so we only reject
// control characters and absurdly long values rather than gate the
// shape (a future format change shouldn't need a schema-side update).
func validateTranscriptEventRef(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > 256 {
		return "", fmt.Errorf("transcript_event_ref too long: %d bytes, max 256", len(s))
	}
	for _, r := range s {
		if isDisallowedControlSingle(r) {
			return "", fmt.Errorf("transcript_event_ref contains a disallowed control character (U+%04X)", r)
		}
	}
	return s, nil
}

// nullableString maps the empty string to a SQL NULL via the
// driver.Valuer convention. Used by the BACI-141 transcript_event_ref
// INSERT so an unanchored eval comment writes NULL (not the empty
// string) into the column.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CountEvalCommentsByIssue (BACI-141) returns a map keyed by issue id
// of the number of eval-flagged comments on each issue. Backs the
// per-card "M eval note(s)" chip surfaced on the kanban board. Rides
// the partial idx_comments_issue_eval index so the cost scales with
// the eval-comment count, not the full comment table.
func (s *Store) CountEvalCommentsByIssue(issueIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(issueIDs))
	args := make([]any, len(issueIDs))
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT issue_id, COUNT(*) FROM comments
	       WHERE eval = 1 AND issue_id IN (` + strings.Join(placeholders, ", ") + `)
	       GROUP BY issue_id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// EvalContext is the in-flight snapshot ResolveEvalContext returns —
// the (session, dispatch, mode) triple captured server-side at write
// time so the kanban card's quick-eval composer can post a comment
// pinned to the exact agent run that was live when the user clicked
// the button. All three fields are zero values when the issue has no
// open claim (i.e. an eval was posted on an untaken card — the UI
// blocks that, the store stays defensive).
type EvalContext struct {
	AgentSessionID string
	DispatchID     *int64
	Mode           string
}

// ResolveEvalContext snapshots the in-flight dispatch context the
// kanban card's "taken" state derives from — what we want to pin onto
// an eval comment at write time. Mirrors what
// internal/boardcards/cards.go's pickActiveMode does over already-bulked
// board data, lifted into the store layer so the HTTP and local-client
// paths share one implementation.
//
// Returns a zero EvalContext when issueID has no open claim. When a
// claim is held but no non-cancelled dispatch matches that session and
// issue, AgentSessionID is set and DispatchID stays nil with Mode "".
func (s *Store) ResolveEvalContext(issueID int64) (EvalContext, error) {
	claims, err := s.ListClaimsForIssue(issueID)
	if err != nil {
		return EvalContext{}, err
	}
	// Newest *open* claim wins (the claim list is already newest-first
	// by claimed_at DESC). Pairing/review can hold two open claims at
	// once; the newer-first ordering matches the kanban card's "active
	// session" derivation.
	var sess *model.AgentClaim
	for _, c := range claims {
		if c.ReleasedAt == nil {
			sess = c
			break
		}
	}
	if sess == nil {
		return EvalContext{}, nil
	}
	ctx := EvalContext{AgentSessionID: sess.SessionID}
	// Look up the issue's repo so the dispatch filter scopes correctly
	// (a session id is per-host; the repo guard is belt-and-braces).
	iss, err := s.GetIssueByID(issueID)
	if err != nil {
		return EvalContext{}, err
	}
	disps, err := s.ListDispatches(DispatchFilter{
		RepoID:          &iss.RepoID,
		TargetSessionID: sess.SessionID,
	})
	if err != nil {
		return EvalContext{}, err
	}
	for _, d := range disps {
		if d.Status == model.DispatchCancelled {
			continue
		}
		if d.IssueKey != iss.Key {
			continue
		}
		id := d.ID
		ctx.DispatchID = &id
		ctx.Mode = string(d.Mode)
		break
	}
	return ctx, nil
}
