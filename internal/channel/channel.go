// Package channel implements the Claude Code "channel" contract: an MCP
// server, spoken over stdio, that pushes events into a running Claude
// Code session and exposes a reply tool so the agent can answer back.
//
// It is a small, dependency-free JSON-RPC 2.0 implementation — MCP's
// stdio transport is newline-delimited JSON-RPC, and bacio only needs
// the handful of methods a channel uses (initialize, tools/list,
// tools/call) plus the channel-specific notification. The official
// channel examples are Bun/TypeScript; nothing about the protocol
// requires that runtime.
//
// Research-preview caveats live with the `bacio channel` command, not
// here: this package is just the wire protocol.
package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Event is one unit of work pushed into the session. It maps onto a
// bacio dispatch; the channel doesn't know about the store.
type Event struct {
	ID       int64  // dispatch id — echoed back by the reply tool
	IssueKey string // "" when the dispatch isn't tied to an issue
	From     string // who created the dispatch
	Mode     string // "plan", "implement", or "" — the dispatch intent
	Payload  string // the instruction body
}

// Source is the bacio-side backing the channel drains and acks against.
// internal/cli wires this to the agent dispatch queue for one session.
type Source interface {
	// Drain returns events to push: un-acked dispatches the caller
	// hasn't already emitted this process. It marks pending dispatches
	// delivered as a side effect.
	Drain(ctx context.Context) ([]Event, error)
	// Ack records the agent's acknowledgement of an event (dispatch).
	Ack(ctx context.Context, eventID int64, note string) error
	// Heartbeat is called once per poll tick (and once immediately at
	// startup) regardless of whether there's anything to drain. The
	// bacio source uses it to record this channel's liveness so the
	// hooks can correlate it back to a session. Errors are logged, not
	// fatal — a channel that can't heartbeat still delivers.
	Heartbeat(ctx context.Context) error
	// Register completes the agent's session: links the channel to the
	// agent-supplied session id and enriches the session row with the
	// agent's self-description (model, branch). The SessionStart hook
	// only creates a minimal stub; this is the path that flips
	// registered_at and makes the session visible to the default
	// agent-list view. model/branch are optional — empty leaves the
	// column unchanged. The bacio binary version stamped onto the
	// session row is taken from the channel itself (the agent doesn't
	// know it, and shouldn't be trusted to report it), so it isn't
	// part of this signature.
	Register(ctx context.Context, sessionID, model, branch string) error
	// EnsureSetup is called on every poll tick. It is the source's hook
	// to idempotently queue a "call register" dispatch into the agent's
	// inbox so the next Drain picks it up — routing setup through the
	// proven dispatch path rather than a synthetic notification, which
	// Claude Code's MCP client appears to drop. The source no-ops once
	// Register has fired (or any other terminal signal it tracks). Best
	// effort: errors are logged, not fatal.
	EnsureSetup(ctx context.Context) error
}

// Server speaks the channel protocol over a reader/writer pair (stdin
// and stdout in production). It pushes drained events as
// notifications/claude/channel and answers the reply tool by calling
// Source.Ack.
type Server struct {
	src     Source
	name    string
	version string // serverInfo.version; also stamped onto agent_sessions.channel_version when register fires (server-side, not echoed via the tool)

	in   io.Reader
	out  io.Writer
	logf func(format string, args ...any)

	pollInterval time.Duration

	mu sync.Mutex // serialises writes to out across the poller + read loop

	// initialized is closed when the client sends notifications/initialized,
	// signalling its claude/channel notification handler is wired up. The
	// poller waits on this before its first tick — pushing a notification
	// before Claude Code has registered the handler results in a silently
	// dropped event, and the in-memory `pushed` dedup in the source then
	// prevents re-push on subsequent ticks. Earlier "channel queues setup
	// dispatch on its first tick before the handshake completes" bug.
	initialized chan struct{}
	initOnce    sync.Once
}

// New builds a channel server. name is the source attribute Claude Code
// stamps on every <channel> tag. version is reported in the initialize
// response's serverInfo.version AND is stamped server-side onto the
// session row when the agent calls the register tool — so bacio can
// spot stale channel processes still running after the binary was
// upgraded, without trusting the agent to report it. in/out are the
// stdio transport; logf receives diagnostics (stderr in production) —
// pass nil to discard.
func New(src Source, name, version string, in io.Reader, out io.Writer, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if version == "" {
		version = "dev"
	}
	return &Server{
		src:          src,
		name:         name,
		version:      version,
		in:           in,
		out:          out,
		logf:         logf,
		initialized:  make(chan struct{}),
		pollInterval: 3 * time.Second,
	}
}

// Run drives the server until stdin closes or ctx is cancelled. The
// poller goroutine drains the Source on a ticker; the read loop handles
// inbound JSON-RPC. Both share the write mutex.
//
// Run does not return until the poller goroutine has fully stopped — so
// a caller that closes Source-backing resources (a DB handle, say) in a
// defer after Run can't race a poll still in flight.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		s.poll(ctx)
	}()

	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop(ctx) }()

	var err error
	select {
	case <-ctx.Done():
	case err = <-readErr:
	}
	cancel()   // signal the poller to stop
	<-pollDone // and wait for it before returning
	return err
}

