package anthropic

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// TestParseCapture_SSETurn parses the real title-gen SSE capture (sample 01)
// and asserts the reconstructed text, the merged usage (input off message_start,
// output off message_delta), and the stop reason — the core decode rules from
// the reference decoder doc, against a real capture with trailing whitespace in
// the data: JSON.
func TestParseCapture_SSETurn(t *testing.T) {
	pc, err := ParseCapture(readFixture(t, "01-streaming-messages.sse.http"))
	if err != nil {
		t.Fatalf("ParseCapture: %v", err)
	}
	if pc.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", pc.Model)
	}
	if !pc.HasOutputConfig {
		// Fixture 01 is the real title-gen probe: its output_config carries a
		// {"effort":"high","format":{"type":"json_schema",...}}. This assertion now
		// proves the BACI-325 format-keyed detection (the `format` child marks the
		// probe), not the bare presence of output_config.
		t.Errorf("HasOutputConfig = false, want true (the title-gen call carries output_config.format)")
	}
	if pc.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", pc.MessageCount)
	}
	// Reconstructed assistant text — the two text_delta fragments stitched.
	var text string
	for _, b := range pc.Turn.Blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if want := `{"title": "Check if subagent is frozen"}`; text != want {
		t.Errorf("reconstructed text = %q, want %q", text, want)
	}
	// Merged usage: input_tokens off message_start (606), output_tokens off
	// message_delta (18) — the message_start's provisional 7 must NOT win.
	if pc.Turn.Usage.InputTokens != 606 {
		t.Errorf("input_tokens = %d, want 606", pc.Turn.Usage.InputTokens)
	}
	if pc.Turn.Usage.OutputTokens != 18 {
		t.Errorf("output_tokens = %d, want 18 (message_delta supersedes message_start's 7)", pc.Turn.Usage.OutputTokens)
	}
	if pc.Turn.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", pc.Turn.StopReason)
	}
}

// TestParseCapture_OutputConfigEffortOnly is the BACI-325 core regression: a
// normal Opus 4.x turn whose output_config carries only a reasoning-effort hint
// ({"effort":"xhigh"}) and NO structured-output `format`. The old bare-presence
// heuristic flagged it as a probe → auxiliary → zero primary turns. The
// format-keyed detection must classify it as NOT a probe (HasOutputConfig false)
// so it stays primary-eligible, and the assistant turn must still reconstruct.
func TestParseCapture_OutputConfigEffortOnly(t *testing.T) {
	pc, err := ParseCapture(readFixture(t, "output-config-effort-only.sse.http"))
	if err != nil {
		t.Fatalf("ParseCapture: %v", err)
	}
	if pc.HasOutputConfig {
		t.Errorf("HasOutputConfig = true, want false (effort-only output_config is a normal turn, not a probe)")
	}
	if pc.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", pc.Model)
	}
	if pc.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", pc.MessageCount)
	}
	// The assistant turn reconstructs from the SSE stream just like any other turn.
	var text string
	for _, b := range pc.Turn.Blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if want := "It reverses the slice in place."; text != want {
		t.Errorf("reconstructed text = %q, want %q", text, want)
	}
	if pc.Turn.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", pc.Turn.StopReason)
	}
}

// TestParseCapture_ToolUse parses the synthesized turn-1 job fixture and asserts
// the tool_use block reconstructs with its id, name, and accumulated input JSON
// (the input_json_delta fragments stitched), plus the surrounding text block in
// emission order.
func TestParseCapture_ToolUse(t *testing.T) {
	pc, err := ParseCapture(readFixture(t, "job-turn1.sse.http"))
	if err != nil {
		t.Fatalf("ParseCapture: %v", err)
	}
	if len(pc.Turn.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text then tool_use): %+v", len(pc.Turn.Blocks), pc.Turn.Blocks)
	}
	if pc.Turn.Blocks[0].Type != "text" || pc.Turn.Blocks[0].Text != "Let me list the files." {
		t.Errorf("block 0 = %+v, want text 'Let me list the files.'", pc.Turn.Blocks[0])
	}
	tu := pc.Turn.Blocks[1]
	if tu.Type != "tool_use" || tu.ID != "toolu_job1" || tu.Name != "Bash" {
		t.Errorf("block 1 = %+v, want tool_use toolu_job1/Bash", tu)
	}
	if got := strings.TrimSpace(string(tu.Input)); got != `{"command": "ls -la"}` {
		t.Errorf("tool_use input = %q, want {\"command\": \"ls -la\"}", got)
	}
	if pc.Turn.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", pc.Turn.StopReason)
	}
}

// TestParseCapture_ErrorJSON and _CountTokens assert the non-stream JSON shapes
// (error reply, count_tokens reply) are rejected with ErrNotStream rather than
// fed to the SSE decoder — they're a separate shape, surfaced raw, not message
// transcripts.
func TestParseCapture_ErrorJSON(t *testing.T) {
	_, err := ParseCapture(readFixture(t, "02-error-404.json.http"))
	if !errors.Is(err, ErrNotStream) {
		t.Errorf("ParseCapture(error JSON) err = %v, want ErrNotStream", err)
	}
}

func TestParseCapture_CountTokens(t *testing.T) {
	_, err := ParseCapture(readFixture(t, "03-count-tokens.json.http"))
	if !errors.Is(err, ErrNotStream) {
		t.Errorf("ParseCapture(count_tokens) err = %v, want ErrNotStream", err)
	}
}

