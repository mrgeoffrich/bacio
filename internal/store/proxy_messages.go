package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ProxyMessageFilter parameterises the BACI-320 content search over the parsed
// proxy_messages bodies (`bacio proxy grep` / GET /proxy/search). Query is the
// case-insensitive substring to find; Role ("assistant" | "user", optional)
// scopes which column is scanned — assistant → the reconstructed turn (turn_json),
// user → the request delta turns (delta_json), unset → both; Block (one of
// "text"|"thinking"|"tool_use"|"tool_result", optional) keeps only matches found
// in that block type. DispatchID / SessionID / ClaudeAgentID are the same
// correlation filters ProxyRequestFilter carries (proxy_messages mirrors those
// columns), Since is an inclusive lower bound on started_at (nil = no bound), and
// Limit caps the match lines returned newest-first — <= 0 falls back to
// defaultProxyListLimit, hard-capped at defaultProxyRequestLimit.
type ProxyMessageFilter struct {
	Query         string
	Role          string
	Block         string
	DispatchID    *int64
	SessionID     string
	ClaudeAgentID string
	Since         *time.Time
	Limit         int
}

// SearchProxyMessages runs the BACI-320 content grep over the parsed message
// bodies: a case-insensitive substring match keyed on Role (delta_json for the
// user side, turn_json for the assistant side, or both), narrowed by the same
// correlation/since filters the capture list uses. The SQL LIKE finds candidate
// rows; the snippet is rendered in Go — each matched row's JSON is re-parsed into
// AnthropicMessage[] / AnthropicTurn and its blocks are walked, so we match real
// block content (never a raw JSON field name or an input_json_delta key) and emit
// one ProxyMessageMatch per matching block. A truncated body (replaced with the
// marker past MaxProxyMessageBody) won't parse into blocks, so such a row simply
// yields no match line — the raw .http stays ground truth. Always returns
// non-nil. Limit bounds the emitted match lines (the unit a reader counts), not
// the rows scanned.
func (s *Store) SearchProxyMessages(f ProxyMessageFilter) ([]*model.ProxyMessageMatch, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultProxyListLimit
	}
	if limit > defaultProxyRequestLimit {
		limit = defaultProxyRequestLimit
	}

	// Escape the LIKE wildcards in the needle so a literal % / _ in the search
	// text matches literally (ESCAPE '\' below). SQLite LIKE is ASCII
	// case-insensitive by default, so the needle isn't lowercased for the SQL;
	// the Go block walk lowercases both sides for its own match.
	pattern := "%" + likeEscape(f.Query) + "%"

	q := `
		SELECT proxy_request_id, dispatch_id, session_id, claude_agent_id,
		       delta_json, turn_json
		FROM proxy_messages`
	var (
		args   []any
		wheres []string
	)
	switch f.Role {
	case "user":
		wheres = append(wheres, `delta_json LIKE ? ESCAPE '\'`)
		args = append(args, pattern)
	case "assistant":
		wheres = append(wheres, `turn_json LIKE ? ESCAPE '\'`)
		args = append(args, pattern)
	default:
		wheres = append(wheres, `(delta_json LIKE ? ESCAPE '\' OR turn_json LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	if f.DispatchID != nil {
		wheres = append(wheres, "dispatch_id = ?")
		args = append(args, *f.DispatchID)
	}
	if f.SessionID != "" {
		wheres = append(wheres, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.ClaudeAgentID != "" {
		wheres = append(wheres, "claude_agent_id = ?")
		args = append(args, f.ClaudeAgentID)
	}
	if f.Since != nil {
		wheres = append(wheres, "started_at >= ?")
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	q += " WHERE " + strings.Join(wheres, " AND ")
	// Newest-first, and cap the candidate rows scanned at the same hard ceiling
	// the match-line limit uses — a row matching in many blocks could otherwise
	// fan out, but the candidate scan stays bounded regardless.
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, defaultProxyRequestLimit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needle := strings.ToLower(f.Query)
	out := make([]*model.ProxyMessageMatch, 0)
	for rows.Next() {
		var (
			proxyRequestID int64
			dispatchID     sql.NullInt64
			sessionID      string
			claudeAgentID  string
			deltaJSON      string
			turnJSON       string
		)
		if err := rows.Scan(&proxyRequestID, &dispatchID, &sessionID, &claudeAgentID, &deltaJSON, &turnJSON); err != nil {
			return nil, err
		}
		var dispPtr *int64
		if dispatchID.Valid {
			id := dispatchID.Int64
			dispPtr = &id
		}
		base := func(role, block, snippet string) *model.ProxyMessageMatch {
			return &model.ProxyMessageMatch{
				ProxyRequestID: proxyRequestID,
				DispatchID:     dispPtr,
				Role:           role,
				Block:          block,
				Snippet:        snippet,
				SessionID:      sessionID,
				ClaudeAgentID:  claudeAgentID,
			}
		}

		// User side — the request delta turns (a slice of messages). Walked only
		// when Role isn't pinned to assistant.
		if f.Role != "assistant" {
			var delta []model.AnthropicMessage
			if json.Unmarshal([]byte(deltaJSON), &delta) == nil {
				for _, msg := range delta {
					for _, b := range msg.Content {
						if !blockAdmitted(f.Block, b.Type) {
							continue
						}
						if snippet, ok := matchBlock(b, needle); ok {
							out = append(out, base("user", b.Type, snippet))
							if len(out) >= limit {
								return out, rows.Err()
							}
						}
					}
				}
			}
		}

		// Assistant side — the reconstructed turn. Walked only when Role isn't
		// pinned to user.
		if f.Role != "user" {
			var turn model.AnthropicTurn
			if json.Unmarshal([]byte(turnJSON), &turn) == nil {
				for _, b := range turn.Blocks {
					if !blockAdmitted(f.Block, b.Type) {
						continue
					}
					if snippet, ok := matchBlock(b, needle); ok {
						out = append(out, base("assistant", b.Type, snippet))
						if len(out) >= limit {
							return out, rows.Err()
						}
					}
				}
			}
		}
	}
	return out, rows.Err()
}

// blockAdmitted reports whether a block of the given type passes the optional
// --block scope (empty = every type admitted).
func blockAdmitted(want, blockType string) bool {
	return want == "" || want == blockType
}

// matchBlock extracts a block's searchable text by type and reports whether it
// contains the (already-lowercased) needle, returning the collapsed snippet. The
// extraction mirrors printAnthropicBlock's per-type fields so the matched snippet
// reads the same way `proxy capture` renders that block:
//   - text         → Text
//   - thinking     → Thinking
//   - tool_use     → Name + " " + Input
//   - tool_result  → Content
func matchBlock(b model.AnthropicBlock, needle string) (string, bool) {
	var text string
	switch b.Type {
	case "text":
		text = b.Text
	case "thinking":
		text = b.Thinking
	case "tool_use":
		text = strings.TrimSpace(b.Name + " " + string(b.Input))
	case "tool_result":
		text = string(b.Content)
	default:
		return "", false
	}
	if !strings.Contains(strings.ToLower(text), needle) {
		return "", false
	}
	return collapseSnippet(text), true
}

// collapseSnippet collapses whitespace and caps a matched block body for the
// match-line snippet. It mirrors the cli oneLine helper so the rendered snippet
// matches how `proxy capture` shows a block; done in the store so both transports
// emit an identical snippet.
func collapseSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// likeEscape escapes the LIKE wildcard metacharacters (% _ \) in a search needle
// so a literal occurrence matches literally under an `ESCAPE '\'` clause.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
