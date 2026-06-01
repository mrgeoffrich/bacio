package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// renderCapture builds a minimal valid .http capture in the same
// ==== REQUEST ==== / ==== RESPONSE ==== layout the recorder's renderRawCapture
// writes, so anthropic.ParseCapture (the reparse substrate) decodes it. The
// request carries `model`, a `system` prompt, and `msgCount` user/assistant
// messages; the response is an SSE stream that reconstructs one assistant turn
// with `text` as its single text block and `in`/`out` usage. This keeps the
// reparse store tests self-contained — no cross-package testdata dependency.
func renderCapture(model_, system, text string, msgCount int, in, out int64) []byte {
	messages := make([]string, 0, msgCount)
	for i := 0; i < msgCount; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// content is a block list (the request shape AnthropicMessage decodes).
		messages = append(messages, fmt.Sprintf(
			`{"role":%q,"content":[{"type":"text","text":%q}]}`, role, fmt.Sprintf("msg %d", i)))
	}
	reqBody := fmt.Sprintf(`{"model":%q,"system":%q,"messages":[%s]}`,
		model_, system, strings.Join(messages, ","))

	// One assistant turn: message_start (usage in/cache), one text block, then
	// message_delta with output tokens + stop_reason — the minimum decodeSSE folds.
	sse := strings.Join([]string{
		fmt.Sprintf(`data: {"type":"message_start","message":{"model":%q,"usage":{"input_tokens":%d}}}`, model_, in),
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text),
		`data: {"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, out),
	}, "\n\n")

	var b strings.Builder
	b.WriteString("==== REQUEST ====\r\n")
	b.WriteString("POST /v1/messages HTTP/1.1\r\n")
	b.WriteString("Content-Type: application/json\r\n")
	b.WriteString("\r\n")
	b.WriteString(reqBody)
	b.WriteString("\r\n==== RESPONSE ====\r\n")
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("Content-Type: text/event-stream\r\n")
	b.WriteString("\r\n")
	b.WriteString(sse)
	return []byte(b.String())
}

// seedCapture writes a rendered .http file under a per-test temp dir and inserts
// the matching proxy_requests index row (is_anthropic=1, is_stream=1) correlated
// to dispatchID, mirroring what the live recorder lands minus the proxy_messages
// row. It returns the inserted row so a test can assert on its id / path. started
// controls both the row's started_at and the on-disk ordering.
func seedCapture(t *testing.T, s *Store, dir string, dispatchID int64, started time.Time, raw []byte) *model.ProxyRequest {
	t.Helper()
	name := fmt.Sprintf("%d.http", started.UnixNano())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write capture %s: %v", name, err)
	}
	d := dispatchID
	pr, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, RawLogPath: path, StartedAt: started, EndedAt: started,
		ContentType: "text/event-stream", IsStream: true, IsAnthropic: true,
		DispatchID: &d,
	})
	if err != nil {
		t.Fatalf("seed capture row: %v", err)
	}
	return pr
}

// livesParse parses + classifies + inserts a capture the way the live recorder
// does — used to build the baseline JobTranscript the reparsed one must match.
func liveParse(t *testing.T, s *Store, pr *model.ProxyRequest, dispatchID int64) {
	t.Helper()
	raw, err := os.ReadFile(pr.RawLogPath)
	if err != nil {
		t.Fatalf("read capture for live parse: %v", err)
	}
	prev, err := s.LatestThreadState(&dispatchID)
	if err != nil {
		t.Fatalf("latest thread state: %v", err)
	}
	pc, isPrimary, delta, err := anthropic.ParseAndClassify(raw, prev)
	if err != nil {
		t.Fatalf("live parse: %v", err)
	}
	d := dispatchID
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: pr.ID, DispatchID: &d,
		Capture: pc, Delta: delta, IsPrimary: isPrimary, StartedAt: pr.StartedAt,
	}); err != nil {
		t.Fatalf("live add message: %v", err)
	}
}

// TestReparseDispatch_FullyUnparsed_MatchesBaseline seeds a 2-capture primary
// thread, live-parses it to capture a baseline JobTranscript, deletes all the
// proxy_messages rows (simulating the live path having missed them), then runs
// ReparseDispatch and asserts the reparsed transcript's ordering and summed usage
// equal the baseline.
func TestReparseDispatch_FullyUnparsed_MatchesBaseline(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	dispatchID := int64(701)
	base := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)

	// Two captures in the same primary thread: turn 1 (1 message), turn 2 (3
	// messages — the prior assistant echo + a new user turn).
	pr1 := seedCapture(t, s, dir, dispatchID, base, renderCapture("claude-opus-4-8", "sys", "first answer", 1, 100, 20))
	pr2 := seedCapture(t, s, dir, dispatchID, base.Add(time.Minute), renderCapture("claude-opus-4-8", "sys", "second answer", 3, 160, 15))

	// Baseline: live-parse both, snapshot the transcript, then wipe the rows.
	liveParse(t, s, pr1, dispatchID)
	liveParse(t, s, pr2, dispatchID)
	baseline, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("baseline JobTranscript: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM proxy_messages WHERE dispatch_id = ?`, dispatchID); err != nil {
		t.Fatalf("wipe proxy_messages: %v", err)
	}
	if _, err := s.JobTranscript(dispatchID); err != ErrNotFound {
		t.Fatalf("after wipe JobTranscript err = %v, want ErrNotFound (fully unparsed)", err)
	}

	// Reparse and compare to baseline.
	res, err := s.ReparseDispatch(dispatchID)
	if err != nil {
		t.Fatalf("ReparseDispatch: %v", err)
	}
	if res.DispatchesScanned != 1 || res.CapturesReparsed != 2 || res.CapturesFailed != 0 {
		t.Fatalf("result = %+v, want 1 scanned / 2 reparsed / 0 failed", res)
	}

	got, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("reparsed JobTranscript: %v", err)
	}
	if len(got.Messages) != len(baseline.Messages) {
		t.Fatalf("reparsed %d messages, baseline %d", len(got.Messages), len(baseline.Messages))
	}
	for i := range baseline.Messages {
		if got.Messages[i].Role != baseline.Messages[i].Role {
			t.Errorf("message %d role = %q, baseline %q", i, got.Messages[i].Role, baseline.Messages[i].Role)
		}
	}
	if got.Usage != baseline.Usage {
		t.Errorf("reparsed usage = %+v, baseline %+v", got.Usage, baseline.Usage)
	}
	// Sanity: usage is the sum of both primary turns (100+160 in, 20+15 out).
	if got.Usage.InputTokens != 260 || got.Usage.OutputTokens != 35 {
		t.Errorf("summed usage = %+v, want input 260 output 35", got.Usage)
	}

	// Idempotent: a second reparse is a no-op (rows already present).
	res2, err := s.ReparseDispatch(dispatchID)
	if err != nil {
		t.Fatalf("second ReparseDispatch: %v", err)
	}
	if res2.CapturesReparsed != 0 {
		t.Errorf("second reparse reparsed %d, want 0 (idempotent)", res2.CapturesReparsed)
	}
}

