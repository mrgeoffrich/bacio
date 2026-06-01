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
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Event is one unit of work pushed into the session. It maps onto a
// bacio dispatch; the channel doesn't know about the store.
type Event struct {
	ID       int64  // dispatch id — echoed back by the reply tool
	IssueKey string // "" when the dispatch isn't tied to an issue
	From     string // who created the dispatch
	Mode     string // template slug ("plan", "design", "implement", etc.) or "" — the dispatch intent
	Payload  string // the instruction body
	// BaseBranch (BACI-226) is the resolved base branch the worker's
	// PR will target — surfaced in the <channel base_branch="..."> meta
	// tag attribute so a reader of the channel log can spot the value
	// without parsing the payload. The same value is already embedded
	// in the payload's <base_branch> stub tag (so the worker's Task
	// prompt carries it without a second lookup); the meta attribute
	// is the supervisor-side breadcrumb. Empty for issue-less
	// dispatches (setup nudges, idle pings).
	BaseBranch string
}

// UserMessageEvent is one BACI-286 user→agent steer message to push at
// a running worker. Unlike an Event (a dispatch) it carries no
// dispatch_id, issue, or mode — it is fire-and-forget context the
// channel injects at the worker's next turn boundary. ID is the
// user_messages row id, used only for the channel's per-process dedup;
// it is NOT echoed back (there is no reply/ack).
type UserMessageEvent struct {
	ID   int64  // user_messages id — per-process dedup key only
	Body string // the free-form steer note
	From string // who sent it (the actor) — surfaced in the meta tag
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
	// via the bacio MCP `ask_user_question` tool. issueID is the
	// canonical issue key the channel parsed from the tool args
	// (BACI-128 — the MCP arg is now required and validated against
	// store.ParseIssueKey before this call); sources may stamp it onto
	// the row verbatim. The source resolves session_id / agent
	// identity on its side and returns the row's request_uuid — the
	// channel uses it as the in-memory key to correlate the parked
	// JSON-RPC reply with the answered row on the next poll tick.
	// An error here means the row couldn't be inserted; the channel
	// surfaces it as an MCP tool error so the agent can fall back to
	// the built-in AskUserQuestion (or report the failure).
	AskQuestion(ctx context.Context, issueID string, payload model.QuestionPayload) (string, error)

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

	// DrainUserMessages returns the un-consumed BACI-286 steer messages
	// for the channel's session(s), oldest-first, marking them consumed
	// as a side effect. The channel pushes each as a `kind="message"`
	// notification. Per-process dedup against the message id covers the
	// edge where a drain returns a row twice; a fresh channel process
	// re-pushes nothing (consumed is terminal). Best effort: errors are
	// logged, not fatal.
	DrainUserMessages(ctx context.Context) ([]UserMessageEvent, error)

	// SendNotification records a BACI-287 agent→user notification — a
	// non-blocking, fire-and-forget message the agent fires at the user
	// via the `send_user_notification` MCP tool. Unlike AskQuestion it
	// parks no reply and never blocks: the source inserts the row and the
	// channel returns a tool result immediately. issueID is the optional
	// canonical issue key the channel parsed + validated (empty when the
	// agent fires a ticket-less notification); body is the message. The
	// source resolves the session / agent identity / repo on its side. An
	// error here means the row couldn't be inserted; the channel surfaces
	// it as an MCP tool error.
	SendNotification(ctx context.Context, issueID, body string) error

	// FailDispatch reconciles a soft-cancelled dispatch worker (BACI-328).
	// The supervisor calls the `report_subagent_incomplete` MCP tool after
	// a Task subagent ended early/interrupted (control returned to the
	// supervisor, not a normal completion and not a needs_input: pause);
	// the source resolves the dispatch's in-flight Pipeline job and cancels
	// it in place (cancel the running job + its dispatch, halt Auto, stamp
	// the neutral subagent_cancelled pause reason, release the stale claim).
	// note is the supervisor's optional human-readable reason (advisory).
	// Best-effort and idempotent: a dispatch whose job already settled is a
	// clean no-op. This tool SETTLES the dispatch (via the cancel) — it is
	// the terminal acknowledgement for the cancelled-worker case, so the
	// supervisor must call it INSTEAD OF `reply`, never both.
	FailDispatch(ctx context.Context, dispatchID int64, note string) error
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
		// reply / register / send_user_notification /
		// report_subagent_incomplete advertise unconditionally — none of
		// them park a JSON-RPC reply, so the poller-gate reasoning that
		// applies to ask_user_question (a parked reply would never be
		// delivered without the drain step) does not apply to them.
		// ask_user_question only advertises when the poller is on. See
		// ServeMCP / Run split + the BACIO_AGENT_MODE gate in
		// internal/cli/channel.go.
		tools := []any{replyToolSchema(), registerToolSchema(), sendUserNotificationToolSchema(), reportSubagentIncompleteToolSchema()}
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
		"description": "Complete the registration of your Claude Code session with bacio. The SessionStart hook only creates a minimal stub; this call enriches it and makes the session visible to the agent list. Call once on your first turn with the session_id the bacio channel pre-filled for you in the setup-dispatch payload (copy the JSON verbatim — do not paste the literal $CLAUDE_CODE_SESSION_ID placeholder; the validator will refuse it). The session_id must be a structurally valid UUID — a malformed id (e.g. a retyped UUID with a wrong-length group) is rejected, so copy it, do not retype it. A second register from the same `claude` process reconciles to one live session row. Pass model if you know it. Safe to call again — idempotent (first-registration timestamp wins).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Your Claude Code session id — a UUID. The bacio channel pre-fills the real value into the setup-dispatch JSON — copy that value verbatim, do not retype it and do not paste the literal $CLAUDE_CODE_SESSION_ID placeholder. A non-UUID id is rejected.",
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
			"Pass the `issue_id` of the dispatched ticket (e.g. \"BACI-42\") so the question is recorded " +
			"against that issue and surfaces on its kanban card pill and issue workspace shelf — required. " +
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
				"issue_id": map[string]any{
					"type":        "string",
					"description": "The canonical issue key (e.g. \"BACI-42\") the question is asked against. The question is recorded against this issue and surfaces on the issue's kanban card pill and the issue workspace's question shelf. Required — even if the agent has not yet claimed the issue.",
				},
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
			"required": []string{"issue_id", "questions"},
		},
	}
}

