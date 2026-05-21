package channel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TranscriptMeta is the small set of fields the channel knows about a
// subagent transcript before it renders the digest. agentType /
// description come from the sibling agent-<id>.meta.json; the rest are
// resolved by the channel-side AttachTranscript implementation. Every
// field is optional — the renderer degrades gracefully when one is "".
type TranscriptMeta struct {
	AgentID         string // the agentId (no "agent-" prefix)
	AgentType       string // e.g. "bacio-plan-worker", "Explore" — from meta.json
	Description     string // the Task call's description — from meta.json
	ParentSessionID string // the parent session dir name the transcript was filed under
	SourcePath      string // absolute path to the raw .jsonl transcript
	SizeBytes       int64  // raw .jsonl byte size
	Note            string // optional supervisor-supplied note
	GeneratedAt     string // RFC 3339 stamp
}

// DigestCap caps the rendered digest size. It is deliberately far
// under the 1 MiB ValidateBody doc-body cap so a transcript full of
// huge file reads can never push the digest past what a doc accepts.
const DigestCap = 256 * 1024

// perToolResultCap truncates a single tool_result block in the digest.
// Transcripts routinely embed multi-hundred-KB file reads; without a
// per-block cap one read would dominate the whole digest.
const perToolResultCap = 2 * 1024

// perToolInputCap truncates a tool_use input block.
const perToolInputCap = 1 * 1024

// RenderTranscriptDigest parses a subagent transcript (.jsonl, one JSON
// event per line) and renders a human-readable markdown digest. It is
// pure and total: malformed lines are skipped with a noted count, and
// the output is always capped at DigestCap with an explicit truncation
// footer pointing at the raw transcript path.
func RenderTranscriptDigest(raw []byte, meta TranscriptMeta) string {
	var b strings.Builder

	// ---- Header ----
	b.WriteString("# Subagent transcript — ")
	if meta.Description != "" {
		b.WriteString(meta.Description)
	} else {
		b.WriteString("agent-" + meta.AgentID)
	}
	b.WriteString("\n\n")
	writeHeaderField(&b, "Agent ID", "agent-"+meta.AgentID)
	writeHeaderField(&b, "Agent type", meta.AgentType)
	writeHeaderField(&b, "Description", meta.Description)
	writeHeaderField(&b, "Parent session", meta.ParentSessionID)
	writeHeaderField(&b, "Source transcript", meta.SourcePath)
	if meta.SizeBytes > 0 {
		writeHeaderField(&b, "Raw size", fmt.Sprintf("%d bytes", meta.SizeBytes))
	}
	writeHeaderField(&b, "Supervisor note", meta.Note)
	writeHeaderField(&b, "Digest generated", meta.GeneratedAt)
	b.WriteString("\n> This is a rendered digest of a completed subagent's conversation. ")
	b.WriteString("The full raw transcript (line-delimited JSON) is at the source path above.\n\n")
	b.WriteString("---\n\n")

	// ---- Body ----
	lines := splitJSONLines(raw)
	rendered := 0
	skipped := 0
	truncated := false
	emitted := b.Len()

	for i, line := range lines {
		var ev transcriptEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			skipped++
			continue
		}
		section := renderEvent(ev)
		if section == "" {
			continue
		}
		// Reserve room for the truncation footer.
		if emitted+len(section)+512 > DigestCap {
			truncated = true
			omitted := len(lines) - i
			b.WriteString(fmt.Sprintf(
				"\n---\n\n_[transcript truncated — %d of %d events omitted to keep the digest under %d KB. Full raw transcript at %s]_\n",
				omitted, len(lines), DigestCap/1024, meta.SourcePath))
			break
		}
		b.WriteString(section)
		emitted = b.Len()
		rendered++
	}

	if !truncated {
		b.WriteString("\n---\n\n")
		b.WriteString(fmt.Sprintf("_Digest of %d transcript events", rendered))
		if skipped > 0 {
			b.WriteString(fmt.Sprintf(" (%d unparseable lines skipped)", skipped))
		}
		b.WriteString("._\n")
	}

	return b.String()
}

