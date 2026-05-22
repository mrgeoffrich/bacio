package transcript

import (
	"strings"
	"testing"
)

// A compact synthetic transcript exercising every block type plus the
// two content shapes (string and []block) and an attachment entry.
const sampleTranscript = `{"type":"user","timestamp":"2026-05-22T01:25:32Z","agentId":"abc123","sessionId":"sess-1","gitBranch":"main","cwd":"/tmp/wt","version":"2.1.148","message":{"role":"user","content":"Plan ticket BACI-1."}}
{"type":"assistant","timestamp":"2026-05-22T01:25:40Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"I should read the ticket first."},{"type":"text","text":"On it."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["X"]}}
{"type":"user","timestamp":"2026-05-22T01:25:41Z","message":{"role":"user","content":[{"type":"tool_result","content":"file-a\nfile-b","is_error":false}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"boom"}],"is_error":true}]}}
`

func TestToMarkdown(t *testing.T) {
	md, err := ToMarkdown(strings.NewReader(sampleTranscript), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}

	wants := []string{
		"# Agent transcript",
		"- **Agent:** `abc123`",
		"- **Branch:** `main`",
		"## 👤 User  ·  _2026-05-22T01:25:32Z_",
		"Plan ticket BACI-1.",
		"> 💭 **Thinking**",
		"> I should read the ticket first.",
		"**🔧 Tool call: `Bash`**",
		"\"command\": \"ls\"",
		"**↳ Tool result**",
		"file-a",
		"**↳ Tool result (error)**",
		"boom",
	}
	for _, w := range wants {
		if !strings.Contains(md, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, md)
		}
	}

	// attachment entries are dropped entirely.
	if strings.Contains(md, "deferred_tools_delta") {
		t.Errorf("attachment entry leaked into output:\n%s", md)
	}
}

func TestToMarkdownEmpty(t *testing.T) {
	md, err := ToMarkdown(strings.NewReader(""), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown empty: %v", err)
	}
	if md != "" {
		t.Errorf("empty input should yield empty output, got %q", md)
	}
}

func TestToMarkdownMalformedLine(t *testing.T) {
	_, err := ToMarkdown(strings.NewReader("{not json}\n"), Options{})
	if err == nil {
		t.Fatal("expected an error on a malformed line")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error should name the line number, got: %v", err)
	}
}

func TestTruncation(t *testing.T) {
	long := strings.Repeat("x", 100)
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + long + `"}]}}` + "\n"

	md, err := ToMarkdown(strings.NewReader(line), Options{Truncate: 10})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	// A plain text block is not clipped (only fenced tool blocks are),
	// so the truncation marker should be absent here…
	if strings.Contains(md, "truncated") {
		t.Errorf("plain text should not be truncated:\n%s", md)
	}

	// …but a long tool result IS fenced and therefore clipped.
	bigResult := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + long + `"}]}}` + "\n"
	md, err = ToMarkdown(strings.NewReader(bigResult), Options{Truncate: 10})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(md, "more chars truncated") {
		t.Errorf("long tool result should be truncated:\n%s", md)
	}
}

func TestFenceEscapesBackticks(t *testing.T) {
	// A tool result containing a triple-backtick run must be wrapped in
	// a longer fence so the Markdown stays well-formed.
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"see ` + "```go" + ` block"}]}}` + "\n"
	md, err := ToMarkdown(strings.NewReader(line), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(md, "````") {
		t.Errorf("fence should lengthen past embedded backticks:\n%s", md)
	}
}
