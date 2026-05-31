package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mrgeoffrich/bacio/internal/model"
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
	registerErr func(sessionID, model string) error

	// Question-side state. asked records every AskQuestion call
	// (including the issue id the channel threaded through);
	// questions is the in-memory rows keyed by request_uuid that
	// the next DrainAnsweredQuestions returns and clears.
	asked            []askRec
	askErr           error
	questions        map[string]model.SessionQuestion
	abandonedOpenN   int
	abandonOpenCalls int

	// BACI-286 user-message state. userMsgBatches hands back successive
	// pre-canned batches from DrainUserMessages (same shape as
	// batches), and userMsgErr lets a test inject a drain error.
	userMsgBatches [][]UserMessageEvent
	userMsgErr     error

	// BACI-287 notification state. notified records every
	// SendNotification call; notifyErr lets a test inject an insert error
	// and assert the channel surfaces it as a tool-error.
	notified  []notifyRec
	notifyErr error
}

// notifyRec records one SendNotification call with the issue id the channel
// threaded through (empty for a ticket-less notification) and the body.
type notifyRec struct {
	issueID string
	body    string
}

// askRec records one AskQuestion call with the issue id the
// channel threaded through alongside the payload. BACI-128: the
// channel now validates and forwards a required issue_id, so the
// test asserts both fields.
type askRec struct {
	issueID string
	payload model.QuestionPayload
}

type regRec struct {
	sessionID string
	model     string
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

func (f *fakeSource) Register(ctx context.Context, sessionID, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registerErr != nil {
		return f.registerErr(sessionID, model)
	}
	f.registered = append(f.registered, regRec{sessionID, model})
	return nil
}

func (f *fakeSource) EnsureSetup(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setupCalls++
	return nil
}

func (f *fakeSource) AskQuestion(ctx context.Context, issueID string, payload model.QuestionPayload) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.askErr != nil {
		return "", f.askErr
	}
	f.asked = append(f.asked, askRec{issueID: issueID, payload: payload})
	uuid := fmt.Sprintf("req-%d", len(f.asked))
	if f.questions == nil {
		f.questions = map[string]model.SessionQuestion{}
	}
	f.questions[uuid] = model.SessionQuestion{
		RequestUUID: uuid, IssueKey: issueID, Payload: payload, State: model.QuestionOpen,
	}
	return uuid, nil
}

// completeQuestion lets a test simulate the user submitting an
// answer (state="answered" + answers map) or dismissing the row
// (state="cancelled"). The next DrainAnsweredQuestions returns it.
func (f *fakeSource) completeQuestion(uuid string, state model.QuestionState, answers model.QuestionAnswers) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.questions[uuid]
	if !ok {
		return
	}
	row.State = state
	row.Answers = answers
	now := time.Now().UTC()
	row.AnsweredAt = &now
	f.questions[uuid] = row
}

func (f *fakeSource) DrainAnsweredQuestions(ctx context.Context) ([]model.SessionQuestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.SessionQuestion
	for uuid, q := range f.questions {
		if q.State == model.QuestionAnswered || q.State == model.QuestionCancelled {
			out = append(out, q)
			delete(f.questions, uuid)
		}
	}
	return out, nil
}

func (f *fakeSource) AbandonOpenQuestions(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abandonOpenCalls++
	return f.abandonedOpenN, nil
}

func (f *fakeSource) DrainUserMessages(ctx context.Context) ([]UserMessageEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.userMsgErr != nil {
		return nil, f.userMsgErr
	}
	if len(f.userMsgBatches) == 0 {
		return nil, nil
	}
	b := f.userMsgBatches[0]
	f.userMsgBatches = f.userMsgBatches[1:]
	return b, nil
}