// TestParseCapture_ResponseMarkerInRequestBody is the BACI-323 regression: the
// request body (the conversation-so-far) embeds the literal "==== RESPONSE ===="
// text inside a JSON string. A bare-substring cut fires on that in-body
// occurrence and truncates the request JSON mid-object, so json.Unmarshal
// returns "unexpected end of JSON input". The line-anchored split must cut on
// the real boundary instead, leaving the request JSON whole and the assistant
// turn reconstructable.
func TestParseCapture_ResponseMarkerInRequestBody(t *testing.T) {
	pc, err := ParseCapture(readFixture(t, "marker-collision.sse.http"))
	if err != nil {
		t.Fatalf("ParseCapture: %v (the in-body marker truncated the request JSON)", err)
	}
	// Request side parses whole — the in-body marker did not truncate it.
	if pc.Model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", pc.Model)
	}
	if pc.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", pc.MessageCount)
	}
	// Response side reconstructs from the SSE stream after the real boundary.
	var text string
	for _, b := range pc.Turn.Blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if want := "Confirmed: the split is line-anchored."; text != want {
		t.Errorf("reconstructed text = %q, want %q", text, want)
	}
	if pc.Turn.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", pc.Turn.StopReason)
	}
}

// TestSystemFingerprint_StripsBillingHeader is the BACI-326 core regression for
// the fingerprint helper: the Claude client prepends a volatile
// x-anthropic-billing-header block whose `cch` cache hash drifts every request,
// so hashing the system bytes verbatim gave every turn a different fingerprint.
// Two captures whose system blocks differ ONLY in the billing header's cch must
// now produce the SAME fingerprint (so Classify threads them), a genuinely
// different system prompt must still produce a DIFFERENT fingerprint (so sibling
// threads stay disambiguated), and a string-form system must still fingerprint
// (the verbatim fallback) — non-empty and stable across identical bytes.
func TestSystemFingerprint_StripsBillingHeader(t *testing.T) {
	turn1, err := ParseCapture(readFixture(t, "system-fp-drift-turn1.sse.http"))
	if err != nil {
		t.Fatalf("parse turn1: %v", err)
	}
	turn2, err := ParseCapture(readFixture(t, "system-fp-drift-turn2.sse.http"))
	if err != nil {
		t.Fatalf("parse turn2: %v", err)
	}
	// turn1 and turn2 share the same conversation; their system blocks differ
	// only in block 0's cch (4bd2d → 8d5cb). The stripped fingerprint is equal.
	if turn1.SystemFP == "" {
		t.Fatalf("turn1 SystemFP is empty, want a stripped-block-list hash")
	}
	if turn1.SystemFP != turn2.SystemFP {
		t.Errorf("cch-only drift produced different fingerprints: turn1=%s turn2=%s (the billing header must be stripped before hashing)", turn1.SystemFP, turn2.SystemFP)
	}

	// A genuinely-different system prompt (same billing-header shape, different
	// stable text) must hash differently — disambiguation is preserved.
	otherText := systemFingerprint([]byte(`[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.159.1f0; cc_entrypoint=cli; cch=4bd2d;"},{"type":"text","text":"You are a DIFFERENT agent."}]`))
	if otherText == turn1.SystemFP {
		t.Errorf("a different system prompt hashed equal to the drift thread: %s — sibling threads must stay distinct", otherText)
	}

	// A string-form system (the shorthand the API also accepts) has no block list
	// to strip, so it hashes the verbatim bytes — non-empty and stable.
	strFP1 := systemFingerprint([]byte(`"You are a plain string system prompt."`))
	strFP2 := systemFingerprint([]byte(`"You are a plain string system prompt."`))
	if strFP1 == "" {
		t.Errorf("string-form system fingerprinted empty, want a verbatim hash")
	}
	if strFP1 != strFP2 {
		t.Errorf("string-form fingerprint not stable: %s vs %s", strFP1, strFP2)
	}
}

// TestParseCapture_StringFormMessageContent is the BACI-324 regression: a request
// message carries `content` as a plain string (the /v1/messages shorthand the
// Claude client emits for plain user turns) rather than a block list. The parser
// must decode it — normalising the string to a single text block — rather than
// failing `cannot unmarshal string into ... content`. Built inline (no fixture)
// so the exact string-content shape is visible in the test.
func TestParseCapture_StringFormMessageContent(t *testing.T) {
	raw := "==== REQUEST ====\r\n" +
		"POST /v1/messages HTTP/1.1\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"model":"claude-opus-4-8","system":"sys","messages":[{"role":"user","content":"plain string turn"},{"role":"assistant","content":[{"type":"text","text":"block turn"}]}]}` +
		"\r\n==== RESPONSE ====\r\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		`data: {"type":"message_start","message":{"model":"claude-opus-4-8","usage":{"input_tokens":5}}}` + "\n\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`

	pc, err := ParseCapture([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCapture: %v (string-form content should decode)", err)
	}
	if pc.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2", pc.MessageCount)
	}
	// The string-form message normalised to one text block carrying its text.
	first := pc.Messages[0]
	if first.Role != "user" || len(first.Content) != 1 ||
		first.Content[0].Type != "text" || first.Content[0].Text != "plain string turn" {
		t.Errorf("message[0] = %+v, want one user text block 'plain string turn'", first)
	}
	// The block-form message still decodes verbatim.
	if second := pc.Messages[1]; len(second.Content) != 1 || second.Content[0].Text != "block turn" {
		t.Errorf("message[1] = %+v, want one text block 'block turn'", second)
	}
}
