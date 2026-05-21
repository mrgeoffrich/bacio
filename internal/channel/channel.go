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

	"github.com/mrgeoffrich/bacio/internal/model"
)

// Event is one unit of work pushed into the session. It maps onto a
// bacio dispatch; the channel doesn't know about the store.
type Event struct {
	ID       int64  // dispatch id — echoed back by the reply tool
	IssueKey string // "" when the dispatch isn't tied to an issue
	From     string // who created the dispatch
	Mode     string // template slug ("plan", "design", "implement", etc.) or "" — the dispatch intent
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
	// agent's self-description (model). The SessionStart hook only
	// creates a minimal stub; this is the path that flips
	// registered_at and makes the session visible to the default
	// agent-list view. model is optional — empty leaves the column
	// unchanged. The session's git branch is resolved by the
	// SessionStart hook, so the agent doesn't supply it here. The
	// bacio binary version stamped onto the session row is taken from
	// the channel itself (the agent doesn't know it, and shouldn't be
	// trusted to report it), so it isn't part of this signature.
	Register(ctx context.Context, sessionID, model string) error
	// EnsureSetup is called on every poll tick. It is the source's hook
	// to idempotently queue a "call register" dispatch into the agent's
	// inbox so the next Drain picks it up — routing setup through the
	// proven dispatch path rather than a synthetic notification, which
	// Claude Code's MCP client appears to drop. The source no-ops once
	// Register has fired (or any other terminal signal it tracks). Best
	// effort: errors are logged, not fatal.
	EnsureSetup(ctx context.Context) error

	// AskQuestion records a clarification question the agent posed
	// via the bacio MCP `ask_user_question` tool. The source resolves
	// session_id / agent identity / open-claim issue key on its side
	// and returns the row's request_uuid — the channel uses it as
	// the in-memory key to correlate the parked JSON-RPC reply with
	// the answered row on the next poll tick. An error here means
	// the row couldn't be inserted; the channel surfaces it as an
	// MCP tool error so the agent can fall back to the built-in
	// AskUserQuestion (or report the failure).
	AskQuestion(ctx context.Context, payload model.QuestionPayload) (string, error)

	// DrainAnsweredQuestions returns the answered + cancelled rows
	// for the channel's session. The channel iterates and matches
	// each row's request_uuid against its in-memory pending map; a
	// hit fires the parked MCP tool result (answer or error). Per-
	// process dedupe means returning the same row twice across two
	// ticks is fine — the channel just no-ops the second time. Best
	// effort: errors are logged.
	DrainAnsweredQuestions(ctx context.Context) ([]model.SessionQuestion, error)

	// AbandonOpenQuestions is called once at channel startup. Any
	// rows the previous channel process parked on are flipped to
	// `abandoned` — the parked JSON-RPC reply is gone with the
	// previous process, and the agent restarted with the channel,
	// so there's no path to recover. Returns the number of rows
	// abandoned (for logging) and any error.
	AbandonOpenQuestions(ctx context.Context) (int, error)

	// AttachTranscript locates the transcript of a completed
	// subagent (identified by agentID — the `agentId` from a Task
	// result) and attaches a rendered digest of it to issueKey as a
	// linked document. note is an optional supervisor-supplied
	// string recorded in the digest header. It returns a short
	// human-readable confirmation the channel surfaces as the tool
	// result, or an error the channel surfaces as an MCP tool error
	// (issue/agent not found, harness too old to persist subagent
	// transcripts, etc.).
	AttachTranscript(ctx context.Context, issueKey, agentID, note string) (string, error)
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

	// pending parks ask_user_question MCP replies until the user
	// answers (or dismisses) the row. Keyed by request_uuid (the
	// id the source returns from AskQuestion). drainAnsweredQuestions
	// pulls the matching JSON-RPC id back out, fires the result,
	// and deletes the entry. Touched from both the read loop
	// (insert on a fresh ask) and the poller (drain), so it carries
	// its own mutex.
	pendingMu sync.Mutex
	pending   map[string]json.RawMessage

	// poller drives whether the drain step is reachable. Set true
	// by serve when withPoller is on; the read loop checks it before
	// accepting an ask_user_question call so a no-poller channel
	// (ServeMCP path) refuses the tool with a clear message rather
	// than parking a reply that will never be delivered.
	poller bool
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
		pending:      map[string]json.RawMessage{},
	}
}