// transcriptEvent is the subset of a Claude Code transcript row the
// digest renderer reads. Unknown fields are ignored.
type transcriptEvent struct {
	Type    string         `json:"type"`    // "user" | "assistant" | "attachment" | ...
	Message *transcriptMsg `json:"message"` // present on user/assistant rows
	Summary string         `json:"summary"` // present on summary rows
}

type transcriptMsg struct {
	Role string `json:"role"`
	// Content is either a bare string or an array of content blocks.
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type    string          `json:"type"` // "text" | "tool_use" | "tool_result" | "thinking"
	Text    string          `json:"text"`
	Name    string          `json:"name"`    // tool_use
	Input   json.RawMessage `json:"input"`   // tool_use
	Content json.RawMessage `json:"content"` // tool_result (string or array of blocks)
	IsError bool            `json:"is_error"`
}

// renderEvent turns one transcript event into a markdown section, or
// "" when the event carries nothing worth showing.
func renderEvent(ev transcriptEvent) string {
	if ev.Message == nil {
		return ""
	}
	role := ev.Message.Role
	if role == "" {
		role = ev.Type
	}

	// Content may be a bare string (common for the initial user prompt).
	var asString string
	if err := json.Unmarshal(ev.Message.Content, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return ""
		}
		return roleHeading(role) + "\n\n" + asString + "\n\n"
	}

	var blocks []contentBlock
	if err := json.Unmarshal(ev.Message.Content, &blocks); err != nil {
		return ""
	}

	var b strings.Builder
	wroteHeading := false
	heading := func() {
		if !wroteHeading {
			b.WriteString(roleHeading(role) + "\n\n")
			wroteHeading = true
		}
	}
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			if strings.TrimSpace(blk.Text) == "" {
				continue
			}
			heading()
			b.WriteString(blk.Text + "\n\n")
		case "thinking":
			// Thinking blocks are folded — noted, not inlined.
			heading()
			b.WriteString("_[thinking]_\n\n")
		case "tool_use":
			heading()
			b.WriteString("**Tool call:** `" + blk.Name + "`\n\n")
			if in := compactJSON(blk.Input, perToolInputCap); in != "" {
				b.WriteString("```json\n" + in + "\n```\n\n")
			}
		case "tool_result":
			heading()
			label := "**Tool result:**"
			if blk.IsError {
				label = "**Tool result (error):**"
			}
			b.WriteString(label + "\n\n")
			res := toolResultText(blk.Content)
			if res == "" {
				b.WriteString("_(empty)_\n\n")
			} else {
				b.WriteString("```\n" + truncate(res, perToolResultCap) + "\n```\n\n")
			}
		}
	}
	return b.String()
}

func roleHeading(role string) string {
	switch role {
	case "user":
		return "## User"
	case "assistant":
		return "## Assistant"
	default:
		if role == "" {
			return "## Event"
		}
		return "## " + strings.Title(role) //nolint:staticcheck // ASCII role names only
	}
}

// toolResultText flattens a tool_result content payload (which may be a
// bare string or an array of {type:text,text:...} blocks) to plain text.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, blk := range blocks {
			if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
				parts = append(parts, blk.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// compactJSON re-marshals raw JSON compactly and truncates it. Returns
// "" for empty / null input.
func compactJSON(raw json.RawMessage, cap int) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if compact, err := json.Marshal(v); err == nil {
			s = string(compact)
		}
	}
	return truncate(s, cap)
}

func truncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	return s[:cap] + fmt.Sprintf("\n… [truncated, %d more bytes]", len(s)-cap)
}

func writeHeaderField(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("- **" + label + ":** " + value + "\n")
}

// splitJSONLines splits a .jsonl byte slice into its non-empty lines.
func splitJSONLines(raw []byte) [][]byte {
	var out [][]byte
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, []byte(ln))
	}
	return out
}