// TestReparseDispatch_TruncatedCaptureMarkedAndNotRetried seeds a malformed
// capture (a non-SSE body, which ParseCapture rejects), runs ReparseDispatch, and
// asserts it yields no proxy_messages row, stamps parse_failed_at, and a second
// sweep skips it (no repeated work).
func TestReparseDispatch_TruncatedCaptureMarkedAndNotRetried(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	dispatchID := int64(702)
	started := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)

	// A capture whose response is plain JSON, not SSE — ParseCapture returns
	// ErrNotStream, the unparseable case. Build it by hand rather than via
	// renderCapture (which always writes an SSE response).
	bad := "==== REQUEST ====\r\nPOST /v1/messages HTTP/1.1\r\nContent-Type: application/json\r\n\r\n{\"model\":\"m\"}\r\n" +
		"==== RESPONSE ====\r\nHTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\n\r\n{\"error\":\"truncated\"}"
	pr := seedCapture(t, s, dir, dispatchID, started, []byte(bad))

	res, err := s.ReparseDispatch(dispatchID)
	if err != nil {
		t.Fatalf("ReparseDispatch: %v", err)
	}
	if res.CapturesReparsed != 0 || res.CapturesFailed != 1 {
		t.Fatalf("result = %+v, want 0 reparsed / 1 failed", res)
	}
	if _, err := s.JobTranscript(dispatchID); err != ErrNotFound {
		t.Errorf("JobTranscript err = %v, want ErrNotFound (no row for unparseable capture)", err)
	}
	got, err := s.GetProxyRequest(pr.ID)
	if err != nil {
		t.Fatalf("GetProxyRequest: %v", err)
	}
	if got.ParseFailedAt == nil {
		t.Fatalf("parse_failed_at not stamped on the unparseable capture")
	}

	// Second sweep: the marked row is excluded from eligibility, so the dispatch
	// is no longer eligible and the sweep does no work.
	res2, err := s.ReparseUnparsedDispatches(ReparseOpts{QuietWindow: time.Nanosecond})
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if res2.Total() != 0 {
		t.Errorf("second sweep did work %+v, want 0 (marked capture not retried)", res2)
	}
}