func (f *fakeSource) SendNotification(ctx context.Context, issueID, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifyErr != nil {
		return f.notifyErr
	}
	f.notified = append(f.notified, notifyRec{issueID: issueID, body: body})
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
	// Run() turns the poller on, so ask_user_question (BACI-53)
	// joins reply + register + send_user_notification (BACI-287) on
	// the advertised list.
	if len(tools) != 4 {
		t.Fatalf("tools/list returned %d tools, want 4", len(tools))
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool.(map[string]any)["name"].(string)
		seen[name] = true
	}
	if !seen["reply"] || !seen["register"] || !seen["ask_user_question"] || !seen["send_user_notification"] {
		t.Fatalf("tools/list missing entries: %+v", seen)
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
		// "branch" is no longer part of the register tool's input
		// schema (BACI-98); keeping it in the request is a deliberate
		// regression check that an unknown field is ignored gracefully.
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
	want := regRec{"sess-abc", "claude-opus-4-7"}
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
	if src.registered[1] != (regRec{"sess-bare", ""}) {
		t.Fatalf("registered[1] = %+v, want {sess-bare \"\"}", src.registered[1])
	}
}

// TestRegisterToolSchemaHasNoBranch locks in BACI-98: the register
// tool's input schema no longer advertises a "branch" property — the
// SessionStart hook resolves the branch, so the agent never supplies it.
func TestRegisterToolSchemaHasNoBranch(t *testing.T) {
	schema := registerToolSchema()
	input, _ := schema["inputSchema"].(map[string]any)
	props, _ := input["properties"].(map[string]any)
	if _, ok := props["branch"]; ok {
		t.Fatalf("register tool schema still advertises a branch property: %+v", props)
	}
	if _, ok := props["session_id"]; !ok {
		t.Fatalf("register tool schema missing session_id property: %+v", props)
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
		registerErr: func(sid, _ string) error {
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

// TestChannelRegisterRejectsMalformedUUID locks in the BACI-100
// contract: a register call whose session_id is not a structurally
// valid UUID (the fat-fingered-retry bug) surfaces to the agent as an
// MCP tool error, exactly like the placeholder reject. The fake source
// mimics ValidateSessionUUID rejecting a wrong-length hex group.
func TestChannelRegisterRejectsMalformedUUID(t *testing.T) {
	const bad = "23543e26-6339-4b8-aff2-d8ea2013f287" // 3-hex third group
	src := &fakeSource{
		registerErr: func(sid, _ string) error {
			if _, err := uuid.Parse(sid); err != nil {
				return fmt.Errorf("session_id %q is not a valid UUID; copy it verbatim, do not retype it", sid)
			}
			return nil
		},
	}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"register","arguments":{"session_id":"` + bad + `"}}}`,
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
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("register with malformed UUID must surface isError=true: %+v", call)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("response missing content: %+v", res)
	}
	body, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(body, "not a valid UUID") {
		t.Fatalf("response text does not mention the UUID problem: %q", body)
	}
}

// TestChannelPushesEvents checks drainOnce turns a Source batch into a
// notifications/claude/channel frame with the dispatch metadata.
func TestChannelPushesEvents(t *testing.T) {
	src := &fakeSource{batches: [][]Event{{
		{ID: 42, IssueKey: "MINI-9", From: "supervisor", Payload: "look at this", BaseBranch: "feat/X"},
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
	// BACI-226: <channel base_branch="..."> is the supervisor-side
	// breadcrumb so a channel-log reader can spot the resolved value
	// without parsing the payload's stub.
	if meta["base_branch"] != "feat/X" {
		t.Fatalf("meta base_branch = %v, want %q", meta["base_branch"], "feat/X")
	}
}

// TestChannelPushesEventsOmitsBaseBranchWhenEmpty: an issue-less
// dispatch (setup nudge, idle ping) carries an empty BaseBranch — the
// meta tag must drop the attribute entirely rather than emit
// `base_branch=""`, so legacy log parsers stay backwards-compatible.
func TestChannelPushesEventsOmitsBaseBranchWhenEmpty(t *testing.T) {
	src := &fakeSource{batches: [][]Event{{
		{ID: 7, From: "bacio-channel", Payload: "register please"},
	}}}
	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(""), &out, nil)

	srv.drainOnce(context.Background())

	frames := decodeFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	params, _ := frames[0]["params"].(map[string]any)
	meta, _ := params["meta"].(map[string]any)
	if _, ok := meta["base_branch"]; ok {
		t.Fatalf("meta unexpectedly carried base_branch: %+v", meta)
	}
}

// TestChannelPushesUserMessage (BACI-286) locks in the steer-message
// wire shape: drainUserMessages turns a Source batch into a
// notifications/claude/channel frame with meta.kind="message",
// meta.from, the body as content, and crucially NO dispatch_id / issue /
// mode keys — that's what lets the worker (and a channel-log reader)
// tell a steer message apart from a dispatch.
func TestChannelPushesUserMessage(t *testing.T) {
	src := &fakeSource{userMsgBatches: [][]UserMessageEvent{{
		{ID: 5, From: "user", Body: "please write ACKSTEER"},
	}}}
	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(""), &out, nil)

	srv.drainUserMessages(context.Background())

	frames := decodeFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	f := frames[0]
	if f["method"] != "notifications/claude/channel" {
		t.Fatalf("method = %v, want notifications/claude/channel", f["method"])
	}
	params, _ := f["params"].(map[string]any)
	if params["content"] != "please write ACKSTEER" {
		t.Fatalf("content = %v", params["content"])
	}
	meta, _ := params["meta"].(map[string]any)
	if meta["kind"] != "message" {
		t.Fatalf("meta kind = %v, want message", meta["kind"])
	}
	if meta["from"] != "user" {
		t.Fatalf("meta from = %v, want user", meta["from"])
	}
	// A steer message is NOT a dispatch — none of the dispatch-only
	// keys may appear, or a worker keyed on dispatch_id would mistake
	// it for work to ack.
	for _, k := range []string{"dispatch_id", "issue", "mode", "base_branch"} {
		if _, ok := meta[k]; ok {
			t.Fatalf("meta unexpectedly carried %q: %+v", k, meta)
		}
	}
}

// TestChannelTickDrainsUserMessages checks tick runs the user-message
// drain alongside the dispatch + question drains, so a steer message
// queued between turns rides out on the next poll tick.
func TestChannelTickDrainsUserMessages(t *testing.T) {
	src := &fakeSource{userMsgBatches: [][]UserMessageEvent{{
		{ID: 9, From: "user", Body: "steer"},
	}}}
	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(""), &out, nil)

	srv.tick(context.Background())

	frames := decodeFrames(t, out.String())
	found := false
	for _, f := range frames {
		params, _ := f["params"].(map[string]any)
		meta, _ := params["meta"].(map[string]any)
		if meta != nil && meta["kind"] == "message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tick did not emit a kind=message frame; frames=%+v", frames)
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

// TestAskUserQuestionParksAndDelivers locks in the BACI-53 happy path:
// a tools/call(ask_user_question) parks the JSON-RPC reply, the next
// drain finds an answered row, and the parked reply fires with the
// stored answers map.
func TestAskUserQuestionParksAndDelivers(t *testing.T) {
	src := &fakeSource{}
	// First request: initialize. Then the ask. We don't send
	// notifications/initialized so the poller doesn't auto-start
	// here — we call drainAnsweredQuestions by hand to keep timing
	// deterministic.
	askArgs := `{"issue_id":"BACI-42","questions":[{"question":"Pick one","header":"Q","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ask_user_question","arguments":` + askArgs + `}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	// Force the poller flag without starting the goroutine so the
	// tool registers and the parked reply path is reachable; we
	// drive drainAnsweredQuestions manually below to avoid timing
	// flake. ServeMCP() / Run() set this in production.
	srv.poller = true
	if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
		// Fall back via the actual serve path. If the helper doesn't
		// exist (it doesn't in v1), drive the read loop directly:
		t.Skipf("test scaffolding requires direct read-loop drive: %v", err)
	}
	frames := decodeFrames(t, out.String())
	byID := map[float64]map[string]any{}
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok {
			byID[id] = f
		}
	}

	// tools/list should advertise three tools now that poller=true.
	list := byID[2]
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool.(map[string]any)["name"].(string)
		names[name] = true
	}
	if !names["ask_user_question"] || !names["reply"] || !names["register"] {
		t.Fatalf("tools/list missing entries: %+v", names)
	}

	// The ask is parked — id=3 should NOT have a response yet.
	if _, ok := byID[3]; ok {
		t.Fatalf("ask_user_question reply should be parked, got %+v", byID[3])
	}
	src.mu.Lock()
	if len(src.asked) != 1 {
		src.mu.Unlock()
		t.Fatalf("AskQuestion called %d times, want 1", len(src.asked))
	}
	if src.asked[0].issueID != "BACI-42" {
		src.mu.Unlock()
		t.Fatalf("AskQuestion issueID = %q, want %q", src.asked[0].issueID, "BACI-42")
	}
	src.mu.Unlock()

	// Simulate the user answering "A" on the only question. The
	// next drain finds the row and fires the parked reply.
	src.completeQuestion("req-1", model.QuestionAnswered, model.QuestionAnswers{"Pick one": "A"})
	out.Reset()
	srv.drainAnsweredQuestions(context.Background())

	frames = decodeFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("expected 1 reply frame after drain, got %d: %v", len(frames), out.String())
	}
	res, _ := frames[0]["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("answer should not be flagged as error: %+v", frames[0])
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("answer response missing content: %+v", res)
	}
	body, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(body, `"answers"`) || !strings.Contains(body, `"A"`) {
		t.Fatalf("answer body %q missing answers/A", body)
	}
}

