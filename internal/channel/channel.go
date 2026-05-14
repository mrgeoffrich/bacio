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
	// Drain returns newly-pending events and marks them delivered.
	Drain(ctx context.Context) ([]Event, error)
	// Ack records the agent's acknowledgement of an event (dispatch).
	Ack(ctx context.Context, eventID int64, note string) error
	// Heartbeat is called once per poll tick (and once immediately at
	// startup) regardless of whether there's anything to drain. The
	// bacio source uses it to record this channel's liveness so the
	// hooks can correlate it back to a session. Errors are logged, not
	// fatal — a channel that can't heartbeat still delivers.
	Heartbeat(ctx context.Context) error
}

// Server speaks the channel protocol over a reader/writer pair (stdin
// and stdout in production). It pushes drained events as
// notifications/claude/channel and answers the reply tool by calling
// Source.Ack.
type Server struct {
	src  Source
	name string

	in   io.Reader
	out  io.Writer
	logf func(format string, args ...any)

	pollInterval time.Duration

	mu sync.Mutex // serialises writes to out across the poller + read loop
}

// New builds a channel server. name is the source attribute Claude Code
// stamps on every <channel> tag. in/out are the stdio transport; logf
// receives diagnostics (stderr in production) — pass nil to discard.
func New(src Source, name string, in io.Reader, out io.Writer, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Server{
		src:          src,
		name:         name,
		in:           in,
		out:          out,
		logf:         logf,
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
	case "notifications/initialized", "notifications/cancelled":
		s.logf("bacio channel: %s received — handshake complete", msg.Method)
		// no-op acknowledgement notifications
	case "ping":
		s.reply(msg.ID, map[string]any{})
	case "tools/list":
		s.reply(msg.ID, map[string]any{"tools": []any{replyToolSchema()}})
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
		"serverInfo": map[string]any{"name": s.name, "version": "1"},
		"instructions": "Events from the bacio channel arrive as " +
			"<channel source=\"" + s.name + "\" dispatch_id=\"...\" issue=\"...\" from=\"...\">. " +
			"Each is a work item a supervisor dispatched to you: read the instruction, do the work, " +
			"then call the `reply` tool with the dispatch_id from the tag and a short note to acknowledge it.",
	}
}

// ---------- reply tool ----------

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

func (s *Server) handleToolCall(ctx context.Context, msg *rpcMessage) {
	var call struct {
		Name      string `json:"name"`
		Arguments struct {
			DispatchID int64  `json:"dispatch_id"`
			Note       string `json:"note"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &call); err != nil {
		s.replyError(msg.ID, -32602, "invalid tool-call params: "+err.Error())
		return
	}
	if call.Name != "reply" {
		s.replyError(msg.ID, -32602, "unknown tool: "+call.Name)
		return
	}
	if call.Arguments.DispatchID == 0 {
		s.toolResult(msg.ID, true, "reply requires a dispatch_id (the value from the <channel> tag)")
		return
	}
	if err := s.src.Ack(ctx, call.Arguments.DispatchID, call.Arguments.Note); err != nil {
		s.logf("bacio channel: ack dispatch %d: %v", call.Arguments.DispatchID, err)
		s.toolResult(msg.ID, true, fmt.Sprintf("could not ack dispatch %d: %v", call.Arguments.DispatchID, err))
		return
	}
	s.toolResult(msg.ID, false, fmt.Sprintf("acked dispatch %d", call.Arguments.DispatchID))
}

// ---------- poller ----------

func (s *Server) poll(ctx context.Context) {
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()
	s.tick(ctx) // heartbeat + push anything already queued, no initial wait
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick is one poll cycle: heartbeat (record liveness) then drain (push
// queued work). Heartbeat runs every tick regardless of whether there's
// anything to drain — it's how the channel stays correlatable.
func (s *Server) tick(ctx context.Context) {
	if err := s.src.Heartbeat(ctx); err != nil {
		s.logf("bacio channel: heartbeat: %v", err)
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
