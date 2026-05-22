// Package transcript converts a raw Claude Code agent transcript — the
// .jsonl session log bacio attaches to issues via attach_transcript
// (BACI-85) — into readable Markdown.
//
// A transcript is one JSON object per line. Each line is a `user`,
// `assistant`, or `attachment` entry whose `message.content` is either
// a plain string or a list of content blocks (`text`, `thinking`,
// `tool_use`, `tool_result`). ToMarkdown walks those entries and emits
// a single Markdown document: role headers, tool calls with their JSON
// input, tool results, and thinking blocks as blockquotes. Harness
// noise (`attachment` entries — deferred-tool deltas and the like) is
// dropped.
//
// The renderer is deliberately self-contained: it decodes only the
// fields it needs and never errors on an unrecognised block type (it
// falls back to a JSON dump), so a future transcript-schema addition
// degrades gracefully rather than breaking the surface.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// DefaultTruncate is the per-block character budget used by ToMarkdown.
// Long tool results and tool inputs are clipped past this so a
// transcript full of huge file reads renders to a browsable document
// rather than a wall of text.
const DefaultTruncate = 4000

// Options tunes a Markdown render. The zero value is valid and renders
// with DefaultTruncate.
type Options struct {
	// Truncate is the per-block character budget. Blocks longer than
	// this are clipped with a "… N more chars truncated" marker. Zero
	// means DefaultTruncate; a negative value disables truncation.
	Truncate int
}

func (o Options) truncate() int {
	switch {
	case o.Truncate == 0:
		return DefaultTruncate
	case o.Truncate < 0:
		return 0 // disabled
	default:
		return o.Truncate
	}
}

