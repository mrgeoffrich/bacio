package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type ackRec struct {
	id   int64
	note string
}

// fakeSource is a deterministic Source: Drain hands back successive
// pre-canned batches, Ack/Register just record the call, Heartbeat /
// EnsureSetup count.
type fakeSource struct {
	mu         sync.Mutex
	batches    [][]Event
	acked      []ackRec
	registered []regRec
	heartbeats int
	setupCalls int
	// registerErr, when non-nil, lets a test inject a deterministic
	// error from Register (e.g. the placeholder-reject path) and
	// assert the channel surfaces it as a tool-error rather than
	// quietly recording the call.
	registerErr func(sessionID, model, branch string) error
}

type regRec struct {
	sessionID string
	model     string
	branch    string
}

func (f *fakeSource) Drain(ctx context.Context) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		return nil, nil
	}
	b := f.batches[0]
	f.batches = f.batches[1:]
	return b, nil
}

func (f *fakeSource) Ack(ctx context.Context, id int64, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, ackRec{id, note})
	return nil
}

func (f *fakeSource) Heartbeat(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return nil
}

func (f *fakeSource) Register(ctx context.Context, sessionID, model, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registerErr != nil {
		return f.registerErr(sessionID, model, branch)
	}
	f.registered = append(f.registered, regRec{sessionID, model, branch})
	return nil
}

func (f *fakeSource) EnsureSetup(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setupCalls++
	return nil
}

// decodeFrames splits the newline-delimited JSON-RPC output into
// generic maps for assertion.
func decodeFrames(t *testing.T, out string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad output frame %q: %v", line, err)
		}
		frames = append(frames, m)
	}
	return frames
}

// TestChannelHandshakeAndReply drives a full initialize -> tools/list ->
// tools/call(reply) exchange and checks the channel capability is
// declared, the reply tool is advertised, and the call reaches Ack.
func TestChannelHandshakeAndReply(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"reply","arguments":{"dispatch_id":7,"note":"done"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames := decodeFrames(t, out.String())
	byID := map[float64]map[string]any{}
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok {
			byID[id] = f
		}
	}

	init := byID[1]
	if init == nil {
		t.Fatal("no initialize response")
	}
	caps, _ := init["result"].(map[string]any)["capabilities"].(map[string]any)
	exp, _ := caps["experimental"].(map[string]any)
	if _, ok := exp["claude/channel"]; !ok {
		t.Fatalf("initialize result missing claude/channel capability: %+v", init)
	}

	list := byID[2]
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools/list returned %d tools, want 2", len(tools))
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool.(map[string]any)["name"].(string)
		seen[name] = true
	}
	if !seen["reply"] || !seen["register"] {
		t.Fatalf("tools/list missing reply/register: %+v", seen)
	}

	call := byID[3]
	if isErr, _ := call["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("reply tool-call reported an error: %+v", call)
	}
	if len(src.acked) != 1 || src.acked[0] != (ackRec{7, "done"}) {
		t.Fatalf("acked = %+v, want [{7 done}]", src.acked)
	}
}

// TestChannelRegisterTool drives a tools/call(register) and checks the
// args reach Source.Register, the response is not flagged as an error,
// and a missing session_id is rejected with isError=true (without
// reaching Source).
func TestChannelRegisterTool(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"register","arguments":{"session_id":"sess-abc","model":"claude-opus-4-7","branch":"main"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"register","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"register","arguments":{"session_id":"sess-bare"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames := decodeFrames(t, out.String())
	byID := map[float64]map[string]any{}
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok {
			byID[id] = f
		}
	}

	ok := byID[2]
	if isErr, _ := ok["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("register tool-call reported an error: %+v", ok)
	}
	want := regRec{"sess-abc", "claude-opus-4-7", "main"}
	if len(src.registered) != 2 || src.registered[0] != want {
		t.Fatalf("registered[0] = %+v, want %+v", src.registered, want)
	}

	bad := byID[3]
	if isErr, _ := bad["result"].(map[string]any)["isError"].(bool); !isErr {
		t.Fatalf("register without session_id should report isError=true: %+v", bad)
	}
	if len(src.registered) != 2 {
		t.Fatalf("Source.Register reached on invalid args: %+v", src.registered)
	}

	bare := byID[4]
	if isErr, _ := bare["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("register with only session_id should still succeed: %+v", bare)
	}
	if src.registered[1] != (regRec{"sess-bare", "", ""}) {
		t.Fatalf("registered[1] = %+v, want {sess-bare \"\" \"\"}", src.registered[1])
	}
}

// TestChannelRegisterRejectsPlaceholder locks in the BACI-46 contract:
// the channel hands the validator's "placeholder" error straight back to
// the agent as a tool error. The fake source mimics what the real local
// client does (ValidateSessionID rejects "$CLAUDE_CODE_SESSION_ID" up
// front), so we get end-to-end coverage of the wire path without
// importing the store layer.
func TestChannelRegisterRejectsPlaceholder(t *testing.T) {
	src := &fakeSource{
		registerErr: func(sid, _, _ string) error {
			if strings.HasPrefix(sid, "$") {
				return fmt.Errorf("session_id %q looks like an unsubstituted placeholder; pass the real value", sid)
			}
			return nil
		},
	}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"register","arguments":{"session_id":"$CLAUDE_CODE_SESSION_ID"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames := decodeFrames(t, out.String())
	var call map[string]any
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok && id == 2 {
			call = f
		}
	}
	if call == nil {
		t.Fatal("no register tool-call response")
	}
	res, _ := call["result"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	if !isErr {
		t.Fatalf("register with placeholder must surface isError=true: %+v", call)
	}
	// The validator's actionable error text reaches the agent verbatim
	// — that's the user-facing fix.
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("response missing content: %+v", res)
	}
	body, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(body, "placeholder") {
		t.Fatalf("response text does not mention placeholder: %q", body)
	}
}

