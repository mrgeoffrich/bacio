// Package anthropic parses a captured Anthropic /v1/messages exchange (the
// reverse-proxy .http file BACI-302/305 wrote) into the structured per-turn
// detail BACI-306 persists. It is pure: it imports only internal/model and the
// standard library, so the recorder (write), the store read API, the CLI, and
// the tests all share one parser with no store/proxy coupling.
//
// The on-disk capture is the inverse of renderRawCapture in
// internal/api/proxy_recorder.go: a "==== REQUEST ====" header block + body,
// then "==== RESPONSE ====" header block + body, written with CRLF, with the
// gzip already inflated on disk and secrets redacted in the headers. The
// request/response boundary is matched line-anchored (the marker on its own
// line) so an in-body occurrence of the literal "==== RESPONSE ====" text — the
// request body carries the whole conversation, which often contains it — can't
// truncate the split (BACI-323). The
// response of a streaming /v1/messages turn is an SSE event stream that
// reconstructs exactly one assistant turn; the request body (application/json)
// carries the whole conversation-so-far. The parsing rules are pinned by the
// reference decoder doc (proxy-capture-decoder.md), validated against 220 real
// SSE turns.
package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// ErrNotStream is returned by ParseCapture when the response isn't an SSE
// stream — a count_tokens / error JSON reply, the separate non-message shape.
// The caller (recorder) only parses is_stream captures, so it never feeds
// these in; the parser still classifies defensively.
var ErrNotStream = errors.New("anthropic: response is not an SSE stream")

const (
	requestMarker  = "==== REQUEST ===="
	responseMarker = "==== RESPONSE ===="
)

// ParseCapture decodes one raw .http capture into a ParsedCapture: the
// request-side facts the assembler classifies on (model, system fingerprint,
// message count, output_config presence) and the reconstructed assistant turn
// from the response SSE stream. The Delta field is left empty — Classify fills
// it once it knows the prior thread state.
//
// It does NOT read the request's metadata.user_id (device_id / account_uuid /
// session_id PII): that field is never decoded into the model, so it can never
// be persisted.
func ParseCapture(raw []byte) (*model.ParsedCapture, error) {
	// Headers are already redacted on disk; the parser reads only the bodies.
	_, reqBody, respHead, respBody := splitCapture(raw)

	// Defensive classification: the recorder only feeds is_stream captures, but
	// a non-SSE response (count_tokens / error JSON) has no assistant turn to
	// reconstruct, so bail rather than hand back a hollow turn.
	if !isEventStream(respHead) {
		return nil, ErrNotStream
	}

	pc := &model.ParsedCapture{}

	// Request side: model, system fingerprint, message count, output_config.
	var req anthropicRequest
	if err := json.Unmarshal(bytes.TrimSpace(reqBody), &req); err != nil {
		return nil, err
	}
	pc.Model = req.Model
	pc.SystemFP = systemFingerprint(req.System)
	pc.MessageCount = len(req.Messages)
	pc.HasOutputConfig = hasStructuredFormat(req.OutputConfig)
	pc.Messages = req.Messages
	pc.Turn = decodeSSE(respBody)
	if pc.Turn.Model == "" {
		pc.Turn.Model = req.Model
	}
	return pc, nil
}

// anthropicRequest is the subset of the /v1/messages request body the parser
// reads. metadata is deliberately absent — see ParseCapture. system is a
// RawMessage because the API accepts either a plain string or a block list, and
// the fingerprint hashes the bytes either way. output_config carries the probe
// signal, but only via its `format` (json_schema) child — see hasStructuredFormat:
// the Opus 4.x API also overloads output_config to carry a reasoning-`effort`
// hint on essentially every normal turn, so its bare presence no longer marks a
// probe (BACI-325).
type anthropicRequest struct {
	Model        string                   `json:"model"`
	System       json.RawMessage          `json:"system"`
	Messages     []model.AnthropicMessage `json:"messages"`
	OutputConfig json.RawMessage          `json:"output_config"`
}

