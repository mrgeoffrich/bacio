package anthropic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// ThreadState is the per-job carry the recorder threads from one capture to the
// next so Classify can tell a primary turn (extends the main conversation) from
// an auxiliary one (a title-gen / structured-output probe). It is the last
// primary capture's fingerprint + message count; the zero value means "no
// primary thread established yet".
//
// It is small and serialisable so the recorder can recover it from the store
// (latestThreadState) without holding job state in memory across process
// restarts.
type ThreadState struct {
	// SystemFP is the fingerprint of the established thread's system prompt; ""
	// until the first primary capture.
	SystemFP string
	// Model is the established thread's model — a different model (e.g. haiku
	// title-gen) is auxiliary even with a matching message count.
	Model string
	// MessageCount is the request messages[] length of the last primary
	// capture. The next primary capture's count must be >= this (it appends the
	// prior assistant turn + the new user/tool_result turn).
	MessageCount int
}

// billingHeaderPrefix marks the volatile system block the Claude client prepends
// to every /v1/messages request — an x-anthropic-billing-header text block whose
// `cch=<hash>` cache token changes on essentially every call. systemFingerprint
// drops it before hashing so the same conversation hashes identically across
// turns (BACI-326).
const billingHeaderPrefix = "x-anthropic-billing-header:"

// systemFingerprint hashes the request's system block so two captures in the
// same conversation (identical system prompt) share a fingerprint while a probe
// with a different system prompt doesn't.
//
// The Claude client prepends a volatile x-anthropic-billing-header text block
// whose `cch` cache hash drifts per request, so hashing the system bytes
// verbatim gave every turn after the first a different fingerprint — Classify
// then filed them all auxiliary and turn_count capped at 1 (BACI-326). To make
// the same conversation hash identically across turns, we decode the system as a
// block list and drop a leading billing-header block before hashing. The strip
// is tight: only a block at index 0 whose type is text and whose text starts
// with the billing-header prefix is removed, so a real system prompt that merely
// mentions the string is untouched.
//
// Anything that isn't a Claude-Code block list with a leading billing header —
// the string-shorthand form, a block list without the header, an unexpected
// shape — hashes the original bytes verbatim, exactly as before. The change is
// strictly additive: it can never worsen a shape it doesn't recognise, and the
// 17 genuinely-distinct system prompts in the surveyed corpus stay distinct so
// sibling-thread disambiguation is preserved. An empty system yields "".
func systemFingerprint(system []byte) string {
	if len(system) == 0 {
		return ""
	}
	// Try the block-list shape; only the leading block's type/text matter for the
	// strip decision, so a tiny local struct (extra fields ignored) is enough.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(system, &blocks); err == nil &&
		len(blocks) > 0 && blocks[0].Type == "text" &&
		strings.HasPrefix(blocks[0].Text, billingHeaderPrefix) {
		// Re-encode the survivors deterministically (a slice of a fixed struct
		// marshals with stable key order) and hash that stable portion.
		if stable, err := json.Marshal(blocks[1:]); err == nil {
			sum := sha256.Sum256(stable)
			return hex.EncodeToString(sum[:])
		}
	}
	// No billing header to strip (a string-form system, a non-Claude-Code block
	// list, or an unexpected shape) — hash the bytes verbatim, as before.
	sum := sha256.Sum256(system)
	return hex.EncodeToString(sum[:])
}

// ParseAndClassify folds the two pure parse steps a capture goes through — decode
// the raw .http into a ParsedCapture (ParseCapture), then classify it against the
// job's prior thread state (Classify) — into one call so the live recorder and the
// BACI-321 backfill sweep run identical classification and can never drift. raw is
// the rendered (inflated, redacted) .http bytes the recorder wrote to disk; prev is
// the job's LatestThreadState carry (the zero value for a fresh thread). It returns
// the parsed capture, whether it's a primary turn, and the request-message delta —
// everything the caller needs to AddProxyMessage. A decode error (a truncated or
// malformed capture, or a non-stream reply) is returned verbatim so the caller can
// treat it as a terminal parse failure.
func ParseAndClassify(raw []byte, prev ThreadState) (pc *model.ParsedCapture, isPrimary bool, delta []model.AnthropicMessage, err error) {
	pc, err = ParseCapture(raw)
	if err != nil {
		return nil, false, nil, err
	}
	isPrimary, delta, _ = Classify(pc, prev)
	return pc, isPrimary, delta, nil
}