// TestInstructionsBlockContainsContract guards the BACI-52 worker
// contract that ships in the MCP `instructions` block. The block is
// injected into every channel-equipped session's system prompt, so
// dropping a load-bearing phrase silently re-introduces the
// "dispatches grow the parent context monotonically" regression.
// Keep the phrase list aligned with the contract in
// docs/agent-dispatch.md ("Subagent delegation") and SKILL.md
// ("Handling dispatches"); if either drifts, the next reader will
// hit a stale spec.
func TestInstructionsBlockContainsContract(t *testing.T) {
	srv := New(&fakeSource{}, "bacio", "test", strings.NewReader(""), &bytes.Buffer{}, nil)
	got := srv.instructionsBlock()

	mustContain := []string{
		// Base contract — must survive the rewrite.
		"reply",
		"dispatch_id",
		"<channel source=\"bacio\"",
		// Delegation contract — every phrase here is load-bearing.
		"Subagent delegation",
		"subagent_type = \"general-purpose\"",
		"model         = \"opus\"",
		"bacio agent claim",
		"bacio agent release",
		"mcp__bacio__reply",
		"plan / design / implement / review / ship / fix_review",
		"needs_input",
		// Trivial-dispatch carve-out.
		"from=\"bacio-channel\"",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("instructions block missing load-bearing phrase %q\nfull block:\n%s", phrase, got)
		}
	}
}

// TestInitializeResultUsesInstructionsBlock keeps the wire-level
// initialize response and the in-code instructionsBlock in lockstep —
// so a future refactor can't accidentally route around the contract
// (e.g. by inlining a different string back into initializeResult).
func TestInitializeResultUsesInstructionsBlock(t *testing.T) {
	srv := New(&fakeSource{}, "bacio", "test", strings.NewReader(""), &bytes.Buffer{}, nil)
	result := srv.initializeResult(nil)
	got, _ := result["instructions"].(string)
	if got == "" {
		t.Fatal("initialize result missing instructions string")
	}
	if got != srv.instructionsBlock() {
		t.Fatalf("initialize instructions diverged from instructionsBlock()\nwire:\n%s\nblock:\n%s", got, srv.instructionsBlock())
	}
}

// TestChannelPushesEvents checks drainOnce turns a Source batch into a
// notifications/claude/channel frame with the dispatch metadata.
func TestChannelPushesEvents(t *testing.T) {
	src := &fakeSource{batches: [][]Event{{
		{ID: 42, IssueKey: "MINI-9", From: "supervisor", Payload: "look at this"},
	}}}
	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(""), &out, nil)

	srv.drainOnce(context.Background())

	frames := decodeFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	f := frames[0]
	if f["method"] != "notifications/claude/channel" {
		t.Fatalf("method = %v, want notifications/claude/channel", f["method"])
	}
	params, _ := f["params"].(map[string]any)
	if params["content"] != "look at this" {
		t.Fatalf("content = %v", params["content"])
	}
	meta, _ := params["meta"].(map[string]any)
	if meta["dispatch_id"] != "42" || meta["issue"] != "MINI-9" || meta["from"] != "supervisor" {
		t.Fatalf("meta = %+v", meta)
	}
}

// TestChannelTickCallsEnsureSetup checks that tick invokes EnsureSetup
// alongside Heartbeat — the way the channel funnels its "tell the
// agent to call register" prompt through the dispatch queue rather
// than a synthetic notification.
func TestChannelTickCallsEnsureSetup(t *testing.T) {
	src := &fakeSource{}
	srv := New(src, "bacio", "test", strings.NewReader(""), &bytes.Buffer{}, nil)

	srv.tick(context.Background())
	srv.tick(context.Background())

	src.mu.Lock()
	defer src.mu.Unlock()
	if src.setupCalls != 2 {
		t.Fatalf("EnsureSetup calls = %d, want 2", src.setupCalls)
	}
	if src.heartbeats != 2 {
		t.Fatalf("heartbeat calls = %d, want 2", src.heartbeats)
	}
}

// TestServeMCPLeavesPollerParked locks in the BACI-48 contract on the
// channel side: ServeMCP completes the MCP handshake and answers
// tools/list + tools/call(register), but does NOT start the setup-
// dispatch poller — so the source's EnsureSetup / Drain / Heartbeat
// hooks are never invoked. The agent can still opt in mid-session by
// calling register directly, which goes through the read loop.
func TestServeMCPLeavesPollerParked(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"register","arguments":{"session_id":"sess-opt-in"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	if err := srv.ServeMCP(context.Background()); err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}

	src.mu.Lock()
	defer src.mu.Unlock()
	if src.setupCalls != 0 {
		t.Fatalf("EnsureSetup called %d times — poller must stay parked", src.setupCalls)
	}
	if src.heartbeats != 0 {
		t.Fatalf("Heartbeat called %d times — poller must stay parked", src.heartbeats)
	}
	// Read-loop side-effects: tools/list + register both reached.
	if len(src.registered) != 1 || src.registered[0].sessionID != "sess-opt-in" {
		t.Fatalf("Register reach = %+v, want [{sess-opt-in}]", src.registered)
	}
	frames := decodeFrames(t, out.String())
	if len(frames) < 3 {
		t.Fatalf("expected at least 3 frames (initialize, tools/list, register), got %d: %v", len(frames), frames)
	}
}
