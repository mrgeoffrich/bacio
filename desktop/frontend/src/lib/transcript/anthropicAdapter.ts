// Adapter from the proxy-captured Anthropic transcript shape to the
// transcript viewer's TranscriptEvent[] (BACI-308).
//
// The BACI-306 `/proxy/jobs/{id}/transcript` read returns a parsed
// `AnthropicTranscript` — the assembled primary-thread conversation
// (messages[]), the summed token usage, and the auxiliary turns. The viewer
// (TranscriptView / pair / EventCard) was built for Claude Code `.jsonl`
// transcripts and consumes a post-`parse()` TranscriptEvent[]. The model JSON
// tags were deliberately snake_case-aligned to the viewer's TS types, so the
// gap is purely structural: this adapter walks messages[] into the viewer's
// event kinds (user-prompt / user-tool-result / assistant) and appends the
// auxiliary turns as trailing assistant events. The card renderers, minimap,
// filters, and token totals are reused wholesale — this adapter is the only
// new render code the Monitor drill-down needs.

import type {
  AssistantBlock,
  ParseResult,
  ToolResult,
  ToolResultContent,
  TokenUsage,
  TranscriptEvent,
} from './types';

// The captured shape, mirrored from the Wails-generated
// model.AnthropicTranscript / AnthropicMessage / AnthropicBlock / AnthropicTurn
// (snake_case). Declared here as a plain structural type so the adapter
// doesn't depend on the generated binding classes (the web transport hands
// back the same snake_case JSON shape over REST), matching how the viewer's
// own types.ts owns its shapes outright.
export interface AnthropicBlockJSON {
  type: string;
  text?: string;
  thinking?: string;
  id?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  content?: unknown;
  is_error?: boolean;
}

export interface AnthropicMessageJSON {
  role: string;
  content?: AnthropicBlockJSON[];
}

export interface AnthropicUsageJSON {
  input_tokens?: number;
  output_tokens?: number;
  cache_creation_input_tokens?: number;
  cache_read_input_tokens?: number;
  thinking_tokens?: number;
}

export interface AnthropicTurnJSON {
  model?: string;
  blocks?: AnthropicBlockJSON[];
  usage?: AnthropicUsageJSON;
  stop_reason?: string;
}

export interface AnthropicTranscriptJSON {
  dispatch_id?: number | null;
  model?: string;
  messages?: AnthropicMessageJSON[];
  usage?: AnthropicUsageJSON;
  auxiliary?: AnthropicTurnJSON[];
}

// usageFrom narrows the merged usage shape down to the viewer's TokenUsage
// (same field names; thinking_tokens isn't a TokenUsage field so it's dropped
// for the per-turn badge, consistent with the .jsonl path).
function usageFrom(u: AnthropicUsageJSON | undefined): TokenUsage | undefined {
  if (!u) return undefined;
  return {
    input_tokens: u.input_tokens,
    output_tokens: u.output_tokens,
    cache_creation_input_tokens: u.cache_creation_input_tokens,
    cache_read_input_tokens: u.cache_read_input_tokens,
  };
}

// assistantBlocksFrom maps the captured content blocks to the viewer's
// AssistantBlock union (text | thinking | tool_use). Unknown block types are
// dropped from the rendered blocks but survive in the event's `raw` payload
// for the [▶] drawer — same posture as the .jsonl decoder.
function assistantBlocksFrom(blocks: AnthropicBlockJSON[]): AssistantBlock[] {
  const out: AssistantBlock[] = [];
  for (const b of blocks) {
    if (b.type === 'text' && typeof b.text === 'string') {
      out.push({ type: 'text', text: b.text });
    } else if (b.type === 'thinking' && typeof b.thinking === 'string') {
      out.push({ type: 'thinking', thinking: b.thinking });
    } else if (b.type === 'tool_use') {
      out.push({
        type: 'tool_use',
        id: String(b.id ?? ''),
        name: String(b.name ?? ''),
        input: b.input ?? {},
      });
    }
  }
  return out;
}

