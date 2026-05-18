package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SessionQuestion is one clarification question an agent asked the
// user through the bacio channel's `ask_user_question` MCP tool
// (BACI-53). It mirrors a single AskUserQuestion call: the questions
// array goes in, the user's answers come back. Local-only — never
// synced.
//
// RequestUUID is the UUIDv7 the channel mints at ask time. The
// channel uses it in its in-memory request_uuid -> JSON-RPC id map
// to correlate the parked reply with the answered row when it
// re-polls.
//
// IssueKey is the issue key of the session's open claim at ask time,
// when there was one. Empty for asks made without an active claim
// (e.g. the agent asking a setup question before claiming anything).
//
// AskedBy is the persistent agent identity that posed the question.
// AnsweredBy is the actor who responded (typically the OS user via
// the TUI/desktop, or "api" for REST callers without an X-Actor).
// Empty until the row leaves the open state.
type SessionQuestion struct {
	// ID / SessionPK are server-time fields — `omitempty` so dry-run
	// projections (which can't know them yet) emit the same JSON shape
	// as real calls minus the unknown fields.
	ID          int64  `json:"id,omitempty"`
	SessionPK   int64  `json:"session_pk,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	RequestUUID string `json:"request_uuid"`
	IssueKey    string `json:"issue_key,omitempty"`

	Payload QuestionPayload `json:"payload"`
	Answers QuestionAnswers `json:"answers,omitempty"`

	State QuestionState `json:"state"`

	AskedAt    time.Time  `json:"asked_at"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	AskedBy    string     `json:"asked_by"`
	AnsweredBy string     `json:"answered_by,omitempty"`
}

// QuestionState is the lifecycle marker on agent_session_questions.
// See the table comment in schema.sql for the full description.
type QuestionState string

const (
	QuestionOpen      QuestionState = "open"
	QuestionAnswered  QuestionState = "answered"
	QuestionCancelled QuestionState = "cancelled"
	QuestionAbandoned QuestionState = "abandoned"
)

var allQuestionStates = []QuestionState{
	QuestionOpen, QuestionAnswered, QuestionCancelled, QuestionAbandoned,
}

// AllQuestionStates returns the lifecycle markers in canonical order.
func AllQuestionStates() []QuestionState {
	return append([]QuestionState(nil), allQuestionStates...)
}

// ParseQuestionState accepts the canonical lowercase form. Unknown
// values error — no dash/space normalisation (these are short
// internal identifiers, agents should send them verbatim).
func ParseQuestionState(s string) (QuestionState, error) {
	s = strings.TrimSpace(s)
	for _, st := range allQuestionStates {
		if string(st) == s {
			return st, nil
		}
	}
	names := make([]string, len(allQuestionStates))
	for i, st := range allQuestionStates {
		names[i] = string(st)
	}
	return "", fmt.Errorf("unknown question state %q (valid: %s)", s, strings.Join(names, ", "))
}

// QuestionPayload mirrors the input shape of Claude Code's built-in
// AskUserQuestion tool so an agent calling either tool builds the
// same payload. Validated at the store boundary by
// ValidateQuestionPayload.
type QuestionPayload struct {
	Questions []QuestionItem `json:"questions"`
}

// QuestionItem is one multi-choice question. Header is the short
// label rendered next to the question text in the UI (capped at
// MaxQuestionHeaderLen, matching the AskUserQuestion docs). Options
// is the 2-4 entry choice set. MultiSelect lets the user pick more
// than one option (rendered as checkboxes vs. radios).
type QuestionItem struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
	Options     []QuestionOption `json:"options"`
}

// QuestionOption is one choice on a question. Description is the
// optional fine-print rendered under the label.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionAnswers maps question text -> answer. For a single-select
// question the value is a string (an option label, or free-text
// "Other"); for a multi-select question the value is []string. The
// shape is constrained by ValidateQuestionAnswers (against the
// question's MultiSelect flag) so a renderer can rely on it.
type QuestionAnswers map[string]any

