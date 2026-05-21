package channel

import (
	"strings"
	"testing"
)

// TestRenderTranscriptDigestBasic renders a small transcript fixture
// and checks the header fields and per-event sections all appear.
func TestRenderTranscriptDigestBasic(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"Plan the feature please."}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/x.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"package main"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"hmm"},{"type":"text","text":"Done."}]}}`,
	}, "\n"))

	meta := TranscriptMeta{
		AgentID:         "abc123",
		AgentType:       "bacio-plan-worker",
		Description:     "Plan BACI-85",
		ParentSessionID: "sess-parent",
		SourcePath:      "/home/u/.claude/projects/-x/sess-parent/subagents/agent-abc123.jsonl",
		SizeBytes:       int64(len(raw)),
		Note:            "looks good",
		GeneratedAt:     "2026-05-21T00:00:00Z",
	}
	digest := RenderTranscriptDigest(raw, meta)

	for _, want := range []string{
		"# Subagent transcript — Plan BACI-85",
		"agent-abc123",
		"bacio-plan-worker",
		"**Parent session:** sess-parent",
		"looks good",
		"## User",
		"Plan the feature please.",
		"## Assistant",
		"On it.",
		"**Tool call:** `Read`",
		"**Tool result:**",
		"package main",
		"_[thinking]_",
		"Done.",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest missing %q\n---\n%s", want, digest)
		}
	}
}

// TestRenderTranscriptDigestSkipsBadLines confirms malformed JSONL
// lines are skipped and counted, not fatal.
func TestRenderTranscriptDigestSkipsBadLines(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"ok"}}`,
		`this is not json`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]}}`,
	}, "\n"))
	digest := RenderTranscriptDigest(raw, TranscriptMeta{AgentID: "z"})
	if !strings.Contains(digest, "1 unparseable lines skipped") {
		t.Errorf("digest should note skipped lines:\n%s", digest)
	}
	if !strings.Contains(digest, "reply") {
		t.Errorf("digest should still render the valid events:\n%s", digest)
	}
}

// TestRenderTranscriptDigestCaps confirms a huge transcript is capped
// under DigestCap with a truncation footer.
func TestRenderTranscriptDigestCaps(t *testing.T) {
	big := strings.Repeat("x", 4096)
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+big+`"}]}}`)
	}
	raw := []byte(strings.Join(lines, "\n"))
	digest := RenderTranscriptDigest(raw, TranscriptMeta{
		AgentID:    "big",
		SourcePath: "/path/to/raw.jsonl",
	})
	if len(digest) > DigestCap {
		t.Errorf("digest %d bytes exceeds cap %d", len(digest), DigestCap)
	}
	if !strings.Contains(digest, "transcript truncated") {
		t.Errorf("oversized digest must carry a truncation footer:\n%s", digest[len(digest)-300:])
	}
	if !strings.Contains(digest, "/path/to/raw.jsonl") {
		t.Errorf("truncation footer must point at the raw transcript path")
	}
}

// TestRenderTranscriptDigestToolResultBlocks covers a tool_result
// whose content is an array of text blocks (not a bare string).
func TestRenderTranscriptDigestToolResultBlocks(t *testing.T) {
	raw := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"command failed"}]}]}}`)
	digest := RenderTranscriptDigest(raw, TranscriptMeta{AgentID: "z"})
	if !strings.Contains(digest, "**Tool result (error):**") {
		t.Errorf("error tool_result should be labelled:\n%s", digest)
	}
	if !strings.Contains(digest, "command failed") {
		t.Errorf("tool_result block text should be flattened:\n%s", digest)
	}
}