// sendUserNotificationToolSchema describes the bacio MCP
// `send_user_notification` tool (BACI-287). Unlike ask_user_question this
// is non-blocking and fire-and-forget — the agent tells the user something
// and carries on immediately; nothing is returned beyond a delivery ack.
// The description stresses that distinction so an agent reaches for
// ask_user_question when it actually needs an answer.
func sendUserNotificationToolSchema() map[string]any {
	return map[string]any{
		"name": "send_user_notification",
		"description": "Send the user a non-blocking notification through the bacio UI — it appears in the " +
			"notification bell (top-right, desktop / web) and the TUI Notifications tab. " +
			"This is fire-and-forget: the call returns immediately and does NOT wait for the user. Use it to " +
			"surface a status update, a heads-up, or a result the user should see but that doesn't need a reply " +
			"(e.g. \"finished implementing BACI-42, PR opened\"). " +
			"If you actually need the user to choose or answer something, use ask_user_question instead — that " +
			"blocks until they respond. " +
			"Pass the optional `issue_id` (e.g. \"BACI-42\") to deep-link the notification to a ticket; omit it " +
			"for a notification that isn't about a specific issue.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"body": map[string]any{
					"type":        "string",
					"description": "The notification message. Required. Multi-line is allowed; keep it short — this is a heads-up, not a document.",
				},
				"issue_id": map[string]any{
					"type":        "string",
					"description": "Optional canonical issue key (e.g. \"BACI-42\") to deep-link the notification to a ticket. Omit for a notification not tied to a specific issue.",
				},
			},
			"required": []string{"body"},
		},
	}
}