// Limits enforced by ValidateQuestionPayload. These mirror the
// AskUserQuestion docs: 1-4 questions, 2-4 options per question,
// short header. The body caps are generous enough for a real
// clarification but tight enough to fail loud on a runaway paste.
const (
	MinQuestionsPerAsk    = 1
	MaxQuestionsPerAsk    = 4
	MinOptionsPerQuestion = 2
	MaxOptionsPerQuestion = 4
	MaxQuestionHeaderLen  = 12
	MaxQuestionTextLen    = 1024
	MaxQuestionOptionLen  = 200
	MaxQuestionDescLen    = 500
	// MaxOtherAnswerLen caps the free-text "Other" response a user can
	// type when they don't pick a built-in option. Single-line by
	// convention — the modal renders a text input, not a textarea.
	MaxOtherAnswerLen = 1024
)

// ValidateQuestionPayload runs the boundary check on an
// AskUserQuestion-shaped payload before it lands in the table.
// Mirrors the AskUserQuestion docs: 1-4 questions, 2-4 options per
// question, short header (<=12 chars), no control characters in any
// single-line field. Question / label text is required (non-blank).
// Option labels within a single question must be unique so an
// answer can address the chosen option unambiguously.
func ValidateQuestionPayload(p QuestionPayload) error {
	n := len(p.Questions)
	if n < MinQuestionsPerAsk || n > MaxQuestionsPerAsk {
		return fmt.Errorf("payload must carry between %d and %d questions; got %d", MinQuestionsPerAsk, MaxQuestionsPerAsk, n)
	}
	seenQ := make(map[string]bool, n)
	for i, q := range p.Questions {
		if err := validateQuestionItem(i, q); err != nil {
			return err
		}
		if seenQ[q.Question] {
			return fmt.Errorf("question %d: duplicate question text %q (each question must be unique within an ask — answers map by text)", i, q.Question)
		}
		seenQ[q.Question] = true
	}
	return nil
}

func validateQuestionItem(idx int, q QuestionItem) error {
	if err := validateAskString(q.Question, "question", MaxQuestionTextLen, true); err != nil {
		return fmt.Errorf("question %d: %w", idx, err)
	}
	if err := validateAskString(q.Header, "header", MaxQuestionHeaderLen, true); err != nil {
		return fmt.Errorf("question %d: %w", idx, err)
	}
	// AskUserQuestion's header is the short "tag" rendered next to
	// the question — utf8 character count, not byte count, so the
	// length check above uses len() but here we double-check by
	// rune count (12 chars per docs).
	if utf8.RuneCountInString(q.Header) > MaxQuestionHeaderLen {
		return fmt.Errorf("question %d: header too long (%d chars; max %d)", idx, utf8.RuneCountInString(q.Header), MaxQuestionHeaderLen)
	}
	m := len(q.Options)
	if m < MinOptionsPerQuestion || m > MaxOptionsPerQuestion {
		return fmt.Errorf("question %d: must carry between %d and %d options; got %d", idx, MinOptionsPerQuestion, MaxOptionsPerQuestion, m)
	}
	seen := make(map[string]bool, m)
	for j, opt := range q.Options {
		if err := validateAskString(opt.Label, "option label", MaxQuestionOptionLen, true); err != nil {
			return fmt.Errorf("question %d option %d: %w", idx, j, err)
		}
		if opt.Description != "" {
			if err := validateAskString(opt.Description, "option description", MaxQuestionDescLen, false); err != nil {
				return fmt.Errorf("question %d option %d: %w", idx, j, err)
			}
		}
		if seen[opt.Label] {
			return fmt.Errorf("question %d: duplicate option label %q", idx, opt.Label)
		}
		seen[opt.Label] = true
	}
	return nil
}

// validateAskString is the question-payload-local sibling of the
// store-package validators. Lives in model so the channel handler
// can also run the check before parking a reply (belt-and-braces:
// the store re-runs the same validator at the boundary).
func validateAskString(s, field string, maxLen int, required bool) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	if required && strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(s) > maxLen {
		return fmt.Errorf("%s too long: %d chars, max %d", field, len(s), maxLen)
	}
	for _, r := range s {
		if isDisallowedAskControl(r) {
			return fmt.Errorf("%s contains a disallowed control character (U+%04X)", field, r)
		}
	}
	return nil
}

