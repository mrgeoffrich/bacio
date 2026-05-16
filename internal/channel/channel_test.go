package channel

import (
	"bytes"
	"context"
	"encoding/json"
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