// reportSubagentIncompleteToolSchema describes the bacio MCP
// `report_subagent_incomplete` tool (BACI-328). The supervisor calls it
// after a Task subagent ended early/interrupted (a user soft-cancel handed
// control back) so bacio can reconcile the dirty in-flight Pipeline job —
// otherwise the card sits in_pipeline with a running job, an un-released
// claim, and an un-acked dispatch forever. It REPLACES `reply` for that
// dispatch (the cancel settles it; acking a cancelled dispatch errors), so
// the description is explicit about when to call it and when NOT to.
func reportSubagentIncompleteToolSchema() map[string]any {
	return map[string]any{
		"name": "report_subagent_incomplete",
		"description": "Report that a dispatched Task subagent ended EARLY or was INTERRUPTED (a user soft-cancel / Esc " +
			"returned control to you, the supervisor) rather than finishing its job. bacio reconciles the dirty " +
			"in-flight Pipeline job: it cancels the running job and its dispatch, pauses the card in place with a " +
			"neutral \"Cancelled — Start to retry\" halt, and releases the worker's claim. " +
			"Call this INSTEAD OF `reply` for that dispatch — it settles the dispatch itself, so do NOT also call " +
			"`reply` (acking a cancelled dispatch errors). " +
			"Only call it when the subagent did NOT return a normal completion summary AND did NOT return a " +
			"`needs_input:` line. A `needs_input:` return is a legitimate pause (the worker parked an " +
			"ask_user_question) — leave that dispatch alone, do not report it incomplete.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dispatch_id": map[string]any{
					"type":        "integer",
					"description": "The dispatch_id from the <channel> tag whose subagent ended early/interrupted.",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Optional short human-readable reason (e.g. \"user cancelled\"). Advisory.",
				},
			},
			"required": []string{"dispatch_id"},
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
	case "send_user_notification":
		s.handleSendUserNotificationCall(ctx, msg.ID, head.Arguments)
	case "report_subagent_incomplete":
		s.handleReportSubagentIncompleteCall(ctx, msg.ID, head.Arguments)
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

// handleSendUserNotificationCall validates the body + optional issue key,
// asks the source to record a notification, and returns a tool result
// immediately. Unlike ask_user_question this is fire-and-forget — there is
// no parked reply, no pending-map entry, and no poller gate (the tool
// advertises unconditionally, like reply / register).
// Any failure surfaces as an MCP tool error rather than dropping the
// JSON-RPC connection.
func (s *Server) handleSendUserNotificationCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	var args struct {
		Body    string `json:"body"`
		IssueID string `json:"issue_id"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.toolResult(id, true, "invalid send_user_notification arguments: "+err.Error())
			return
		}
	}
	body := strings.TrimSpace(args.Body)
	if body == "" {
		s.toolResult(id, true, "send_user_notification requires a body (the message to show the user)")
		return
	}
	// issue_id is optional — canonicalise it only when present so a
	// ticket-less notification fires cleanly.
	var issueID string
	if raw := strings.TrimSpace(args.IssueID); raw != "" {
		prefix, number, err := store.ParseIssueKey(raw)
		if err != nil {
			s.toolResult(id, true, "send_user_notification issue_id rejected: "+err.Error())
			return
		}
		issueID = fmt.Sprintf("%s-%d", prefix, number)
	}
	if err := s.src.SendNotification(ctx, issueID, body); err != nil {
		s.logf("bacio channel: send_user_notification: %v", err)
		s.toolResult(id, true, "could not send notification: "+err.Error())
		return
	}
	s.toolResult(id, false, "notification sent")
}

// handleReportSubagentIncompleteCall validates the dispatch_id, asks the
// source to reconcile the dirty in-flight Pipeline job (BACI-328), and
// returns a tool result. Mirrors handleReplyCall's arg-validation shape —
// dispatch_id required, note optional/advisory — but this is the terminal
// acknowledgement for a cancelled worker: the source's FailDispatch settles
// the dispatch by cancelling it, so the supervisor calls this INSTEAD OF
// `reply`. Idempotent on the source side; any failure surfaces as an MCP
// tool error rather than dropping the JSON-RPC connection.
func (s *Server) handleReportSubagentIncompleteCall(ctx context.Context, id json.RawMessage, rawArgs json.RawMessage) {
	var args struct {
		DispatchID int64  `json:"dispatch_id"`
		Note       string `json:"note"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.toolResult(id, true, "invalid report_subagent_incomplete arguments: "+err.Error())
			return
		}
	}
	if args.DispatchID == 0 {
		s.toolResult(id, true, "report_subagent_incomplete requires a dispatch_id (the value from the <channel> tag)")
		return
	}
	if err := s.src.FailDispatch(ctx, args.DispatchID, strings.TrimSpace(args.Note)); err != nil {
		s.logf("bacio channel: report_subagent_incomplete dispatch %d: %v", args.DispatchID, err)
		s.toolResult(id, true, fmt.Sprintf("could not reconcile dispatch %d: %v", args.DispatchID, err))
		return
	}
	s.toolResult(id, false, fmt.Sprintf("reconciled cancelled dispatch %d", args.DispatchID))
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
	// BACI-128: issue_id is a required, validated input — without it
	// the parked row goes nowhere (the kanban card surface filters by
	// issue_key) and the MCP call hangs forever. Unmarshal a thin
	// wrapper so we can validate the issue id before the payload.
	var args struct {
		IssueID   string               `json:"issue_id"`
		Questions []model.QuestionItem `json:"questions"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.toolResult(id, true, "invalid ask_user_question arguments: "+err.Error())
			return
		}
	}
	payload := model.QuestionPayload{Questions: args.Questions}
	if err := model.ValidateQuestionPayload(payload); err != nil {
		s.toolResult(id, true, "ask_user_question payload rejected: "+err.Error())
		return
	}
	rawIssueID := strings.TrimSpace(args.IssueID)
	if rawIssueID == "" {
		s.toolResult(id, true, "ask_user_question requires an issue_id (e.g. \"BACI-42\") so the question is recorded against that issue and surfaces on its kanban card")
		return
	}
	prefix, number, err := store.ParseIssueKey(rawIssueID)
	if err != nil {
		s.toolResult(id, true, "ask_user_question issue_id rejected: "+err.Error())
		return
	}
	issueID := fmt.Sprintf("%s-%d", prefix, number)
	requestUUID, err := s.src.AskQuestion(ctx, issueID, payload)
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
// queued work), drain-answered-questions (deliver any settled
// ask_user_question replies), then drain-user-messages (push any
// BACI-286 steer messages). EnsureSetup runs every tick — the source
// is responsible for being a no-op once register has fired. Heartbeat
// runs every tick regardless of whether there's anything to drain —
// it's how the channel stays correlatable.
func (s *Server) tick(ctx context.Context) {
	if err := s.src.Heartbeat(ctx); err != nil {
		s.logf("bacio channel: heartbeat: %v", err)
	}
	if err := s.src.EnsureSetup(ctx); err != nil {
		s.logf("bacio channel: ensure-setup: %v", err)
	}
	s.drainOnce(ctx)
	s.drainAnsweredQuestions(ctx)
	s.drainUserMessages(ctx)
}

// drainUserMessages pulls every un-consumed BACI-286 steer message for
// this channel's session(s) and pushes each as a `kind="message"`
// notification. The source marks them consumed and dedups per-process,
// so a row is emitted once per channel lifetime. Best effort — a drain
// error is logged, not fatal.
func (s *Server) drainUserMessages(ctx context.Context) {
	msgs, err := s.src.DrainUserMessages(ctx)
	if err != nil {
		s.logf("bacio channel: drain user messages: %v", err)
		return
	}
	for _, m := range msgs {
		s.pushMessage(m)
	}
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
	// BACI-226: surface the resolved base branch on the meta tag so
	// a reader of the channel log can spot it without parsing the
	// payload. The worker's Task prompt carries the same value via
	// the payload's <base_branch> stub tag — this meta attribute is
	// the supervisor-side breadcrumb.
	if e.BaseBranch != "" {
		meta["base_branch"] = e.BaseBranch
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

// pushMessage emits one BACI-286 user→agent steer message as a
// notifications/claude/channel event with meta.kind="message". It rides
// the same notification a dispatch uses but carries NO dispatch_id (it
// is fire-and-forget — nothing to reply/ack), so the worker and a reader
// of the channel log can tell a steer message apart from a dispatch by
// the kind attribute alone. meta keys must be bare identifiers — Claude
// Code silently drops keys with hyphens or other characters.
func (s *Server) pushMessage(m UserMessageEvent) {
	meta := map[string]string{
		"kind": "message",
		"from": m.From,
	}
	s.write(rpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/claude/channel",
		Params: map[string]any{
			"content": m.Body,
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