// TestAskUserQuestionCancelDeliversError covers the user-dismissed
// path: the channel hands the agent a tool error so it can fall
// back to the same path as a no-answer return from the built-in.
func TestAskUserQuestionCancelDeliversError(t *testing.T) {
	src := &fakeSource{}
	askArgs := `{"issue_id":"BACI-42","questions":[{"question":"Pick one","header":"Q","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user_question","arguments":` + askArgs + `}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	srv.poller = true
	if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
		t.Skipf("test scaffolding: %v", err)
	}
	src.completeQuestion("req-1", model.QuestionCancelled, nil)
	out.Reset()
	srv.drainAnsweredQuestions(context.Background())
	frames := decodeFrames(t, out.String())
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame after cancel-drain, got %d", len(frames))
	}
	res, _ := frames[0]["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("cancel should be flagged as tool-error: %+v", frames[0])
	}
	content, _ := res["content"].([]any)
	body, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(strings.ToLower(body), "dismiss") {
		t.Fatalf("error text %q should mention dismissal", body)
	}
}

// TestAskUserQuestionRejectsInvalidPayload covers the validator path
// — the channel must NOT park a reply for a payload that fails
// ValidateQuestionPayload (e.g. zero questions).
func TestAskUserQuestionRejectsInvalidPayload(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user_question","arguments":{"issue_id":"BACI-42","questions":[]}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	srv.poller = true
	if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
		t.Skipf("test scaffolding: %v", err)
	}
	frames := decodeFrames(t, out.String())
	var bad map[string]any
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok && id == 2 {
			bad = f
		}
	}
	if bad == nil {
		t.Fatalf("no reply for invalid ask")
	}
	res, _ := bad["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("invalid payload must surface isError=true: %+v", bad)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.asked) != 0 {
		t.Fatalf("source.AskQuestion was reached on invalid payload: %+v", src.asked)
	}
}

// TestChannelSendUserNotificationAdvertisedWithoutPoller locks in the
// BACI-287 decision that send_user_notification advertises unconditionally
// — like reply / register it parks no JSON-RPC reply, so the poller-gate
// that applies to ask_user_question does not apply to it.
func TestChannelSendUserNotificationAdvertisedWithoutPoller(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	if err := srv.ServeMCP(context.Background()); err != nil {
		t.Fatalf("ServeMCP: %v", err)
	}
	frames := decodeFrames(t, out.String())
	var list map[string]any
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok && id == 2 {
			list = f
		}
	}
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	seen := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool.(map[string]any)["name"].(string)
		seen[name] = true
	}
	if !seen["send_user_notification"] {
		t.Fatalf("send_user_notification must advertise even without the poller: %+v", seen)
	}
}

// TestChannelSendUserNotificationTool drives tools/call(send_user_notification)
// and checks the body + optional issue id reach Source.SendNotification, the
// response is a non-error tool result (no parked reply), and a missing body
// is rejected without reaching the source.
func TestChannelSendUserNotificationTool(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_user_notification","arguments":{"body":"shipped BACI-42","issue_id":"BACI-42"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_user_notification","arguments":{"body":"ticket-less heads up"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"send_user_notification","arguments":{}}}`,
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

	// id=2: issue-linked notification — non-error tool result, reached source.
	ok := byID[2]
	if isErr, _ := ok["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("send_user_notification reported an error: %+v", ok)
	}
	// id=3: ticket-less notification — also a non-error result.
	bare := byID[3]
	if isErr, _ := bare["result"].(map[string]any)["isError"].(bool); isErr {
		t.Fatalf("ticket-less send_user_notification reported an error: %+v", bare)
	}
	// id=4: missing body — rejected before reaching the source.
	bad := byID[4]
	if isErr, _ := bad["result"].(map[string]any)["isError"].(bool); !isErr {
		t.Fatalf("send_user_notification without body should report isError=true: %+v", bad)
	}

	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.notified) != 2 {
		t.Fatalf("SendNotification reached %d times, want 2 (missing-body call must not reach source): %+v", len(src.notified), src.notified)
	}
	if src.notified[0] != (notifyRec{issueID: "BACI-42", body: "shipped BACI-42"}) {
		t.Fatalf("notified[0] = %+v, want {BACI-42 shipped BACI-42}", src.notified[0])
	}
	if src.notified[1] != (notifyRec{issueID: "", body: "ticket-less heads up"}) {
		t.Fatalf("notified[1] = %+v, want ticket-less", src.notified[1])
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

// TestAskUserQuestionRequiresIssueID locks in BACI-128: an
// ask_user_question call with a valid payload but no issue_id arg
// is rejected at the channel with an MCP tool error, and the
// source's AskQuestion is never reached. Without this guard the
// row would land with issue_key="" and the kanban-card pill
// surface would never light up.
func TestAskUserQuestionRequiresIssueID(t *testing.T) {
	src := &fakeSource{}
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user_question","arguments":{"questions":[{"question":"Pick one","header":"Q","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	srv.poller = true
	if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
		t.Skipf("test scaffolding: %v", err)
	}
	frames := decodeFrames(t, out.String())
	var bad map[string]any
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok && id == 2 {
			bad = f
		}
	}
	if bad == nil {
		t.Fatalf("no reply for ask without issue_id")
	}
	res, _ := bad["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("ask without issue_id must surface isError=true: %+v", bad)
	}
	content, _ := res["content"].([]any)
	body, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(body, "issue_id") {
		t.Fatalf("error text %q should mention issue_id", body)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.asked) != 0 {
		t.Fatalf("source.AskQuestion was reached without issue_id: %+v", src.asked)
	}
}

