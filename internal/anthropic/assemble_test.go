package anthropic

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAssemble_PrimaryThread walks the synthesized two-turn job (a tool_use →
// tool_result chain) the way the recorder does: parse each capture, classify it
// against the carried thread state, then assemble the primary rows into one
// ordered transcript. It asserts the ordering (user → assistant+tool_use →
// tool_result → assistant), that the delta is the appended tail (not the full
// growing body), and that token usage sums across both turns.
func TestAssemble_PrimaryThread(t *testing.T) {
	turn1, err := ParseCapture(readFixture(t, "job-turn1.sse.http"))
	if err != nil {
		t.Fatalf("parse turn1: %v", err)
	}
	turn2, err := ParseCapture(readFixture(t, "job-turn2.sse.http"))
	if err != nil {
		t.Fatalf("parse turn2: %v", err)
	}

	var state ThreadState
	var rows []AssembledRow

	prim1, delta1, state := Classify(turn1, state)
	if !prim1 {
		t.Fatalf("turn1 should be primary (establishes the thread)")
	}
	if len(delta1) != 1 || delta1[0].Role != "user" {
		t.Fatalf("turn1 delta = %+v, want the 1 opening user message", delta1)
	}
	rows = append(rows, AssembledRow{IsPrimary: prim1, Delta: delta1, Turn: turn1.Turn})

	prim2, delta2, state := Classify(turn2, state)
	if !prim2 {
		t.Fatalf("turn2 should be primary (extends the thread, count 1→3)")
	}
	// The delta is the genuinely-new user input only: the echoed assistant turn
	// (a re-send of turn1's response) is dropped because it's already
	// represented by turn1's reconstructed Turn. So just the tool_result user.
	if len(delta2) != 1 || delta2[0].Role != "user" {
		t.Fatalf("turn2 delta = %+v, want 1 user (tool_result); the echoed assistant must be dropped", delta2)
	}
	if !hasToolResult(delta2[0], "toolu_job1") {
		t.Errorf("turn2 delta user message missing tool_result for toolu_job1: %+v", delta2[0])
	}
	rows = append(rows, AssembledRow{IsPrimary: prim2, Delta: delta2, Turn: turn2.Turn})

	dispatchID := int64(1631)
	tr := AssembleTranscript(&dispatchID, rows)

	// Ordered thread: user prompt, assistant+tool_use, tool_result user,
	// final assistant.
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	if len(tr.Messages) != len(wantRoles) {
		t.Fatalf("assembled %d messages, want %d: %+v", len(tr.Messages), len(wantRoles), tr.Messages)
	}
	for i, want := range wantRoles {
		if tr.Messages[i].Role != want {
			t.Errorf("message %d role = %q, want %q", i, tr.Messages[i].Role, want)
		}
	}
	// The first assistant message carries the tool_use the second turn replied to.
	if !hasToolUse(tr.Messages[1], "toolu_job1") {
		t.Errorf("assembled assistant message missing tool_use toolu_job1: %+v", tr.Messages[1])
	}
	// Usage sums both primary turns: input 100+160=260, output 42+15=57.
	if tr.Usage.InputTokens != 260 {
		t.Errorf("summed input_tokens = %d, want 260", tr.Usage.InputTokens)
	}
	if tr.Usage.OutputTokens != 57 {
		t.Errorf("summed output_tokens = %d, want 57", tr.Usage.OutputTokens)
	}
	if tr.Model != "claude-opus-4-8" {
		t.Errorf("transcript model = %q, want claude-opus-4-8", tr.Model)
	}
	if tr.DispatchID == nil || *tr.DispatchID != 1631 {
		t.Errorf("dispatch_id = %v, want 1631", tr.DispatchID)
	}
}

// TestClassify_AuxiliaryProbe asserts a structured-output probe (the real
// title-gen capture, which carries output_config) is classified auxiliary even
// after a primary thread is established, and that AssembleTranscript keeps it
// out of the primary Messages while retaining it under Auxiliary.
func TestClassify_AuxiliaryProbe(t *testing.T) {
	turn1, err := ParseCapture(readFixture(t, "job-turn1.sse.http"))
	if err != nil {
		t.Fatalf("parse turn1: %v", err)
	}
	probe, err := ParseCapture(readFixture(t, "01-streaming-messages.sse.http"))
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}

	var state ThreadState
	prim1, delta1, state := Classify(turn1, state)
	rows := []AssembledRow{{IsPrimary: prim1, Delta: delta1, Turn: turn1.Turn}}

	primProbe, deltaProbe, state := Classify(probe, state)
	if primProbe {
		t.Fatalf("the output_config probe should classify auxiliary, not primary")
	}
	if deltaProbe != nil {
		t.Errorf("auxiliary probe delta = %+v, want nil", deltaProbe)
	}
	// State must be unchanged by the auxiliary capture — the primary thread's
	// message count is preserved.
	if state.MessageCount != turn1.MessageCount {
		t.Errorf("state.MessageCount = %d after aux, want unchanged %d", state.MessageCount, turn1.MessageCount)
	}
	rows = append(rows, AssembledRow{IsPrimary: primProbe, Delta: deltaProbe, Turn: probe.Turn})

	tr := AssembleTranscript(nil, rows)
	// Primary thread = just turn1: opening user + the assistant turn.
	if len(tr.Messages) != 2 {
		t.Errorf("primary messages = %d, want 2 (probe excluded): %+v", len(tr.Messages), tr.Messages)
	}
	if len(tr.Auxiliary) != 1 {
		t.Errorf("auxiliary turns = %d, want 1 (the probe retained)", len(tr.Auxiliary))
	}
}

func hasToolUse(m model.AnthropicMessage, id string) bool {
	for _, b := range m.Content {
		if b.Type == "tool_use" && b.ID == id {
			return true
		}
	}
	return false
}

func hasToolResult(m model.AnthropicMessage, toolUseID string) bool {
	for _, b := range m.Content {
		if b.Type == "tool_result" && b.ToolUseID == toolUseID {
			return true
		}
	}
	return false
}