// Run drives the server with the setup-dispatch poller running until
// stdin closes or ctx is cancelled. The poller goroutine drains the
// Source on a ticker; the read loop handles inbound JSON-RPC. Both share
// the write mutex.
//
// Run does not return until the poller goroutine has fully stopped — so
// a caller that closes Source-backing resources (a DB handle, say) in a
// defer after Run can't race a poll still in flight.
func (s *Server) Run(ctx context.Context) error {
	return s.serve(ctx, true)
}

// ServeMCP runs the JSON-RPC read loop without starting the
// setup-dispatch poller. The MCP handshake and the tool handlers
// (register, reply) stay reachable, so an agent that explicitly wants
// to opt in to bacio agent supervision mid-session can still call
// `register` directly — but the channel will not push queued
// dispatches and the source's EnsureSetup / Drain / Heartbeat hooks
// are never invoked. Use this when the host process has decided the
// channel should be inert (e.g. the BACIO_AGENT_MODE env var gate in
// the CLI).
func (s *Server) ServeMCP(ctx context.Context) error {
	return s.serve(ctx, false)
}

// runReadLoopOnlyForTest is a test-only entry point that runs the
// read loop with the poller flag forced on (so ask_user_question is
// advertised and accepted) but without starting the poller
// goroutine. Tests drive drainAnsweredQuestions by hand for
// deterministic timing.
func (s *Server) runReadLoopOnlyForTest(ctx context.Context) error {
	s.poller = true
	return s.readLoop(ctx)
}

// serve is the shared poller-optional core. When withPoller is false
// the poller goroutine isn't started and pollDone is closed up front,
// so the read loop's exit still returns immediately.
func (s *Server) serve(ctx context.Context, withPoller bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.poller = withPoller

	// Startup sweep: any open questions our session left dangling
	// have lost their parked reply (the previous channel process
	// owned the in-memory map; the agent restarted with the
	// channel). The source's AbandonOpenQuestions flips them to
	// `abandoned` so the audit log shows the ask happened and was
	// never answered. Best effort — a failure here just means the
	// next tick will surface a stale row in DrainAnsweredQuestions
	// (the channel won't have it in pending, so the row is ignored).
	if withPoller {
		if n, err := s.src.AbandonOpenQuestions(ctx); err != nil {
			s.logf("bacio channel: abandon open questions: %v", err)
		} else if n > 0 {
			s.logf("bacio channel: abandoned %d open questions from previous session", n)
		}
	}

	pollDone := make(chan struct{})
	if withPoller {
		go func() {
			defer close(pollDone)
			s.poll(ctx)
		}()
	} else {
		close(pollDone)
	}

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
		// reply / register / attach_transcript advertise
		// unconditionally — none of them park a JSON-RPC reply, so
		// the poller-gate reasoning that applies to ask_user_question
		// (a parked reply would never be delivered without the drain
		// step) does not apply to them. ask_user_question only
		// advertises when the poller is on. See ServeMCP / Run split
		// + the BACIO_AGENT_MODE gate in internal/cli/channel.go.
		tools := []any{replyToolSchema(), registerToolSchema(), attachTranscriptToolSchema()}
		if s.poller {
			tools = append(tools, askUserQuestionToolSchema())
		}
		s.reply(msg.ID, map[string]any{"tools": tools})
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
		"description": "Complete the registration of your Claude Code session with bacio. The SessionStart hook only creates a minimal stub; this call enriches it and makes the session visible to the agent list. Call once on your first turn with the session_id the bacio channel pre-filled for you in the setup-dispatch payload (copy the JSON verbatim — do not paste the literal $CLAUDE_CODE_SESSION_ID placeholder; the validator will refuse it). Pass model if you know it. Safe to call again — idempotent (first-registration timestamp wins).",
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
			},
			"required": []string{"session_id"},
		},
	}
}

