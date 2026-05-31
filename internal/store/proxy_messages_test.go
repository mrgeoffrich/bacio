package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// userMsg / asstTurn are tiny builders so the tests read as conversations.
func userMsg(text string) model.AnthropicMessage {
	return model.AnthropicMessage{Role: "user", Content: []model.AnthropicBlock{{Type: "text", Text: text}}}
}

func asstTurn(text string, in, out int64) model.AnthropicTurn {
	return model.AnthropicTurn{
		Model:  "claude-opus-4-8",
		Blocks: []model.AnthropicBlock{{Type: "text", Text: text}},
		Usage:  model.AnthropicUsage{InputTokens: in, OutputTokens: out},
	}
}

// TestAddProxyMessage_JobTranscript inserts a two-capture primary thread plus
// one auxiliary capture against a dispatch, then asserts JobTranscript assembles
// the ordered primary thread, sums usage, and keeps the auxiliary turn separate.
func TestAddProxyMessage_JobTranscript(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(7)

	// Capture 1 (primary): the opening user turn + the first assistant turn.
	cap1 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp-main", MessageCount: 1,
		Turn: asstTurn("first answer", 100, 20),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap1, Delta: []model.AnthropicMessage{userMsg("first question")},
		IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add cap1: %v", err)
	}

	// Capture 2 (auxiliary): a title-gen probe — excluded from the primary thread.
	capAux := &model.ParsedCapture{
		Model: "claude-haiku", SystemFP: "fp-title", MessageCount: 1, HasOutputConfig: true,
		Turn: asstTurn(`{"title":"x"}`, 30, 5),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 2, DispatchID: &dispatchID, SessionID: "sess",
		Capture: capAux, Delta: nil, IsPrimary: false, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add capAux: %v", err)
	}

	// Capture 3 (primary): extends the thread — its delta is the new user turn.
	cap3 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp-main", MessageCount: 3,
		Turn: asstTurn("second answer", 160, 15),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 3, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap3, Delta: []model.AnthropicMessage{userMsg("second question")},
		IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add cap3: %v", err)
	}

	tr, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("JobTranscript: %v", err)
	}
	// Ordered primary thread: user, assistant, user, assistant. The aux probe
	// is not interleaved.
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	if len(tr.Messages) != len(wantRoles) {
		t.Fatalf("assembled %d messages, want %d: %+v", len(tr.Messages), len(wantRoles), tr.Messages)
	}
	for i, want := range wantRoles {
		if tr.Messages[i].Role != want {
			t.Errorf("message %d role = %q, want %q", i, tr.Messages[i].Role, want)
		}
	}
	if len(tr.Auxiliary) != 1 {
		t.Errorf("auxiliary turns = %d, want 1", len(tr.Auxiliary))
	}
	// Usage sums only the primary turns: input 100+160=260, output 20+15=35.
	if tr.Usage.InputTokens != 260 || tr.Usage.OutputTokens != 35 {
		t.Errorf("summed usage = %+v, want input 260 output 35", tr.Usage)
	}
}

// TestLatestThreadState asserts the recorder's carry reflects the newest primary
// row for a dispatch (and is empty for an unknown / nil dispatch).
func TestLatestThreadState(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(11)

	if st, err := s.LatestThreadState(nil); err != nil || st.SystemFP != "" {
		t.Fatalf("nil dispatch should yield zero state, got %+v err=%v", st, err)
	}
	if st, err := s.LatestThreadState(&dispatchID); err != nil || st.SystemFP != "" {
		t.Fatalf("unknown dispatch should yield zero state, got %+v err=%v", st, err)
	}

	for _, mc := range []int{1, 3} {
		cap := &model.ParsedCapture{Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: mc, Turn: asstTurn("a", 1, 1)}
		if _, err := s.AddProxyMessage(AddProxyMessageIn{
			ProxyRequestID: int64(mc), DispatchID: &dispatchID, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("add mc=%d: %v", mc, err)
		}
	}
	st, err := s.LatestThreadState(&dispatchID)
	if err != nil {
		t.Fatalf("LatestThreadState: %v", err)
	}
	if st.SystemFP != "fp" || st.Model != "claude-opus-4-8" || st.MessageCount != 3 {
		t.Errorf("latest thread state = %+v, want fp/opus/3 (newest primary)", st)
	}
	// Sanity: it threads into Classify cleanly.
	var _ anthropic.ThreadState = st
}

// TestCaptureMessage asserts a parsed-message row is fetchable by the
// proxy_requests id it parsed from, and ErrNotFound for an id with no row.
func TestCaptureMessage(t *testing.T) {
	s := newTestStore(t)
	cap := &model.ParsedCapture{Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("hi", 5, 2)}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 42, Capture: cap, Delta: []model.AnthropicMessage{userMsg("q")}, IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	pm, err := s.CaptureMessage(42)
	if err != nil {
		t.Fatalf("CaptureMessage: %v", err)
	}
	if pm.ProxyRequestID != 42 || pm.Usage.OutputTokens != 2 {
		t.Errorf("row = %+v, want proxy_request_id 42 / output 2", pm)
	}
	// turn_json round-trips to the stored turn.
	var turn model.AnthropicTurn
	if err := json.Unmarshal([]byte(pm.TurnJSON), &turn); err != nil {
		t.Fatalf("turn_json not valid JSON: %v", err)
	}
	if len(turn.Blocks) != 1 || turn.Blocks[0].Text != "hi" {
		t.Errorf("turn_json round-trip = %+v, want one text block 'hi'", turn.Blocks)
	}
	if _, err := s.CaptureMessage(999); err != ErrNotFound {
		t.Errorf("CaptureMessage(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestAddProxyMessage_BodyCap asserts an over-cap body is replaced with a marker
// rather than stored as a truncated (invalid) JSON blob.
func TestAddProxyMessage_BodyCap(t *testing.T) {
	s := newTestStore(t)
	huge := strings.Repeat("x", model.MaxProxyMessageBody+10)
	cap := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: huge}}},
	}
	pm, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.HasPrefix(pm.TurnJSON, "[proxy message body truncated") {
		t.Errorf("over-cap turn_json should be a marker, got %q…", pm.TurnJSON[:40])
	}
}

// TestPruneProxyMessages asserts rows older than the retention window are
// deleted while recent rows survive.
func TestPruneProxyMessages(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(5)
	old := &model.ParsedCapture{Model: "m", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("old", 1, 1)}
	recent := &model.ParsedCapture{Model: "m", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("new", 1, 1)}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, DispatchID: &dispatchID, Capture: old, IsPrimary: true,
		StartedAt: time.Now().Add(-2 * ProxyRequestRetention),
	}); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 2, DispatchID: &dispatchID, Capture: recent, IsPrimary: true,
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add recent: %v", err)
	}
	if err := pruneProxyMessages(s.DB, ProxyRequestRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	tr, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("JobTranscript: %v", err)
	}
	// Only the recent capture's turn survives → one assistant message.
	var asst int
	for _, m := range tr.Messages {
		if m.Role == "assistant" {
			asst++
		}
	}
	if asst != 1 {
		t.Errorf("after prune: %d assistant messages, want 1 (old pruned)", asst)
	}
}