// TestClearProxyParseFailed_RetryFailedRecoversCaptures is the BACI-323 recovery
// path: a parseable capture is stamped parse_failed_at (simulating a pre-fix
// marker-collision failure the parser has since been fixed for). A sweep without
// RetryFailed leaves it stamped and does no work ("already given up on stays given
// up on"); a sweep WITH RetryFailed clears the marker first and backfills the
// capture. The dry-run count must not mutate the marker.
func TestClearProxyParseFailed_RetryFailedRecoversCaptures(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-1 * time.Hour)
	good := renderCapture("claude-opus-4-8", "sys", "answer", 1, 10, 5)

	// A parseable capture that was nonetheless stamped failed (the bug under fix).
	dispatchID := int64(901)
	pr := seedCapture(t, s, dir, dispatchID, old, good)
	if err := s.MarkProxyRequestParseFailed(pr.ID); err != nil {
		t.Fatalf("stamp parse_failed_at: %v", err)
	}

	// (1) Sweep without RetryFailed: the marked capture is excluded → no work.
	res, err := s.ReparseUnparsedDispatches(ReparseOpts{QuietWindow: 2 * time.Minute, Now: now})
	if err != nil {
		t.Fatalf("sweep without retry: %v", err)
	}
	if res.Total() != 0 {
		t.Fatalf("sweep without retry did work %+v, want 0 (marked capture not retried)", res)
	}

	// (2) Dry-run projection with RetryFailed must count the recoverable capture
	// (it drops the parse_failed_at predicate) WITHOUT mutating the marker.
	proj, err := s.ProjectReparseUnparsedDispatches(ReparseOpts{QuietWindow: 2 * time.Minute, Now: now, RetryFailed: true})
	if err != nil {
		t.Fatalf("ProjectReparseUnparsedDispatches(RetryFailed): %v", err)
	}
	if proj.DispatchesScanned != 1 || proj.CapturesReparsed != 1 {
		t.Errorf("retry-failed projection = %+v, want 1 scanned / 1 reparsed", proj)
	}
	got, err := s.GetProxyRequest(pr.ID)
	if err != nil {
		t.Fatalf("GetProxyRequest after projection: %v", err)
	}
	if got.ParseFailedAt == nil {
		t.Fatalf("projection cleared the marker — dry-run must not mutate")
	}

	// (3) Sweep WITH RetryFailed: clears the marker first, then backfills.
	res2, err := s.ReparseUnparsedDispatches(ReparseOpts{QuietWindow: 2 * time.Minute, Now: now, RetryFailed: true})
	if err != nil {
		t.Fatalf("sweep with retry: %v", err)
	}
	if res2.DispatchesScanned != 1 || res2.CapturesReparsed != 1 {
		t.Fatalf("sweep with retry result = %+v, want 1 scanned / 1 reparsed", res2)
	}
	if _, err := s.JobTranscript(dispatchID); err != nil {
		t.Errorf("recovered dispatch should now have a transcript: %v", err)
	}
	got2, err := s.GetProxyRequest(pr.ID)
	if err != nil {
		t.Fatalf("GetProxyRequest after retry: %v", err)
	}
	if got2.ParseFailedAt != nil {
		t.Errorf("parse_failed_at still set after RetryFailed sweep, want cleared")
	}
}

