// Type model for parsed Claude Code subagent transcripts (BACI-125).
//
// A transcript is one JSON envelope per line. The schema is
// Claude-Code-shaped (not bacio-shaped), so this module owns it
// outright — the React tree is the only consumer.
//
// We decode only the fields the renderer reads; everything else stays
// in a stashed `raw` field so the per-event `[▶]` raw drawer can show
// the original line verbatim and the renderer never silently mutates
// the source. Same approach the Go decoder in `internal/transcript`
// takes.

export type TokenUsage = {
  input_tokens?: number;
  output_tokens?: number;
  cache_creation_input_tokens?: number;
  cache_read_input_tokens?: number;
};

// A tool_result `content` value is polymorphic — either a plain string
// or a list of `{type, text|...}` blocks. We carry both shapes here
// and let the per-tool renderer (or `flattenResultContent`) decide.
export type ToolResultContentBlock =
  | { type: 'text'; text: string }
  | { type: 'image'; [k: string]: unknown }
  | { type: string; [k: string]: unknown };

export type ToolResultContent = string | ToolResultContentBlock[];

export type ToolResult = {
  toolUseId: string;
  isError: boolean;
  content: ToolResultContent;
};

export type AssistantBlock =
  | { type: 'text'; text: string }
  | { type: 'thinking'; thinking: string }
  | { type: 'tool_use'; id: string; name: string; input: unknown };

export type TranscriptEvent =
  | {
      kind: 'user-prompt';
      ts?: string;
      text: string;
      raw: unknown;
      lineIndex: number;
    }
  | {
      kind: 'user-tool-result';
      ts?: string;
      results: ToolResult[];
      raw: unknown;
      lineIndex: number;
    }
  | {
      kind: 'user-meta';
      ts?: string;
      text: string;
      raw: unknown;
      lineIndex: number;
    }
  | {
      kind: 'assistant';
      ts?: string;
      usage?: TokenUsage;
      blocks: AssistantBlock[];
      raw: unknown;
      lineIndex: number;
    }
  | {
      kind: 'attachment';
      ts?: string;
      attachmentType: string;
      raw: unknown;
      lineIndex: number;
    };

export type ParseResult = {
  events: TranscriptEvent[];
  warnings: string[];
  // Raw lines kept side-by-side so "copy event range" can stitch
  // them back together without re-stringifying our parsed view.
  rawLines: string[];
};

// EvalComment (BACI-141) is the trimmed eval-comment payload the
// transcript viewer reads to render inline annotations. Mirrors the
// fields of CommentDTO the renderer actually needs: identity (uuid +
// author), body + timestamp, and the (dispatchId / agentSessionId)
// pair the filter matches against the transcript's `<dispatch_id>`
// tag. transcriptEventRef is the per-event anchor handle —
// `tool_use_id:<id>` or `line_index:<n>`; empty means the comment is
// dispatch-level anchored and pins to the dispatch prompt card.
export type EvalComment = {
  uuid: string;
  author: string;
  body: string;
  createdAt: string;
  dispatchId?: number;
  agentSessionId?: string;
  transcriptEventRef?: string;
};

// The flattened, render-ready item the viewer iterates over. Pairing
// (`pair.ts`) turns the `events` stream into a `RenderItem[]`.
//
// One assistant event with mixed `text` + `tool_use` blocks splits into
// one `assistant` item (text-only) plus one `tool-call` item per
// `tool_use`, in source order — the visible timeline matches what the
// agent emitted while pairing keeps tool_use ↔ tool_result together as
// one card.
export type RenderItem =
  | {
      kind: 'dispatch';
      id: string;
      ts?: string;
      ev: Extract<TranscriptEvent, { kind: 'user-prompt' }>;
    }
  | {
      kind: 'assistant';
      id: string;
      ts?: string;
      // Text-only block subset of the originating assistant event.
      blocks: Extract<AssistantBlock, { type: 'text' } | { type: 'thinking' }>[];
      usage?: TokenUsage;
      ev: Extract<TranscriptEvent, { kind: 'assistant' }>;
    }
  | {
      kind: 'tool-call';
      id: string;
      ts?: string;
      toolName: string;
      use: Extract<AssistantBlock, { type: 'tool_use' }>;
      result?: ToolResult;
      // The assistant event the call lives in, and the user event the
      // result lives in (if any) — both kept for the raw drawer.
      assistantEv: Extract<TranscriptEvent, { kind: 'assistant' }>;
      resultEv?: Extract<TranscriptEvent, { kind: 'user-tool-result' }>;
      // True if this is a standalone result with no matching call
      // (renders with a warning chip rather than disappearing).
      orphanedResult?: boolean;
    }
  | {
      kind: 'system-reminder';
      id: string;
      ts?: string;
      ev: Extract<TranscriptEvent, { kind: 'user-meta' }>;
    }
  | {
      kind: 'attachment';
      id: string;
      ts?: string;
      ev: Extract<TranscriptEvent, { kind: 'attachment' }>;
    };