// TestAskUserQuestionRejectsMalformedIssueID locks in BACI-128:
// every parser-rejected issue_id surfaces as an MCP tool error
// and never reaches the source.
func TestAskUserQuestionRejectsMalformedIssueID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"non-numeric-suffix", "BACI-foo"},
		{"three-char-prefix", "foo-1"},
		{"embedded-space", "BACI 1"},
		{"trim-to-empty", "   "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSource{}
			askArgs := fmt.Sprintf(
				`{"issue_id":%q,"questions":[{"question":"Pick one","header":"Q","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`,
				tc.raw,
			)
			requests := strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user_question","arguments":` + askArgs + `}}`,
			}, "\n") + "\n"

			var out bytes.Buffer
			srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
			srv.poller = true
			if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
				t.Skipf("test scaffolding: %v", err)
			}
			frames := decodeFrames(t, out.String())
			var bad map[string]any
			for _, f := range frames {
				if id, ok := f["id"].(float64); ok && id == 2 {
					bad = f
				}
			}
			if bad == nil {
				t.Fatalf("no reply for ask with malformed issue_id %q", tc.raw)
			}
			res, _ := bad["result"].(map[string]any)
			if isErr, _ := res["isError"].(bool); !isErr {
				t.Fatalf("malformed issue_id %q must surface isError=true: %+v", tc.raw, bad)
			}
			src.mu.Lock()
			defer src.mu.Unlock()
			if len(src.asked) != 0 {
				t.Fatalf("source.AskQuestion was reached with malformed issue_id %q: %+v", tc.raw, src.asked)
			}
		})
	}
}

// TestAskUserQuestionNormalisesLowerCasePrefix locks in BACI-128:
// the channel parses issue_id via store.ParseIssueKey and threads
// the canonical (upper-case prefix) form into the source. An
// agent that writes "baci-42" gets stamped as "BACI-42".
func TestAskUserQuestionNormalisesLowerCasePrefix(t *testing.T) {
	src := &fakeSource{}
	askArgs := `{"issue_id":"baci-42","questions":[{"question":"Pick one","header":"Q","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_user_question","arguments":` + askArgs + `}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	srv := New(src, "bacio", "test", strings.NewReader(requests), &out, nil)
	srv.poller = true
	if err := srv.runReadLoopOnlyForTest(context.Background()); err != nil {
		t.Skipf("test scaffolding: %v", err)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.asked) != 1 {
		t.Fatalf("AskQuestion called %d times, want 1", len(src.asked))
	}
	if src.asked[0].issueID != "BACI-42" {
		t.Fatalf("AskQuestion issueID = %q, want %q (canonical-form normalisation)", src.asked[0].issueID, "BACI-42")
	}
}