// Classify decides whether a freshly-parsed capture extends the job's primary
// thread (primary) or is an auxiliary call (title-gen, structured-output probe,
// a different model, or a non-extending short call), computes the request-message
// delta for a primary capture, and returns the thread state to carry to the next
// capture.
//
// The rule (from the plan's Q3): a capture is primary when it has no
// output_config probe AND either establishes the thread (no prior state) or
// extends it — same (model, system fingerprint) with a message count that grew.
// Everything else is auxiliary. The delta is the request messages appended since
// the prior primary count; a primary capture that does not in fact grow (e.g.
// context compaction shrank the messages) is treated as a fresh segment — its
// whole messages[] is the delta and the state resets to it — rather than
// computing a negative slice.
func Classify(pc *model.ParsedCapture, prev ThreadState) (isPrimary bool, delta []model.AnthropicMessage, next ThreadState) {
	// A structured-output probe is never part of the main conversation.
	if pc.HasOutputConfig {
		return false, nil, prev
	}

	// No thread yet — this capture establishes it. The delta is the whole
	// request messages[] (the opening user turn, possibly more).
	if prev.SystemFP == "" {
		return true, requestDelta(pc.Messages),
			ThreadState{SystemFP: pc.SystemFP, Model: pc.Model, MessageCount: pc.MessageCount}
	}

	// A different fingerprint or model is a sibling thread within the dispatch —
	// auxiliary. (We keep tracking the original primary thread.)
	if pc.SystemFP != prev.SystemFP || pc.Model != prev.Model {
		return false, nil, prev
	}

	// Same thread. The normal case: the count grew — the delta is the appended
	// tail since the prior primary count.
	if pc.MessageCount > prev.MessageCount {
		delta = requestDelta(pc.Messages[prev.MessageCount:])
		return true, delta, ThreadState{SystemFP: pc.SystemFP, Model: pc.Model, MessageCount: pc.MessageCount}
	}

	// Same fingerprint/model but the count didn't grow — a compaction or a
	// resend. Treat it as a fresh segment (whole messages[] as delta) so we
	// never compute a negative slice, and reset the count to this capture's.
	delta = requestDelta(pc.Messages)
	return true, delta, ThreadState{SystemFP: pc.SystemFP, Model: pc.Model, MessageCount: pc.MessageCount}
}

// requestDelta returns the user-side messages from an appended request tail,
// dropping any leading assistant message. Between two consecutive primary
// captures the client appends exactly the prior turn's assistant response
// (echoed back so the API has the full context) followed by the new
// user/tool_result turn. The assistant echo is already represented by the prior
// capture's reconstructed Turn, so persisting it again would double-count it in
// the assembled transcript; the delta carries only the genuinely-new user input.
func requestDelta(tail []model.AnthropicMessage) []model.AnthropicMessage {
	for len(tail) > 0 && tail[0].Role == "assistant" {
		tail = tail[1:]
	}
	if len(tail) == 0 {
		return nil
	}
	return append([]model.AnthropicMessage(nil), tail...)
}

// AssembleTranscript reconstructs one job's ordered conversation from its
// per-capture rows in capture order. Each capture contributes its request-message
// delta (the user/tool_result turns appended since the prior primary capture)
// followed by its reconstructed assistant turn, in sequence; the auxiliary
// captures are collected separately. Token usage is summed across the primary
// turns. The result is always non-nil.
//
// caps must be ordered by capture sequence (the recorder writes them in request
// order; the store reads them back ORDER BY id). Each element pairs the parsed
// capture's delta + turn with its primary/auxiliary flag.
func AssembleTranscript(dispatchID *int64, caps []AssembledRow) *model.AnthropicTranscript {
	tr := &model.AnthropicTranscript{
		DispatchID: dispatchID,
		Messages:   []model.AnthropicMessage{},
	}
	for _, c := range caps {
		if !c.IsPrimary {
			tr.Auxiliary = append(tr.Auxiliary, c.Turn)
			continue
		}
		if tr.Model == "" {
			tr.Model = c.Turn.Model
		}
		tr.Messages = append(tr.Messages, c.Delta...)
		// The assistant turn becomes an assistant message in the ordered thread.
		tr.Messages = append(tr.Messages, model.AnthropicMessage{
			Role:    "assistant",
			Content: c.Turn.Blocks,
		})
		sumUsage(&tr.Usage, c.Turn.Usage)
	}
	return tr
}

// AssembledRow is one capture's contribution to a job transcript: its parsed
// assistant turn, the request-message delta computed by Classify, and whether
// it's part of the primary thread. The store yields these from proxy_messages
// rows (one per parseable capture) in capture order.
type AssembledRow struct {
	IsPrimary bool
	Delta     []model.AnthropicMessage
	Turn      model.AnthropicTurn
}

// sumUsage adds one turn's usage into the running job total. Unlike mergeUsage
// (which takes the later non-zero value within a single turn), this accumulates
// across turns — a job's total cost is the sum of every primary turn.
func sumUsage(dst *model.AnthropicUsage, src model.AnthropicUsage) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheCreationInputTokens += src.CacheCreationInputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
	dst.ThinkingTokens += src.ThinkingTokens
}