// attachTranscriptToolSchema describes the bacio MCP `attach_transcript`
// tool. It takes an issue key and a completed subagent's agentId,
// locates that subagent's transcript on disk, and links the raw
// .jsonl to the issue as a document.
func attachTranscriptToolSchema() map[string]any {
	return map[string]any{
		"name": "attach_transcript",
		"description": "Attach a completed subagent's transcript to a bacio issue for traceability. " +
			"After a dispatched Task subagent finishes, call this with the issue key and the agentId " +
			"from the Task result — bacio locates that subagent's transcript and links the raw .jsonl " +
			"transcript verbatim to the issue as a document (visible from `bacio issue show` " +
			"and the bacio UIs). The transcript is capped at ~2.5 MB; an over-cap transcript is " +
			"truncated with a footer. Re-calling for the same (issue, " +
			"agent) pair refreshes the attachment. Errors clearly if the issue or transcript cannot be found.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"issue_key": map[string]any{
					"type":        "string",
					"description": "The canonical issue key (e.g. \"BACI-42\") the transcript belongs to.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "The subagent's agentId from the Task tool result (e.g. \"a8d9f1ea8797ea776\"). A leading \"agent-\" is accepted and stripped.",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional free-text note recorded in the digest header — e.g. the subagent's one-line summary.",
				},
			},
			"required": []string{"issue_key", "agent_id"},
		},
	}
}

// askUserQuestionToolSchema describes the bacio MCP `ask_user_question`
// tool. Input shape mirrors the built-in AskUserQuestion so an agent
// can build the same payload either way — the description is the
// soft-nudge pointing supervised agents at the bacio variant.
func askUserQuestionToolSchema() map[string]any {
	return map[string]any{
		"name": "ask_user_question",
		"description": "Ask the user one or more multi-choice clarification questions through the bacio UI. " +
			"The call blocks until the user answers (or dismisses). Prefer this over the built-in " +
			"AskUserQuestion when running under bacio — the user sees the question in their TUI / " +
			"desktop / web window where the active issue context is right there, and the exchange " +
			"is recorded in bacio's audit log. " +
			"Input shape mirrors AskUserQuestion: a `questions` array with 1-4 items, each carrying " +
			"`question` text, a short `header` tag (<=12 chars), an explicit `multiSelect` boolean, " +
			"and 2-4 `options` (each {label, description, optional preview}). " +
			"`preview` on an option carries an ASCII mockup / code snippet / diagram the supervisor needs to see " +
			"to compare options — when at least one option in a question has a preview, the modal switches to " +
			"side-by-side mode showing the focused option's preview verbatim in monospace. Single-select only " +
			"(`multiSelect: true` + previews is rejected). " +
			"The tool returns once the user submits: {questions: [...], answers: {question: <label|labels>}}. " +
			"If the user dismisses the question the tool errors with \"user dismissed the question\" — " +
			"handle that the same as the built-in returning with no answer.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "Questions to ask the user (1-4 questions).",
					"minItems":    1,
					"maxItems":    4,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{
								"type":        "string",
								"description": "The complete question to ask the user. Should be clear, specific, and end with a question mark. Example: \"Which library should we use for date formatting?\" If multiSelect is true, phrase it accordingly, e.g. \"Which features do you want to enable?\"",
							},
							"header": map[string]any{
								"type":        "string",
								"description": "Very short label displayed as a chip/tag (max 12 chars). Examples: \"Auth method\", \"Library\", \"Approach\".",
								"maxLength":   12,
							},
							"multiSelect": map[string]any{
								"type":        "boolean",
								"default":     false,
								"description": "Set to true to allow the user to select multiple options instead of just one. Use when choices are not mutually exclusive.",
							},
							"options": map[string]any{
								"type":        "array",
								"description": "The available choices for this question. Must have 2-4 options. Each option should be a distinct, mutually exclusive choice (unless multiSelect is enabled). There should be no 'Other' option, that will be provided automatically.",
								"minItems":    2,
								"maxItems":    4,
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label": map[string]any{
											"type":        "string",
											"description": "The display text for this option that the user will see and select. Should be concise (1-5 words) and clearly describe the choice.",
										},
										"description": map[string]any{
											"type":        "string",
											"description": "Explanation of what this option means or what will happen if chosen. Useful for providing context about trade-offs or implications.",
										},
										"preview": map[string]any{
											"type":        "string",
											"description": "Optional preview content rendered when this option is focused. Use for mockups, code snippets, or visual comparisons that help users compare options. Multi-line allowed; rendered in monospace. Single-select only (rejected on multiSelect questions).",
											"maxLength":   4096,
										},
									},
									"required": []string{"label", "description"},
								},
							},
						},
						"required": []string{"question", "header", "options", "multiSelect"},
					},
				},
			},
			"required": []string{"questions"},
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
	case "ask_user_question":
		s.handleAskUserQuestionCall(ctx, msg.ID, head.Arguments)
	case "attach_transcript":
		s.handleAttachTranscriptCall(ctx, msg.ID, head.Arguments)
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
	if err := s.src.Register(ctx, sid, modelID); err != nil {
		s.logf("bacio channel: register session %s: %v", sid, err)
		s.toolResult(id, true, fmt.Sprintf("could not register session %s: %v", sid, err))
		return
	}
	var extras []string
	if modelID != "" {
		extras = append(extras, "model="+modelID)
	}
	msg := fmt.Sprintf("registered session %s with the bacio channel", sid)
	if len(extras) > 0 {
		msg += " (" + strings.Join(extras, ", ") + ")"
	}
	s.toolResult(id, false, msg)
}