// toolResultsFrom maps a user message's tool_result blocks to the viewer's
// ToolResult[]. The captured `content` is polymorphic (string | block list),
// which the viewer's ToolResultContent union already encodes — passed through
// verbatim so the per-tool renderer / flattenResultContent decides.
function toolResultsFrom(blocks: AnthropicBlockJSON[]): ToolResult[] {
  const out: ToolResult[] = [];
  for (const b of blocks) {
    if (b.type !== 'tool_result') continue;
    out.push({
      toolUseId: String(b.tool_use_id ?? ''),
      isError: !!b.is_error,
      content: (b.content ?? '') as ToolResultContent,
    });
  }
  return out;
}

// userTextFrom joins a user message's text blocks into one prompt string
// (matching the .jsonl path where a user turn is rendered as a single prompt
// card).
function userTextFrom(blocks: AnthropicBlockJSON[]): string {
  return blocks
    .filter(b => b.type === 'text' && typeof b.text === 'string')
    .map(b => b.text as string)
    .join('\n\n');
}

// anthropicTranscriptToEvents walks a captured AnthropicTranscript into the
// viewer's TranscriptEvent[]:
//   - a user message → a user-prompt event for its text blocks AND/OR a
//     user-tool-result event for its tool_result blocks (a single user turn
//     can carry both — the tool_result reply plus a fresh prompt);
//   - an assistant message → an assistant event (text / thinking / tool_use);
//   - each auxiliary turn → a trailing assistant event carrying its own usage
//     (title-gen / structured-output probes, kept out of the primary thread).
// lineIndex is the running event index (durable for the viewer's anchoring /
// minimap), and `raw` stashes the source object for the [▶] drawer.
export function anthropicTranscriptToEvents(
  tr: AnthropicTranscriptJSON,
): TranscriptEvent[] {
  const events: TranscriptEvent[] = [];
  const push = (
    make: (lineIndex: number) => TranscriptEvent,
  ) => {
    events.push(make(events.length));
  };

  for (const msg of tr.messages ?? []) {
    const blocks = msg.content ?? [];
    if (msg.role === 'user') {
      const results = toolResultsFrom(blocks);
      if (results.length > 0) {
        push(lineIndex => ({
          kind: 'user-tool-result',
          results,
          raw: msg,
          lineIndex,
        }));
      }
      const text = userTextFrom(blocks);
      if (text) {
        push(lineIndex => ({
          kind: 'user-prompt',
          text,
          raw: msg,
          lineIndex,
        }));
      }
    } else if (msg.role === 'assistant') {
      push(lineIndex => ({
        kind: 'assistant',
        blocks: assistantBlocksFrom(blocks),
        raw: msg,
        lineIndex,
      }));
    }
    // An unrecognised role is dropped from the rendered stream (it has no
    // viewer kind); the assembled transcript only ever carries user/assistant.
  }

  for (const turn of tr.auxiliary ?? []) {
    push(lineIndex => ({
      kind: 'assistant',
      usage: usageFrom(turn.usage),
      blocks: assistantBlocksFrom(turn.blocks ?? []),
      raw: turn,
      lineIndex,
    }));
  }

  return events;
}

// anthropicTranscriptToParseResult wraps the adapted events in a ParseResult so
// TranscriptView can consume the captured transcript through the same seam as a
// .jsonl source. There are no per-line parse warnings (the capture was already
// parsed server-side), and rawLines is the per-event source object stringified
// so the "copy all" affordance still produces a faithful dump.
export function anthropicTranscriptToParseResult(
  tr: AnthropicTranscriptJSON,
): ParseResult {
  const events = anthropicTranscriptToEvents(tr);
  return {
    events,
    warnings: [],
    rawLines: events.map(e => JSON.stringify(e.raw)),
  };
}