// isDisallowedAskControl rejects all C0 controls and DEL. Questions
// and option labels are single-line — newlines/tabs in the prompt
// would scramble the UI rendering, and AskUserQuestion's wire
// format treats them as single-line too.
func isDisallowedAskControl(r rune) bool {
	return (r >= 0x00 && r <= 0x1F) || r == 0x7F
}

// ValidateQuestionAnswers checks an answer map against the stored
// payload: every key must match an existing question, the value
// type must match MultiSelect (string vs []string), each selected
// label must either match an option label or be a free-text
// "Other" (subject to MaxOtherAnswerLen). At least one question
// must be answered — if the user truly wanted to skip everything
// they should cancel the ask, not submit an empty answer.
func ValidateQuestionAnswers(p QuestionPayload, a QuestionAnswers) error {
	if len(a) == 0 {
		return fmt.Errorf("answers cannot be empty; cancel the question instead of submitting nothing")
	}
	byText := make(map[string]QuestionItem, len(p.Questions))
	for _, q := range p.Questions {
		byText[q.Question] = q
	}
	for key, val := range a {
		q, ok := byText[key]
		if !ok {
			return fmt.Errorf("answer key %q does not match any question in the payload", key)
		}
		if q.MultiSelect {
			arr, ok := val.([]any)
			if !ok {
				// JSON-decoded multi-select answers can also arrive as
				// []string when the caller built them in Go directly.
				if strs, ok := val.([]string); ok {
					arr = make([]any, len(strs))
					for i, s := range strs {
						arr[i] = s
					}
				} else {
					return fmt.Errorf("answer for %q must be an array (multi-select)", key)
				}
			}
			if len(arr) == 0 {
				return fmt.Errorf("answer for %q must select at least one option (use the cancel flow instead of an empty multi-select)", key)
			}
			for i, v := range arr {
				s, ok := v.(string)
				if !ok {
					return fmt.Errorf("answer for %q index %d must be a string", key, i)
				}
				if err := validateAnswerLabel(s, q); err != nil {
					return fmt.Errorf("answer for %q index %d: %w", key, i, err)
				}
			}
		} else {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("answer for %q must be a string (single-select)", key)
			}
			if err := validateAnswerLabel(s, q); err != nil {
				return fmt.Errorf("answer for %q: %w", key, err)
			}
		}
	}
	return nil
}

// validateAnswerLabel checks a single string answer: either it
// matches one of the question's option labels exactly, or it's a
// free-text response capped at MaxOtherAnswerLen. The "Other"
// path is what lets the UI render a per-question free-text input
// alongside the choice list.
func validateAnswerLabel(s string, q QuestionItem) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("answer value is required")
	}
	for _, opt := range q.Options {
		if opt.Label == s {
			return nil
		}
	}
	// Free-text "Other" path — validate like a long single-line
	// payload field. Multi-line free text is deliberately rejected
	// (it breaks the modal's textinput contract).
	return validateAskString(s, "answer", MaxOtherAnswerLen, true)
}

// MarshalPayload serialises a payload for storage. Round-trips
// through json.Marshal so the column carries a canonical encoding
// (key order is stable per Go's json package).
func MarshalQuestionPayload(p QuestionPayload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalQuestionPayload reverses MarshalQuestionPayload. A
// caller decoding a row goes through this so the JSON shape stays
// in one place.
func UnmarshalQuestionPayload(s string) (QuestionPayload, error) {
	var p QuestionPayload
	if s == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return QuestionPayload{}, fmt.Errorf("unmarshal question payload: %w", err)
	}
	return p, nil
}

// MarshalQuestionAnswers serialises an answer map for storage.
func MarshalQuestionAnswers(a QuestionAnswers) (string, error) {
	if len(a) == 0 {
		return "", nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalQuestionAnswers reverses MarshalQuestionAnswers.
func UnmarshalQuestionAnswers(s string) (QuestionAnswers, error) {
	if s == "" {
		return nil, nil
	}
	var a QuestionAnswers
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return nil, fmt.Errorf("unmarshal question answers: %w", err)
	}
	return a, nil
}
