package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddProxyMessageIn is the validated tuple AddProxyMessage consumes — one
// BACI-306 parsed Anthropic SSE turn. ProxyRequestID points back at the
// proxy_requests index row; DispatchID/SessionID/ClaudeAgentID are the
// correlation lifted from that row; the parsed shape is in Delta/Turn (the
// store serialises and caps them). IsPrimary is the classification flag.
type AddProxyMessageIn struct {
	ProxyRequestID int64
	DispatchID     *int64
	SessionID      string
	ClaudeAgentID  string
	Capture        *model.ParsedCapture
	Delta          []model.AnthropicMessage
	IsPrimary      bool
	StartedAt      time.Time
}

// AddProxyMessage persists one parsed capture. Like AddProxyRequest, the
// machine-generated fields are clamped (not rejected) — a capture row is a
// best-effort observation. delta_json / turn_json are serialised and capped at
// MaxProxyMessageBody per direction; a body past the cap is replaced with a
// marker so the row still lands (the raw .http remains ground truth). Returns
// the freshly-inserted row.
func (s *Store) AddProxyMessage(in AddProxyMessageIn) (*model.ProxyMessage, error) {
	if in.Capture == nil {
		return nil, errors.New("AddProxyMessage: nil capture")
	}
	pc := in.Capture

	deltaJSON := capProxyMessageBody(mustJSON(in.Delta))
	turnJSON := capProxyMessageBody(mustJSON(pc.Turn))

	sessionID := clampProxyField(in.SessionID, model.MaxProxySessionIDLen)
	claudeAgentID := clampProxyField(in.ClaudeAgentID, model.MaxProxyAgentIDLen)
	model_ := clampProxyField(pc.Model, model.MaxProxyContentTypeLen)
	systemFP := clampProxyField(pc.SystemFP, 64) // sha256 hex is 64 chars.
	stopReason := clampProxyField(pc.Turn.StopReason, model.MaxProxyContentTypeLen)

	started := in.StartedAt
	if started.IsZero() {
		started = time.Now()
	}

	u := pc.Turn.Usage
	res, err := s.DB.Exec(`
		INSERT INTO proxy_messages
		    (proxy_request_id, dispatch_id, session_id, claude_agent_id,
		     model, system_fingerprint, message_count, is_primary, stop_reason,
		     delta_json, turn_json,
		     input_tokens, output_tokens, cache_creation_tokens,
		     cache_read_tokens, thinking_tokens, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ProxyRequestID, in.DispatchID, sessionID, claudeAgentID,
		model_, systemFP, pc.MessageCount, proxyBit(in.IsPrimary), stopReason,
		deltaJSON, turnJSON,
		u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens,
		u.CacheReadInputTokens, u.ThinkingTokens, started.UTC(),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetProxyMessage(id)
}

// capProxyMessageBody clamps a serialised body to MaxProxyMessageBody, replacing
// an over-cap body with a marker rather than storing a truncated (invalid) JSON
// blob. The raw .http file remains the ground truth for a capped capture.
func capProxyMessageBody(body string) string {
	if len(body) <= model.MaxProxyMessageBody {
		return body
	}
	return fmt.Sprintf("[proxy message body truncated at %d bytes]", model.MaxProxyMessageBody)
}

// mustJSON marshals v, returning "" on the (practically impossible) error so a
// capture row still lands without its body rather than failing the insert.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

const proxyMessageSelect = `
	SELECT id, proxy_request_id, dispatch_id, session_id, claude_agent_id,
	       model, system_fingerprint, message_count, is_primary, stop_reason,
	       delta_json, turn_json,
	       input_tokens, output_tokens, cache_creation_tokens,
	       cache_read_tokens, thinking_tokens, started_at
	FROM proxy_messages`

// GetProxyMessage fetches one parsed-message row by primary key, or ErrNotFound.
func (s *Store) GetProxyMessage(id int64) (*model.ProxyMessage, error) {
	row := s.DB.QueryRow(proxyMessageSelect+` WHERE id = ?`, id)
	pm, err := scanProxyMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pm, err
}

// CaptureMessage returns the parsed-message row for a given proxy_requests id,
// or ErrNotFound when that capture wasn't parseable (non-stream / truncated) and
// so has no proxy_messages row. The `bacio proxy capture <id>` read surface
// keys on the proxy_requests id (the id the user sees in `proxy stats` / the
// raw file), so this is the natural lookup.
func (s *Store) CaptureMessage(proxyRequestID int64) (*model.ProxyMessage, error) {
	row := s.DB.QueryRow(proxyMessageSelect+` WHERE proxy_request_id = ? ORDER BY id LIMIT 1`, proxyRequestID)
	pm, err := scanProxyMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pm, err
}

// LatestThreadState returns the carry the recorder threads into Classify for a
// dispatch: the (system fingerprint, model, message count) of the most-recent
// PRIMARY row for that dispatch. The zero value means no primary thread is
// established yet (the first capture establishes it). Called off the request
// path by the recorder before it classifies a fresh capture.
func (s *Store) LatestThreadState(dispatchID *int64) (anthropic.ThreadState, error) {
	if dispatchID == nil {
		return anthropic.ThreadState{}, nil
	}
	row := s.DB.QueryRow(`
		SELECT system_fingerprint, model, message_count
		FROM proxy_messages
		WHERE dispatch_id = ? AND is_primary = 1
		ORDER BY id DESC LIMIT 1`, *dispatchID)
	var st anthropic.ThreadState
	err := row.Scan(&st.SystemFP, &st.Model, &st.MessageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return anthropic.ThreadState{}, nil
	}
	if err != nil {
		return anthropic.ThreadState{}, err
	}
	return st, nil
}

// JobTranscript assembles a dispatch's ordered message transcript from its
// proxy_messages rows. It reads the rows in capture order (ORDER BY id), rebuilds
// each capture's delta + turn from the persisted JSON, and folds them through
// anthropic.AssembleTranscript — primary rows into the ordered thread + summed
// usage, auxiliary rows kept separate. Returns ErrNotFound when the dispatch has
// no parsed captures.
func (s *Store) JobTranscript(dispatchID int64) (*model.AnthropicTranscript, error) {
	rows, err := s.DB.Query(`
		SELECT is_primary, delta_json, turn_json
		FROM proxy_messages
		WHERE dispatch_id = ?
		ORDER BY id`, dispatchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assembled []anthropic.AssembledRow
	for rows.Next() {
		var (
			isPrimary int
			deltaJSON string
			turnJSON  string
		)
		if err := rows.Scan(&isPrimary, &deltaJSON, &turnJSON); err != nil {
			return nil, err
		}
		var (
			delta []model.AnthropicMessage
			turn  model.AnthropicTurn
		)
		// A truncated body won't unmarshal — fall back to an empty value so the
		// row still contributes its place in the ordering rather than failing.
		_ = json.Unmarshal([]byte(deltaJSON), &delta)
		_ = json.Unmarshal([]byte(turnJSON), &turn)
		assembled = append(assembled, anthropic.AssembledRow{
			IsPrimary: isPrimary != 0,
			Delta:     delta,
			Turn:      turn,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(assembled) == 0 {
		return nil, ErrNotFound
	}
	id := dispatchID
	return anthropic.AssembleTranscript(&id, assembled), nil
}

func scanProxyMessage(r rowScanner) (*model.ProxyMessage, error) {
	var v model.ProxyMessage
	var isPrimary int
	var dispatchID sql.NullInt64
	if err := r.Scan(
		&v.ID, &v.ProxyRequestID, &dispatchID, &v.SessionID, &v.ClaudeAgentID,
		&v.Model, &v.SystemFP, &v.MessageCount, &isPrimary, &v.StopReason,
		&v.DeltaJSON, &v.TurnJSON,
		&v.Usage.InputTokens, &v.Usage.OutputTokens, &v.Usage.CacheCreationInputTokens,
		&v.Usage.CacheReadInputTokens, &v.Usage.ThinkingTokens, &v.StartedAt,
	); err != nil {
		return nil, err
	}
	v.IsPrimary = isPrimary != 0
	if dispatchID.Valid {
		id := dispatchID.Int64
		v.DispatchID = &id
	}
	return &v, nil
}