// ---------- JSON-RPC wire types ----------

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ---------- read loop ----------

func (s *Server) readLoop(ctx context.Context) error {
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // dispatch payloads are capped well under 1 MiB
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			s.logf("bacio channel: bad JSON-RPC frame: %v", err)
			continue
		}
		s.handle(ctx, &msg)
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, msg *rpcMessage) {
	isRequest := len(msg.ID) > 0
	switch msg.Method {
	case "initialize":
		s.logf("bacio channel: initialize received (params=%s) — MCP client connected", string(msg.Params))
		s.reply(msg.ID, s.initializeResult(msg.Params))
	case "notifications/initialized":
		s.logf("bacio channel: notifications/initialized received — releasing poller")
		s.initOnce.Do(func() { close(s.initialized) })
	case "notifications/cancelled":
		s.logf("bacio channel: notifications/cancelled received")
	case "ping":
		s.reply(msg.ID, map[string]any{})
	case "tools/list":
		s.reply(msg.ID, map[string]any{"tools": []any{replyToolSchema(), registerToolSchema()}})
	case "tools/call":
		s.handleToolCall(ctx, msg)
	default:
		if isRequest {
			s.replyError(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
}

// ---------- initialize ----------

func (s *Server) initializeResult(rawParams json.RawMessage) map[string]any {
	protocol := "2025-06-18"
	if len(rawParams) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(rawParams, &p) == nil && p.ProtocolVersion != "" {
			protocol = p.ProtocolVersion // echo the client's version
		}
	}
	return map[string]any{
		"protocolVersion": protocol,
		"capabilities": map[string]any{
			// Presence of claude/channel registers the notification
			// listener; tools:{} lets Claude discover the reply tool.
			"experimental": map[string]any{"claude/channel": map[string]any{}},
			"tools":        map[string]any{},
		},
		"serverInfo": map[string]any{"name": s.name, "version": s.version},
		"instructions": "Events from the bacio channel arrive as " +
			"<channel source=\"" + s.name + "\" dispatch_id=\"...\" issue=\"...\" from=\"...\">. " +
			"Each is a work item dispatched to you (sometimes by a human supervisor, sometimes by the " +
			"channel itself — e.g. a `from=\"bacio-channel\"` event asking you to call the `register` tool): " +
			"read the instruction, do the work, then call the `reply` tool with the dispatch_id from the " +
			"tag and a short note to acknowledge it.",
	}
}

// ---------- tools ----------

func replyToolSchema() map[string]any {
	return map[string]any{
		"name":        "reply",
		"description": "Acknowledge a bacio dispatch and record a short reply note against it.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dispatch_id": map[string]any{
					"type":        "integer",
					"description": "The dispatch_id from the <channel> tag being acknowledged.",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "A short status note recorded against the dispatch.",
				},
			},
			"required": []string{"dispatch_id"},
		},
	}
}

func registerToolSchema() map[string]any {
	return map[string]any{
		"name":        "register",
		"description": "Complete the registration of your Claude Code session with bacio. The SessionStart hook only creates a minimal stub; this call enriches it and makes the session visible to the agent list. Call once on your first turn with the session_id the bacio channel pre-filled for you in the setup-dispatch payload (copy the JSON verbatim — do not paste the literal $CLAUDE_CODE_SESSION_ID placeholder; the validator will refuse it). Pass model if you know it; pass branch if you know it. Safe to call again — idempotent (first-registration timestamp wins).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Your Claude Code session id. The bacio channel pre-fills the real value into the setup-dispatch JSON — copy that value verbatim, do not paste the literal $CLAUDE_CODE_SESSION_ID placeholder.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Your model identifier, e.g. \"claude-opus-4-7\" or \"claude-sonnet-4-6\". Optional.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Your current git branch, if you know it. Optional.",
				},
			},
			"required": []string{"session_id"},
		},
	}
}

func (s *Server) handleToolCall(ctx context.Context, msg *rpcMessage) {
	var head struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &head); err != nil {
		s.replyError(msg.ID, -32602, "invalid tool-call params: "+err.Error())
		return
	}
	switch head.Name {
	case "reply":
		s.handleReplyCall(ctx, msg.ID, head.Arguments)
	case "register":
		s.handleRegisterCall(ctx, msg.ID, head.Arguments)
	default:
		s.replyError(msg.ID, -32602, "unknown tool: "+head.Name)
	}
}