// TestClearProxyParseFailed_Scopes asserts the dispatch-scoped clear (the
// `--dispatch <id> --retry-failed` path) only clears the named job's markers, and
// that a capture which already has a proxy_messages row is never resurrected.
func TestClearProxyParseFailed_Scopes(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-1 * time.Hour)
	good := renderCapture("claude-opus-4-8", "sys", "answer", 1, 10, 5)

	// Two failed-but-unparsed dispatches.
	dispA, dispB := int64(911), int64(912)
	prA := seedCapture(t, s, dir, dispA, old, good)
	prB := seedCapture(t, s, dir, dispB, old, good)
	for _, pr := range []*model.ProxyRequest{prA, prB} {
		if err := s.MarkProxyRequestParseFailed(pr.ID); err != nil {
			t.Fatalf("stamp parse_failed_at: %v", err)
		}
	}

	// An already-parsed capture stamped failed (shouldn't happen in practice, but
	// the clear must never resurrect a classified row).
	dispC := int64(913)
	prC := seedCapture(t, s, dir, dispC, old, good)
	liveParse(t, s, prC, dispC)
	if err := s.MarkProxyRequestParseFailed(prC.ID); err != nil {
		t.Fatalf("stamp parse_failed_at on parsed capture: %v", err)
	}

	// Scope the clear to dispatch A only.
	cleared, err := s.ClearProxyParseFailed(ClearParseFailedOpts{Dispatch: &dispA})
	if err != nil {
		t.Fatalf("ClearProxyParseFailed(dispA): %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared = %d, want 1 (only dispatch A)", cleared)
	}

	gotA, _ := s.GetProxyRequest(prA.ID)
	if gotA.ParseFailedAt != nil {
		t.Errorf("dispatch A marker not cleared")
	}
	gotB, _ := s.GetProxyRequest(prB.ID)
	if gotB.ParseFailedAt == nil {
		t.Errorf("dispatch B marker cleared, want left intact (out of scope)")
	}
	gotC, _ := s.GetProxyRequest(prC.ID)
	if gotC.ParseFailedAt == nil {
		t.Errorf("already-parsed capture's marker cleared, want left intact (has a proxy_messages row)")
	}
}

// TestReparseUnparsedDispatches_EligibilityAndQuietWindow asserts the sweep's
// eligibility predicate: dispatch_id NULL / non-anthropic / non-stream / already-
// parsed captures are excluded, and a dispatch whose newest capture is inside the
// quiet window is skipped until it ages out.
func TestReparseUnparsedDispatches_EligibilityAndQuietWindow(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-1 * time.Hour)

	good := renderCapture("claude-opus-4-8", "sys", "answer", 1, 10, 5)

	// (A) eligible: anthropic + stream + dispatch + old + unparsed.
	eligible := int64(801)
	seedCapture(t, s, dir, eligible, old, good)

	// (B) excluded: dispatch_id NULL (insert directly, no dispatch).
	if _, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		RawLogPath: filepath.Join(dir, "nulldispatch.http"), StartedAt: old, EndedAt: old,
		ContentType: "text/event-stream", IsStream: true, IsAnthropic: true,
	}); err != nil {
		t.Fatalf("seed null-dispatch: %v", err)
	}

	// (C) excluded: non-anthropic host.
	if _, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "example.com", Path: "/v1/messages", Status: 200,
		RawLogPath: filepath.Join(dir, "nonanthropic.http"), StartedAt: old, EndedAt: old,
		ContentType: "text/event-stream", IsStream: true, IsAnthropic: false,
		DispatchID: ptrInt64(802),
	}); err != nil {
		t.Fatalf("seed non-anthropic: %v", err)
	}

	// (D) excluded: non-stream (count_tokens / error shape).
	if _, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		RawLogPath: filepath.Join(dir, "nonstream.http"), StartedAt: old, EndedAt: old,
		ContentType: "application/json", IsStream: false, IsAnthropic: true,
		DispatchID: ptrInt64(803),
	}); err != nil {
		t.Fatalf("seed non-stream: %v", err)
	}

	// (E) excluded: inside the quiet window (newest capture is "now").
	quiet := int64(804)
	seedCapture(t, s, dir, quiet, now, good)

	// (F) excluded: already fully parsed (partial-gap is out of v1 scope) —
	// live-parse it so it has a proxy_messages row.
	parsed := int64(805)
	prParsed := seedCapture(t, s, dir, parsed, old, good)
	liveParse(t, s, prParsed, parsed)

	res, err := s.ReparseUnparsedDispatches(ReparseOpts{QuietWindow: 2 * time.Minute, Now: now})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// Only dispatch (A) is eligible → 1 scanned, 1 reparsed.
	if res.DispatchesScanned != 1 || res.CapturesReparsed != 1 {
		t.Fatalf("sweep result = %+v, want 1 scanned / 1 reparsed (only the eligible dispatch)", res)
	}
	if _, err := s.JobTranscript(eligible); err != nil {
		t.Errorf("eligible dispatch should now have a transcript: %v", err)
	}
	// The quiet-window dispatch stays unparsed.
	if _, err := s.JobTranscript(quiet); err != ErrNotFound {
		t.Errorf("quiet-window dispatch err = %v, want ErrNotFound (skipped this sweep)", err)
	}
}

func ptrInt64(n int64) *int64 { return &n }