// hasStructuredFormat reports whether output_config carries a structured-output
// `format` (json_schema) child — the signal that the capture is a structured-output
// probe (title-gen and friends) rather than a normal conversation turn. Keying off
// `format` rather than the bare presence of output_config is the BACI-325 fix: the
// Opus 4.x API overloads output_config to also carry a reasoning-`effort` hint
// (`{"effort":"xhigh"}`) on essentially every normal turn, so the bare-presence
// heuristic classified every real turn as a probe → auxiliary → zero primary turns.
// An `effort`-only config, an absent/null output_config, or a body that won't decode
// all yield false (primary-eligible); only a present, non-null `format` yields true.
func hasStructuredFormat(oc json.RawMessage) bool {
	if len(oc) == 0 {
		return false
	}
	var cfg struct {
		Format json.RawMessage `json:"format"`
	}
	if err := json.Unmarshal(oc, &cfg); err != nil {
		return false
	}
	return len(cfg.Format) > 0 && !bytes.Equal(bytes.TrimSpace(cfg.Format), []byte("null"))
}

// splitCapture partitions the raw .http into (request headers, request body,
// response headers, response body), normalising CRLF to LF so a single split
// rule works regardless of how the file was read. Mirrors the Python reference
// decoder's split_capture / section helpers (the read contract for
// renderRawCapture's layout).
//
// The request/response boundary is matched line-anchored — on the full line
// "\n==== RESPONSE ====\n" — not as a bare substring (BACI-323). The request
// body carries the whole conversation-so-far, which in this repo frequently
// contains the literal "==== RESPONSE ====" text (agents read the proxy
// capture decoder / parse.go / docs into context). A bare-substring cut fires
// on that in-body occurrence and truncates the request JSON mid-object, so
// json.Unmarshal returns "unexpected end of JSON input". renderRawCapture
// always writes the real boundary as "\r\n==== RESPONSE ====\r\n" (its own
// line, → "\n==== RESPONSE ====\n" once LF-normalised), so the anchored form
// matches exactly the recorder's boundary while an in-body occurrence — which
// sits inside a single-line JSON body, never preceded by a newline byte — can't
// match. A bare-substring cut is kept as a fallback for marker-less inputs
// (hand-written fixtures / a future format tweak) so old behaviour is preserved
// rather than handing back an empty response half.
func splitCapture(raw []byte) (reqHead, reqBody, respHead, respBody []byte) {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	reqPart, respPart, ok := strings.Cut(s, "\n"+responseMarker+"\n")
	if !ok {
		// No line-anchored boundary (a marker-less or hand-shaped capture):
		// fall back to the bare-substring cut so old behaviour is preserved.
		reqPart, respPart, _ = strings.Cut(s, responseMarker)
	}
	// Strip the leading "==== REQUEST ====\n" the file always opens with. Trim
	// leading newlines first, then drop the prefix — an in-body "==== REQUEST
	// ====" can't be the one removed (it isn't at the head of the request part).
	reqPart = strings.TrimLeft(reqPart, "\n")
	reqPart = strings.TrimPrefix(reqPart, requestMarker+"\n")
	rh, rb := section(reqPart)
	sh, sb := section(respPart)
	return []byte(rh), []byte(rb), []byte(sh), []byte(sb)
}

// section splits one half (the text after a marker) at the first blank line
// into a header block and a body. Leading newlines are trimmed first so the
// marker's trailing CRLF doesn't swallow the blank-line split.
func section(part string) (head, body string) {
	part = strings.TrimLeft(part, "\n")
	h, b, _ := strings.Cut(part, "\n\n")
	return strings.Trim(h, "\n"), b
}

// isEventStream reports whether the response header block declares a
// text/event-stream Content-Type — the SSE turns the parser reconstructs. The
// header block is already LF-normalised by splitCapture.
func isEventStream(respHead []byte) bool {
	for _, line := range strings.Split(string(respHead), "\n") {
		if name, val, ok := strings.Cut(line, ":"); ok &&
			strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return strings.Contains(strings.ToLower(val), "text/event-stream")
		}
	}
	return false
}

