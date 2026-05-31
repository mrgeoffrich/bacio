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
		t.Errorf("HasOutputConfig = false, want true (the title-gen call carries output_config)")
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