// entry is one decoded transcript line. Only the fields the renderer
// consumes are declared; unknown fields are ignored.
type entry struct {
	Type      string  `json:"type"` // user | assistant | attachment
	Timestamp string  `json:"timestamp"`
	AgentID   string  `json:"agentId"`
	SessionID string  `json:"sessionId"`
	GitBranch string  `json:"gitBranch"`
	Cwd       string  `json:"cwd"`
	Version   string  `json:"version"`
	Message   message `json:"message"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// block is one content block. The `content` field is itself
// polymorphic (string or []block) — only tool_result populates it — so
// it stays a RawMessage and is flattened lazily.
type block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

// ToMarkdown reads a raw transcript .jsonl from r and returns it as a
// Markdown document. A malformed line aborts the render with an error
// naming the 1-based line number.
func ToMarkdown(r io.Reader, opts Options) (string, error) {
	limit := opts.truncate()

	var out strings.Builder
	sc := bufio.NewScanner(r)
	// Transcript lines can be large (a single tool result embedding a
	// big file read). Give the scanner room well past the store's
	// 2.5 MB transcript cap.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	lineNo := 0
	metaDone := false
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return "", fmt.Errorf("transcript line %d: %w", lineNo, err)
		}

		if !metaDone {
			out.WriteString("# Agent transcript\n\n")
			writeMeta(&out, e)
			metaDone = true
		}

		// `attachment` entries are harness bookkeeping (deferred-tool
		// deltas etc.) — pure noise in a readable transcript.
		if e.Type == "attachment" {
			continue
		}

		chunks := renderContent(e.Message.Content, limit)
		if len(chunks) == 0 {
			continue
		}

		role := e.Message.Role
		if role == "" {
			role = e.Type
		}
		writeHeader(&out, role, e.Timestamp)
		out.WriteString(strings.Join(chunks, "\n\n"))
		out.WriteString("\n\n")
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	return out.String(), nil
}

func writeMeta(out *strings.Builder, e entry) {
	rows := []struct{ label, val string }{
		{"Agent", e.AgentID},
		{"Session", e.SessionID},
		{"Branch", e.GitBranch},
		{"Working dir", e.Cwd},
		{"CC version", e.Version},
	}
	wrote := false
	for _, r := range rows {
		if r.val == "" {
			continue
		}
		fmt.Fprintf(out, "- **%s:** `%s`\n", r.label, r.val)
		wrote = true
	}
	if wrote {
		out.WriteString("\n")
	}
}

func writeHeader(out *strings.Builder, role, ts string) {
	icon := "•"
	switch role {
	case "user":
		icon = "👤"
	case "assistant":
		icon = "🤖"
	}
	fmt.Fprintf(out, "## %s %s", icon, capitalize(role))
	if ts != "" {
		fmt.Fprintf(out, "  ·  _%s_", ts)
	}
	out.WriteString("\n\n")
}

// capitalize upper-cases the first rune of s. Role names are ASCII, so
// a rune-aware helper is overkill — but it keeps the renderer free of
// the deprecated strings.Title.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// renderContent turns one message's `content` (string or []block) into
// an ordered list of Markdown chunks.
func renderContent(raw json.RawMessage, limit int) []string {
	if len(raw) == 0 {
		return nil
	}
	// content as a plain string.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []string{s}
	}
	// content as a list of blocks.
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var chunks []string
	for _, b := range blocks {
		if c := renderBlock(b, limit); c != "" {
			chunks = append(chunks, c)
		}
	}
	return chunks
}

func renderBlock(b block, limit int) string {
	switch b.Type {
	case "text":
		if strings.TrimSpace(b.Text) == "" {
			return ""
		}
		return b.Text
	case "thinking":
		t := strings.TrimSpace(b.Thinking)
		if t == "" {
			return ""
		}
		var sb strings.Builder
		sb.WriteString("> 💭 **Thinking**\n>\n")
		for i, line := range strings.Split(clip(t, limit), "\n") {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("> ")
			sb.WriteString(line)
		}
		return sb.String()
	case "tool_use":
		input := "{}"
		if len(b.Input) > 0 {
			input = indentJSON(b.Input)
		}
		name := b.Name
		if name == "" {
			name = "?"
		}
		return fmt.Sprintf("**🔧 Tool call: `%s`**\n\n%s", name, fence(input, "json", limit))
	case "tool_result":
		errMark := ""
		if b.IsError {
			errMark = " (error)"
		}
		return fmt.Sprintf("**↳ Tool result%s**\n\n%s", errMark, fence(flatten(b.Content), "", limit))
	default:
		return fence(indentJSON(rawOf(b)), "json", limit)
	}
}

// flatten reduces a tool_result `content` value — string, or a list of
// content blocks — to a plain string. `text` blocks contribute their
// text, `image` blocks a placeholder, and any other block its compact
// JSON, so an unrecognised block degrades to readable JSON rather than
// a dump of the renderer's own struct.
func flatten(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var rawBlocks []json.RawMessage
	if json.Unmarshal(raw, &rawBlocks) != nil {
		return string(raw)
	}
	var parts []string
	for _, rb := range rawBlocks {
		var b block
		_ = json.Unmarshal(rb, &b)
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			parts = append(parts, "[image]")
		default:
			parts = append(parts, string(rb))
		}
	}
	return strings.Join(parts, "\n")
}

// rawOf re-marshals a decoded block back to JSON for the fallback
// renderer of an unrecognised block type.
func rawOf(b block) json.RawMessage {
	j, err := marshalNoEsc(b)
	if err != nil {
		return json.RawMessage("{}")
	}
	return j
}

// indentJSON pretty-prints compact JSON without HTML-escaping `&`, `<`,
// `>` (so a shell command like `a && b` stays legible). On a parse
// failure it returns the input unchanged — a renderer never loses data
// to a formatting step.
func indentJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return string(raw)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// marshalNoEsc is json.Marshal without HTML escaping.
func marshalNoEsc(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// clip truncates s to limit chars, appending a marker naming the
// omitted count. limit <= 0 disables truncation.
func clip(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + fmt.Sprintf("\n… [%d more chars truncated]", len(r)-limit)
}

// fence wraps text in a code fence long enough to survive any run of
// backticks the text itself contains.
func fence(text, lang string, limit int) string {
	text = clip(text, limit)
	ticks := "```"
	for strings.Contains(text, ticks) {
		ticks += "`"
	}
	return ticks + lang + "\n" + text + "\n" + ticks
}