func (s *Server) handleReplyCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	var args struct {
		DispatchID int64  `json:"dispatch_id"`
		Note       string `json:"note"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.replyError(id, -32602, "invalid reply arguments: "+err.Error())
			return
		}
	}
	if args.DispatchID == 0 {
		s.toolResult(id, true, "reply requires a dispatch_id (the value from the <channel> tag)")
		return
	}
	if err := s.src.Ack(ctx, args.DispatchID, args.Note); err != nil {
		s.logf("bacio channel: ack dispatch %d: %v", args.DispatchID, err)
		s.toolResult(id, true, fmt.Sprintf("could not ack dispatch %d: %v", args.DispatchID, err))
		return
	}
	s.toolResult(id, false, fmt.Sprintf("acked dispatch %d", args.DispatchID))
}

func (s *Server) handleRegisterCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	var args struct {
		SessionID string `json:"session_id"`
		Model     string `json:"model"`
		Branch    string `json:"branch"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.replyError(id, -32602, "invalid register arguments: "+err.Error())
			return
		}
	}
	sid := strings.TrimSpace(args.SessionID)
	if sid == "" {
		s.toolResult(id, true, "register requires a session_id (the value the bacio channel pre-filled for you in the setup-dispatch payload)")
		return
	}
	modelID := strings.TrimSpace(args.Model)
	branch := strings.TrimSpace(args.Branch)
	if err := s.src.Register(ctx, sid, modelID, branch); err != nil {
		s.logf("bacio channel: register session %s: %v", sid, err)
		s.toolResult(id, true, fmt.Sprintf("could not register session %s: %v", sid, err))
		return
	}
	var extras []string
	if modelID != "" {
		extras = append(extras, "model="+modelID)
	}
	if branch != "" {
		extras = append(extras, "branch="+branch)
	}
	msg := fmt.Sprintf("registered session %s with the bacio channel", sid)
	if len(extras) > 0 {
		msg += " (" + strings.Join(extras, ", ") + ")"
	}
	s.toolResult(id, false, msg)
}

// ---------- poller ----------

// firstPushDelay is the settle wait between receiving
// notifications/initialized and the first push. Empirically, Claude
// Code's MCP client registers its claude/channel notification handler
// *after* sending notifications/initialized — observed via the gap
// between handshake completion and Claude Code's "Channel notifications
// registered" debug log. Pushing in that window silently drops the
// notification, and our per-process pushed-dedup then prevents re-push.
// 500ms is conservative but cheap; it only applies once per channel
// process lifetime.
const firstPushDelay = 500 * time.Millisecond

func (s *Server) poll(ctx context.Context) {
	// Wait for the MCP handshake to complete before the first tick.
	// 30s fallback so an unhappy client that never sends
	// notifications/initialized still gets ticks (heartbeat / drain run
	// idle rather than wedging the channel forever).
	select {
	case <-s.initialized:
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
		s.logf("bacio channel: notifications/initialized not received within 30s — starting poller anyway")
	}
	// Brief settle delay so Claude Code's channel-notification handler
	// is wired up before our first push (see firstPushDelay comment).
	select {
	case <-ctx.Done():
		return
	case <-time.After(firstPushDelay):
	}
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()
	s.tick(ctx) // heartbeat + push anything already queued
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick is one poll cycle: heartbeat (record liveness), ensure-setup
// (idempotently queue the call-register dispatch), then drain (push
// queued work). EnsureSetup runs every tick — the source is responsible
// for being a no-op once register has fired. Heartbeat runs every tick
// regardless of whether there's anything to drain — it's how the
// channel stays correlatable.
func (s *Server) tick(ctx context.Context) {
	if err := s.src.Heartbeat(ctx); err != nil {
		s.logf("bacio channel: heartbeat: %v", err)
	}
	if err := s.src.EnsureSetup(ctx); err != nil {
		s.logf("bacio channel: ensure-setup: %v", err)
	}
	s.drainOnce(ctx)
}

func (s *Server) drainOnce(ctx context.Context) {
	events, err := s.src.Drain(ctx)
	if err != nil {
		s.logf("bacio channel: drain: %v", err)
		return
	}
	for _, e := range events {
		s.pushEvent(e)
	}
}

// pushEvent emits one dispatch as a notifications/claude/channel event.
// meta keys must be bare identifiers — Claude Code silently drops keys
// with hyphens or other characters.
func (s *Server) pushEvent(e Event) {
	meta := map[string]string{
		"dispatch_id": fmt.Sprintf("%d", e.ID),
		"from":        e.From,
	}
	if e.IssueKey != "" {
		meta["issue"] = e.IssueKey
	}
	if e.Mode != "" {
		meta["mode"] = e.Mode
	}
	s.write(rpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/claude/channel",
		Params: map[string]any{
			"content": e.Payload,
			"meta":    meta,
		},
	})
}

// ---------- write helpers ----------

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // notification — nothing to reply to
	}
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyError(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		return
	}
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

// toolResult writes an MCP tools/call result. isError marks the call as
// failed (Claude sees the text as the tool error) without dropping the
// JSON-RPC connection.
func (s *Server) toolResult(id json.RawMessage, isError bool, text string) {
	s.reply(id, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isError,
	})
}

// write marshals v and writes it as one newline-delimited JSON-RPC
// frame. The mutex serialises the poller and the read loop, which both
// write to out.
func (s *Server) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		s.logf("bacio channel: marshal frame: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		s.logf("bacio channel: write frame: %v", err)
	}
}