// handleAttachTranscriptCall validates the issue key + agent id, asks
// the source to locate the subagent transcript and link a digest to
// the issue, and returns the confirmation as a tool result. Any
// failure (missing args, issue/transcript not found) surfaces as an
// MCP tool error rather than dropping the JSON-RPC connection.
func (s *Server) handleAttachTranscriptCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	var args struct {
		IssueKey string `json:"issue_key"`
		AgentID  string `json:"agent_id"`
		Note     string `json:"note"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.replyError(id, -32602, "invalid attach_transcript arguments: "+err.Error())
			return
		}
	}
	issueKey := strings.TrimSpace(args.IssueKey)
	agentID := strings.TrimSpace(args.AgentID)
	// Accept both "agent-<id>" and bare "<id>" forms.
	agentID = strings.TrimPrefix(agentID, "agent-")
	if issueKey == "" {
		s.toolResult(id, true, "attach_transcript requires an issue_key (e.g. \"BACI-42\")")
		return
	}
	if agentID == "" {
		s.toolResult(id, true, "attach_transcript requires an agent_id (the agentId from the Task result)")
		return
	}
	confirmation, err := s.src.AttachTranscript(ctx, issueKey, agentID, strings.TrimSpace(args.Note))
	if err != nil {
		s.logf("bacio channel: attach_transcript %s agent-%s: %v", issueKey, agentID, err)
		s.toolResult(id, true, fmt.Sprintf("could not attach transcript for agent-%s to %s: %v", agentID, issueKey, err))
		return
	}
	s.toolResult(id, false, confirmation)
}

// handleAskUserQuestionCall validates the payload, asks the source
// to record an open question, and parks the JSON-RPC reply against
// the source's returned request_uuid. The reply is delivered later
// by drainAnsweredQuestions when the user answers (or dismisses).
//
// On a no-poller channel (ServeMCP path — env var unset) the tool
// isn't advertised in tools/list, but a paranoid agent might still
// try it; we refuse with a clear tool-error so the agent can fall
// back to the built-in AskUserQuestion rather than waiting forever.
func (s *Server) handleAskUserQuestionCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	if !s.poller {
		s.toolResult(id, true, "ask_user_question requires the bacio channel to be running its poller (set BACIO_AGENT_MODE=1 in the launching shell)")
		return
	}
	var args model.QuestionPayload
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.toolResult(id, true, "invalid ask_user_question arguments: "+err.Error())
			return
		}
	}
	if err := model.ValidateQuestionPayload(args); err != nil {
		s.toolResult(id, true, "ask_user_question payload rejected: "+err.Error())
		return
	}
	requestUUID, err := s.src.AskQuestion(ctx, args)
	if err != nil {
		s.logf("bacio channel: ask_user_question: %v", err)
		s.toolResult(id, true, "could not record question: "+err.Error())
		return
	}
	// Park the reply. We copy the id (RawMessage is a slice over the
	// read-loop's scanner buffer; the next Scan would overwrite it).
	idCopy := make(json.RawMessage, len(id))
	copy(idCopy, id)
	s.pendingMu.Lock()
	s.pending[requestUUID] = idCopy
	s.pendingMu.Unlock()
	s.logf("bacio channel: ask_user_question parked %s (%d questions)", requestUUID, len(args.Questions))
	// No s.reply / toolResult here — the reply is parked until the
	// drain step fires it.
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
// (idempotently queue the call-register dispatch), drain (push
// queued work), then drain-answered-questions (deliver any settled
// ask_user_question replies). EnsureSetup runs every tick — the
// source is responsible for being a no-op once register has fired.
// Heartbeat runs every tick regardless of whether there's anything
// to drain — it's how the channel stays correlatable.
func (s *Server) tick(ctx context.Context) {
	if err := s.src.Heartbeat(ctx); err != nil {
		s.logf("bacio channel: heartbeat: %v", err)
	}
	if err := s.src.EnsureSetup(ctx); err != nil {
		s.logf("bacio channel: ensure-setup: %v", err)
	}
	s.drainOnce(ctx)
	s.drainAnsweredQuestions(ctx)
}

// drainAnsweredQuestions pulls every answered or cancelled
// ask_user_question row for this session and fires the matching
// parked MCP reply. Rows we don't have a parked reply for are
// ignored — they belong to a previous channel process whose pending
// map is gone (the startup AbandonOpenQuestions sweep flips
// previous-session rows to `abandoned` so they don't appear here,
// but this guard covers the edge where two channel processes raced
// on the same session).
func (s *Server) drainAnsweredQuestions(ctx context.Context) {
	rows, err := s.src.DrainAnsweredQuestions(ctx)
	if err != nil {
		s.logf("bacio channel: drain answered questions: %v", err)
		return
	}
	for _, q := range rows {
		s.pendingMu.Lock()
		id, ok := s.pending[q.RequestUUID]
		if ok {
			delete(s.pending, q.RequestUUID)
		}
		s.pendingMu.Unlock()
		if !ok {
			continue
		}
		switch q.State {
		case model.QuestionAnswered:
			// AskUserQuestion's documented return shape is the
			// questions array echoed back alongside an answers
			// map. We deliver both so the agent's parsing code
			// has everything it had on the way in.
			body, err := json.Marshal(map[string]any{
				"questions": q.Payload.Questions,
				"answers":   q.Answers,
			})
			if err != nil {
				s.logf("bacio channel: marshal answer for %s: %v", q.RequestUUID, err)
				s.toolResult(id, true, "internal error: could not encode answer JSON")
				continue
			}
			s.toolResult(id, false, string(body))
		case model.QuestionCancelled:
			s.toolResult(id, true, "user dismissed the question without answering")
		default:
			// abandoned / open shouldn't be returned by
			// DrainSettledQuestionsForSession, but if a source
			// implementation slips, surface it as a tool error
			// rather than silently sit on it.
			s.logf("bacio channel: unexpected drained state %q for %s", q.State, q.RequestUUID)
			s.toolResult(id, true, fmt.Sprintf("unexpected question state %q; the channel could not deliver an answer", q.State))
		}
	}
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
