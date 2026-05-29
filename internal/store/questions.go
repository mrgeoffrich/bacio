package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddSessionQuestionIn is the validated tuple AddSessionQuestion
// consumes. SessionID is the external session id (the bacio channel
// minted the row but didn't have to know the agent's session PK
// — the store resolves it). IssueKey is optional; an empty string
// means "no open claim at ask time".
//
// AskedBy is the persistent agent identity slug recorded against
// the row's `asked_by` column.
//
// RequestUUID is normally minted by AddSessionQuestion itself
// (UUIDv7, returned to the caller); pass a non-empty value to
// override — only the unit tests need that.
type AddSessionQuestionIn struct {
	SessionID   string
	IssueKey    string
	Payload     model.QuestionPayload
	AskedBy     string
	RequestUUID string
}

// AddSessionQuestion records a new open question. The session is
// resolved by external session_id; an unknown session_id returns
// ErrNotFound. The returned row carries the freshly-minted
// RequestUUID — the channel uses it to correlate the parked
// JSON-RPC reply when DrainAnsweredQuestions later returns the row.
func (s *Store) AddSessionQuestion(in AddSessionQuestionIn) (*model.SessionQuestion, error) {
	if _, err := ValidateSessionID(in.SessionID); err != nil {
		return nil, err
	}
	askedBy, err := ValidateAgentName(in.AskedBy)
	if err != nil {
		return nil, fmt.Errorf("asked_by: %w", err)
	}
	if err := model.ValidateQuestionPayload(in.Payload); err != nil {
		return nil, err
	}
	// The client layer guarantees a non-empty canonical key — see
	// localClient.AddSessionQuestion (BACI-128). Historical empty
	// rows on disk stay queryable; the new write path can't insert
	// one. Defensive: still single-line / no-control-char-validate
	// here so an unknown future caller skipping the client layer
	// trips this guard too.
	if in.IssueKey != "" {
		// Single-line, no control chars — the issue_key column
		// carries a denormalised snapshot so per-issue lookups in
		// the UI don't need a second join.
		if _, err := validateSingleLine(in.IssueKey, "issue_key", maxNameLen, true); err != nil {
			return nil, err
		}
	}

	payloadJSON, err := model.MarshalQuestionPayload(in.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	requestUUID := strings.TrimSpace(in.RequestUUID)
	if requestUUID == "" {
		requestUUID = identity.New()
	}
	// Pipeline: parent the question onto the asking card's running job
	// when the issue is in_pipeline. An open question on the current job
	// is the engine's halt signal (§6.1). Resolved read-only before the
	// tx; nil for non-pipeline / legacy questions. A resolution miss is
	// non-fatal — the question is still recorded, just session-parented.
	pipelineJobID := s.currentPipelineJobForIssueKey(in.IssueKey)

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var sessPK int64
	if err := tx.QueryRow(
		`SELECT id FROM agent_sessions WHERE session_id = ?`, in.SessionID,
	).Scan(&sessPK); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	res, err := tx.Exec(`
		INSERT INTO agent_session_questions
		    (session_pk, request_uuid, issue_key, payload_json, state, asked_by, pipeline_job_id)
		VALUES (?, ?, ?, ?, 'open', ?, ?)`,
		sessPK, requestUUID, in.IssueKey, payloadJSON, askedBy, nullableInt(pipelineJobID),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSessionQuestion(id)
}

// currentPipelineJobForIssueKey returns the id of the running pipeline
// job for the given issue key, or nil — the question-parenting target.
// The asking worker is the session running that job, so "the issue's
// running job" is the worker's current job without needing the
// session→dispatch chain. Read-only and best-effort: any miss (bad key,
// non-pipeline issue, no running job) returns nil so the question still
// records as session-parented.
func (s *Store) currentPipelineJobForIssueKey(issueKey string) *int64 {
	if issueKey == "" {
		return nil
	}
	prefix, num, err := ParseIssueKey(issueKey)
	if err != nil {
		return nil
	}
	iss, err := s.GetIssueByKey(prefix, num)
	if err != nil || iss.State != model.StateInPipeline {
		return nil
	}
	jobs, err := s.ListPipelineJobs(iss.ID)
	if err != nil {
		return nil
	}
	for _, j := range jobs {
		if j.Status == model.JobRunning {
			id := j.ID
			return &id
		}
	}
	return nil
}

// questionSelect is the canonical column list — the join lifts
// the external session_id alongside the row so the channel and
// API surfaces don't need a second lookup.
const questionSelect = `
	SELECT q.id, q.session_pk, s.session_id, q.request_uuid, q.issue_key,
	       q.payload_json, q.answers_json, q.state,
	       q.asked_at, q.answered_at, q.asked_by, q.answered_by,
	       q.pipeline_job_id
	FROM agent_session_questions q
	JOIN agent_sessions s ON s.id = q.session_pk`

// GetSessionQuestion fetches one question by primary key, or ErrNotFound.
func (s *Store) GetSessionQuestion(id int64) (*model.SessionQuestion, error) {
	row := s.DB.QueryRow(questionSelect+` WHERE q.id = ?`, id)
	q, err := scanSessionQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return q, err
}

// GetSessionQuestionByUUID looks up a question by its request_uuid
// — the channel uses this when its in-memory parked-reply map says
// "the row this id corresponds to is ready".
func (s *Store) GetSessionQuestionByUUID(requestUUID string) (*model.SessionQuestion, error) {
	requestUUID = strings.TrimSpace(requestUUID)
	if requestUUID == "" {
		return nil, fmt.Errorf("request_uuid is required")
	}
	row := s.DB.QueryRow(questionSelect+` WHERE q.request_uuid = ?`, requestUUID)
	q, err := scanSessionQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return q, err
}

// ListSessionQuestions returns the questions for one session,
// newest-first. If states is non-empty it filters to those state
// values; an empty/nil states means "every state".
func (s *Store) ListSessionQuestions(sessionID string, states []model.QuestionState) ([]*model.SessionQuestion, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	q := questionSelect + ` WHERE s.session_id = ?`
	args := []any{sessionID}
	if len(states) > 0 {
		q += ` AND q.state IN (` + placeholders(len(states)) + `)`
		for _, st := range states {
			args = append(args, string(st))
		}
	}
	q += ` ORDER BY q.asked_at DESC, q.id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SessionQuestion
	for rows.Next() {
		v, err := scanSessionQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListOpenQuestionsBySessions returns a session_pk -> []*SessionQuestion
// map for the given session ids in one query. Mirrors
// OpenClaimsBySession / ListTodosBySessions so the agent views can
// hydrate every row in one trip instead of N+1. Sessions with no open
// questions are absent from the map; an empty input is a no-op.
func (s *Store) ListOpenQuestionsBySessions(sessionIDs []string) (map[int64][]*model.SessionQuestion, error) {
	out := make(map[int64][]*model.SessionQuestion)
	if len(sessionIDs) == 0 {
		return out, nil
	}
	q := questionSelect + ` WHERE s.session_id IN (` + placeholders(len(sessionIDs)) + `) AND q.state = 'open' ORDER BY q.session_pk ASC, q.asked_at ASC, q.id ASC`
	args := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		args[i] = id
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanSessionQuestion(rows)
		if err != nil {
			return nil, err
		}
		out[v.SessionPK] = append(out[v.SessionPK], v)
	}
	return out, rows.Err()
}

// AnswerSessionQuestion records the user's answers against an open
// question. Rejects if the row isn't open (you can't answer an
// answered/cancelled/abandoned row); rejects if the answer map
// doesn't match the stored payload's shape. answeredBy attributes
// the answer to a real actor — the audit log is downstream.
func (s *Store) AnswerSessionQuestion(id int64, answers model.QuestionAnswers, answeredBy string) (*model.SessionQuestion, error) {
	actor, err := ValidateActor(answeredBy)
	if err != nil {
		return nil, fmt.Errorf("answered_by: %w", err)
	}
	current, err := s.GetSessionQuestion(id)
	if err != nil {
		return nil, err
	}
	if current.State != model.QuestionOpen {
		return nil, fmt.Errorf("question %d is %s; only open questions can be answered", id, current.State)
	}
	if err := model.ValidateQuestionAnswers(current.Payload, answers); err != nil {
		return nil, err
	}
	answersJSON, err := model.MarshalQuestionAnswers(answers)
	if err != nil {
		return nil, fmt.Errorf("marshal answers: %w", err)
	}
	res, err := s.DB.Exec(`
		UPDATE agent_session_questions
		   SET answers_json = ?, state = 'answered',
		       answered_at = CURRENT_TIMESTAMP, answered_by = ?
		 WHERE id = ? AND state = 'open'`,
		answersJSON, actor, id,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Concurrent update slipped past the open check — re-read
		// and surface the actual state in the error.
		latest, gerr := s.GetSessionQuestion(id)
		if gerr != nil {
			return nil, gerr
		}
		return nil, fmt.Errorf("question %d is %s; only open questions can be answered", id, latest.State)
	}
	return s.GetSessionQuestion(id)
}

// CancelSessionQuestion dismisses an open question. Used by the
// modal's "Dismiss" button; also used when the channel is
// shutting down a row that was orphaned by a missing user
// response (caller decides — the store just records the state
// flip). by attributes the cancel to the actor.
func (s *Store) CancelSessionQuestion(id int64, by string) (*model.SessionQuestion, error) {
	actor, err := ValidateActor(by)
	if err != nil {
		return nil, fmt.Errorf("by: %w", err)
	}
	current, err := s.GetSessionQuestion(id)
	if err != nil {
		return nil, err
	}
	if current.State != model.QuestionOpen {
		return nil, fmt.Errorf("question %d is %s; only open questions can be cancelled", id, current.State)
	}
	res, err := s.DB.Exec(`
		UPDATE agent_session_questions
		   SET state = 'cancelled',
		       answered_at = CURRENT_TIMESTAMP, answered_by = ?
		 WHERE id = ? AND state = 'open'`,
		actor, id,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		latest, gerr := s.GetSessionQuestion(id)
		if gerr != nil {
			return nil, gerr
		}
		return nil, fmt.Errorf("question %d is %s; only open questions can be cancelled", id, latest.State)
	}
	return s.GetSessionQuestion(id)
}

// AbandonOpenQuestionsForSession flips every open question for one
// session to `abandoned`. The bacio channel calls this at startup
// (per its session-id) so any rows the previous channel process
// parked on are not left forever-open: the previous channel's
// in-memory parked-reply map is gone, the agent restarted with
// the channel, and there's no path to recover the reply. The
// abandoned rows are the audit-log breadcrumb that the question
// was asked and never delivered. Returns the number of rows
// flipped.
//
// Wraps the abandonOpenQuestionsTx helper (BACI-253) so the same
// UPDATE drives both this public entry point and the in-tx flip
// EndAgentSession runs as part of its session-end cascade.
func (s *Store) AbandonOpenQuestionsForSession(sessionID string) (int, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var sessPK int64
	if err := tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No row to abandon — keep the existing "silent no-op" shape;
			// the channel's startup janitor calls this against every
			// session id it has seen, including ones the store no longer
			// knows about.
			return 0, nil
		}
		return 0, err
	}
	n, err := abandonOpenQuestionsTx(tx, sessPK)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// abandonOpenQuestionsTx is the shared SQL the public
// AbandonOpenQuestionsForSession wrapper and store.EndAgentSession
// (BACI-253) both call to flip every still-open question for one
// session to `abandoned` inside the caller's transaction. Returns
// the number of rows flipped.
func abandonOpenQuestionsTx(tx *sql.Tx, sessPK int64) (int, error) {
	res, err := tx.Exec(`
		UPDATE agent_session_questions
		   SET state = 'abandoned',
		       answered_at = CURRENT_TIMESTAMP
		 WHERE state = 'open' AND session_pk = ?`, sessPK,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// HasOpenQuestionsForSession reports whether sessionID owns any
// open `ask_user_question` rows. The idlepinger uses it (BACI-253)
// as a skip-the-session gate: a session blocked on a user answer is
// correctly idle from a heartbeat perspective and must not be reaped,
// regardless of how stale its LastSeenAt looks. An unknown session
// id returns (false, nil) — the pinger only ever feeds in alive
// registered session ids, and the gate's "skip when true" posture
// means a missing row is the right negative-answer default.
func (s *Store) HasOpenQuestionsForSession(sessionID string) (bool, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return false, err
	}
	var found int
	err := s.DB.QueryRow(`
		SELECT 1
		  FROM agent_session_questions q
		  JOIN agent_sessions s ON s.id = q.session_pk
		 WHERE q.state = 'open' AND s.session_id = ?
		 LIMIT 1`, sessionID,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// HasOpenQuestionForJob reports whether the pipeline job has an open
// question — the controller engine's "halt Auto, waiting on the user"
// signal (§6.1). Questions are re-parented onto a job by the MCP
// ask_user_question path (a later phase); until then this always reads
// false, so the halt mechanism is wired but dormant.
func (s *Store) HasOpenQuestionForJob(jobID int64) (bool, error) {
	var found int
	err := s.DB.QueryRow(
		`SELECT 1 FROM agent_session_questions WHERE state = 'open' AND pipeline_job_id = ? LIMIT 1`,
		jobID,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// DrainSettledQuestionsForSession returns the answered + cancelled
// rows for the session — the rows the channel's poll tick needs to
// re-correlate with its in-memory parked-reply map and deliver
// the JSON-RPC result. The channel deduplicates per-process so
// returning a row twice across two ticks is fine — it just
// no-ops the second time. State-membership is the only filter:
// the caller decides what to do with each (deliver answer vs
// deliver tool-error). Newest first so a fresh row is at the
// front; the channel iterates either way.
func (s *Store) DrainSettledQuestionsForSession(sessionID string) ([]*model.SessionQuestion, error) {
	return s.ListSessionQuestions(sessionID, []model.QuestionState{
		model.QuestionAnswered, model.QuestionCancelled,
	})
}

func scanSessionQuestion(r rowScanner) (*model.SessionQuestion, error) {
	var (
		v           model.SessionQuestion
		stateStr    string
		payloadStr  string
		answersStr  sql.NullString
		answeredAt  sql.NullTime
		issueKey    sql.NullString
		answeredBy  sql.NullString
		pipelineJob sql.NullInt64
	)
	err := r.Scan(
		&v.ID, &v.SessionPK, &v.SessionID, &v.RequestUUID, &issueKey,
		&payloadStr, &answersStr, &stateStr,
		&v.AskedAt, &answeredAt, &v.AskedBy, &answeredBy,
		&pipelineJob,
	)
	if err != nil {
		return nil, err
	}
	v.State = model.QuestionState(stateStr)
	if issueKey.Valid {
		v.IssueKey = issueKey.String
	}
	if pipelineJob.Valid {
		id := pipelineJob.Int64
		v.PipelineJobID = &id
	}
	if answeredBy.Valid {
		v.AnsweredBy = answeredBy.String
	}
	if answeredAt.Valid {
		v.AnsweredAt = &answeredAt.Time
	}
	p, err := model.UnmarshalQuestionPayload(payloadStr)
	if err != nil {
		return nil, err
	}
	v.Payload = p
	if answersStr.Valid && answersStr.String != "" {
		a, err := model.UnmarshalQuestionAnswers(answersStr.String)
		if err != nil {
			return nil, err
		}
		v.Answers = a
	}
	return &v, nil
}