// decodeSSE walks the response event stream and reconstructs one assistant
// turn: ordered text / thinking / tool_use blocks, the merged token usage
// (input/cache off message_start, output/thinking off message_delta), and the
// stop reason off message_delta. The data: JSON in real captures carries
// trailing whitespace inside the object, so each line is trimmed and decoded
// leniently — a block that won't parse is skipped rather than failing the turn.
func decodeSSE(respBody []byte) model.AnthropicTurn {
	turn := model.AnthropicTurn{Blocks: []model.AnthropicBlock{}}

	// blockByIndex accumulates the in-flight content blocks by their SSE index,
	// so an interleaved tool_use's input_json_delta fragments land on the right
	// block. order preserves emission order for the final Blocks slice.
	type pending struct {
		blk      model.AnthropicBlock
		toolJSON strings.Builder // accumulated input_json_delta partial_json
		isTool   bool
	}
	blocks := map[int]*pending{}
	var order []int

	for _, chunk := range splitSSEBlocks(respBody) {
		data := sseData(chunk)
		if data == "" {
			continue
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			continue // lenient: a malformed event line never sinks the turn.
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				mergeUsage(&turn.Usage, ev.Message.Usage)
				if ev.Message.Model != "" {
					turn.Model = ev.Message.Model
				}
			}
		case "content_block_start":
			p := &pending{}
			if ev.ContentBlock != nil {
				p.blk.Type = ev.ContentBlock.Type
				switch ev.ContentBlock.Type {
				case "tool_use":
					p.isTool = true
					p.blk.ID = ev.ContentBlock.ID
					p.blk.Name = ev.ContentBlock.Name
				case "text":
					p.blk.Text = ev.ContentBlock.Text
				case "thinking":
					p.blk.Thinking = ev.ContentBlock.Thinking
				}
			}
			blocks[ev.Index] = p
			order = append(order, ev.Index)
		case "content_block_delta":
			p := blocks[ev.Index]
			if p == nil { // a delta before its start — synthesise the slot.
				p = &pending{}
				blocks[ev.Index] = p
				order = append(order, ev.Index)
			}
			if ev.Delta != nil {
				switch ev.Delta.Type {
				case "text_delta":
					p.blk.Type = "text"
					p.blk.Text += ev.Delta.Text
				case "thinking_delta":
					p.blk.Type = "thinking"
					p.blk.Thinking += ev.Delta.Thinking
				case "input_json_delta":
					p.isTool = true
					p.toolJSON.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			mergeUsage(&turn.Usage, ev.Usage)
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				turn.StopReason = ev.Delta.StopReason
			}
		}
	}

	for _, idx := range order {
		p := blocks[idx]
		if p == nil {
			continue
		}
		if p.isTool {
			p.blk.Type = "tool_use"
			if j := strings.TrimSpace(p.toolJSON.String()); j != "" {
				p.blk.Input = json.RawMessage(j)
			}
		}
		if p.blk.Type == "" {
			continue
		}
		turn.Blocks = append(turn.Blocks, p.blk)
	}
	return turn
}

// sseEvent is the lenient decode target for one `data:` JSON object. Every
// field is optional — different event types populate different fields, and the
// parser only reads what it needs per type.
type sseEvent struct {
	Type         string           `json:"type"`
	Index        int              `json:"index"`
	Message      *sseMessage      `json:"message"`
	ContentBlock *sseContentBlock `json:"content_block"`
	Delta        *sseDelta        `json:"delta"`
	Usage        *sseUsage        `json:"usage"`
}

type sseMessage struct {
	Model string    `json:"model"`
	Usage *sseUsage `json:"usage"`
}

type sseContentBlock struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

type sseDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

// sseUsage mirrors Anthropic's usage object across both events: message_start
// carries input/cache tokens, message_delta carries output_tokens and the
// thinking-token breakdown. Pointers/zero values let mergeUsage take the
// non-zero side per field.
type sseUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// mergeUsage folds an SSE usage object into the merged turn usage, taking the
// later non-zero value per field (message_delta's output_tokens supersedes
// message_start's provisional 0, input/cache stay from message_start).
func mergeUsage(dst *model.AnthropicUsage, src *sseUsage) {
	if src == nil {
		return
	}
	if src.InputTokens != 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens != 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens != 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.OutputTokensDetails != nil && src.OutputTokensDetails.ThinkingTokens != 0 {
		dst.ThinkingTokens = src.OutputTokensDetails.ThinkingTokens
	}
}

// splitSSEBlocks splits an SSE body into its event blocks (separated by a blank
// line). Input is already LF-normalised by splitCapture.
func splitSSEBlocks(body []byte) []string {
	return strings.Split(string(body), "\n\n")
}

// sseData returns the concatenated `data:` payload of one SSE block (an event
// may, per the SSE spec, split data across several `data:` lines; Anthropic
// uses one, but we join defensively). Returns "" when the block carries no data.
func sseData(block string) string {
	var b strings.Builder
	for _, line := range strings.Split(block, "\n") {
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			b.WriteString(strings.TrimSpace(v))
		}
	}
	return b.String()
}
