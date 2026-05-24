// JSONL transcript parser (BACI-125).
//
// One bad line shouldn't nuke the whole render — `parse` returns a
// warnings list per malformed line and keeps going. The original line
// string for every event lives in `rawLines` so the per-event `[▶]`
// raw drawer can show what the agent actually emitted (and the
// "copy event range" affordance can stitch lines back together
// byte-identically).

import type {
  AssistantBlock,
  ParseResult,
  ToolResult,
  ToolResultContent,
  TokenUsage,
  TranscriptEvent,
} from './types';

// classifyAssistantMessage decodes the `message` envelope of an
// `assistant`-typed line. Anthropic's message shape: `content` is
// always a list of blocks; `usage` carries token counts.
function decodeAssistant(message: unknown): {
  blocks: AssistantBlock[];
  usage?: TokenUsage;
} {
  const msg = (message ?? {}) as Record<string, unknown>;
  const rawContent = msg.content;
  const blocks: AssistantBlock[] = [];
  if (Array.isArray(rawContent)) {
    for (const item of rawContent) {
      const b = item as Record<string, unknown>;
      const t = String(b?.type || '');
      if (t === 'text' && typeof b.text === 'string') {
        blocks.push({ type: 'text', text: b.text });
      } else if (t === 'thinking' && typeof b.thinking === 'string') {
        blocks.push({ type: 'thinking', thinking: b.thinking });
      } else if (t === 'tool_use') {
        blocks.push({
          type: 'tool_use',
          id: String(b.id || ''),
          name: String(b.name || ''),
          input: b.input ?? {},
        });
      }
      // Unknown block types fall through silently — they survive in
      // the `raw` payload for the [▶] drawer.
    }
  } else if (typeof rawContent === 'string') {
    blocks.push({ type: 'text', text: rawContent });
  }

  let usage: TokenUsage | undefined;
  if (msg.usage && typeof msg.usage === 'object') {
    const u = msg.usage as Record<string, unknown>;
    usage = {
      input_tokens: numberOrUndefined(u.input_tokens),
      output_tokens: numberOrUndefined(u.output_tokens),
      cache_creation_input_tokens: numberOrUndefined(
        u.cache_creation_input_tokens,
      ),
      cache_read_input_tokens: numberOrUndefined(u.cache_read_input_tokens),
    };
  }
  return { blocks, usage };
}

function numberOrUndefined(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

// decodeUserContent splits a user line into a discriminated event. A
// user message's `content` can be:
//   - a plain string (the dispatch prompt or a user reply)
//   - an array of `tool_result` blocks (paired with assistant tool_use
//     calls)
function decodeUserEvent(
  envelope: Record<string, unknown>,
  raw: unknown,
  lineIndex: number,
): TranscriptEvent | null {
  const ts = stringOrUndefined(envelope.timestamp);
  const msg = (envelope.message ?? {}) as Record<string, unknown>;
  const isMeta = envelope.isMeta === true;
  const content = msg.content;

  if (typeof content === 'string') {
    const text = content;
    if (isMeta) {
      return { kind: 'user-meta', ts, text, raw, lineIndex };
    }
    return { kind: 'user-prompt', ts, text, raw, lineIndex };
  }
  if (Array.isArray(content)) {
    const results: ToolResult[] = [];
    let metaText = '';
    let promptText = '';
    for (const item of content) {
      const b = item as Record<string, unknown>;
      const t = String(b?.type || '');
      if (t === 'tool_result') {
        results.push({
          toolUseId: String(b.tool_use_id || ''),
          isError: b.is_error === true,
          content: (b.content as ToolResultContent) ?? '',
        });
      } else if (t === 'text' && typeof b.text === 'string') {
        if (isMeta) {
          metaText += (metaText ? '\n' : '') + b.text;
        } else {
          promptText += (promptText ? '\n' : '') + b.text;
        }
      }
    }
    if (results.length > 0) {
      return { kind: 'user-tool-result', ts, results, raw, lineIndex };
    }
    if (isMeta) {
      return { kind: 'user-meta', ts, text: metaText, raw, lineIndex };
    }
    return { kind: 'user-prompt', ts, text: promptText, raw, lineIndex };
  }
  // Defensive: an empty / missing content slot still surfaces as
  // something the viewer can render — drop silently rather than
  // produce a noisy malformed warning.
  return null;
}

function stringOrUndefined(v: unknown): string | undefined {
  return typeof v === 'string' && v ? v : undefined;
}

// parse turns the raw JSONL body into a flat event list plus the
// per-line raw strings the viewer needs for the [▶] drawer / copy
// affordances.
export function parse(source: string): ParseResult {
  const events: TranscriptEvent[] = [];
  const warnings: string[] = [];
  const rawLines: string[] = [];
  // Splitting on \n keeps the same `lineIndex` as a 0-based offset
  // into the source, which matches what `String.prototype.split` would
  // produce in a text editor. We trim the trailing \r for CRLF
  // transcripts but otherwise leave the line untouched.
  const lines = source.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i].replace(/\r$/, '');
    rawLines.push(raw);
    if (!raw.trim()) continue;

    let envelope: Record<string, unknown>;
    try {
      envelope = JSON.parse(raw);
    } catch (err) {
      warnings.push(
        `Line ${i + 1} malformed: ${(err as Error).message || String(err)}`,
      );
      continue;
    }
    const ts = stringOrUndefined(envelope.timestamp);
    const type = String(envelope.type || '');
    if (type === 'attachment') {
      const att = (envelope.attachment ?? {}) as Record<string, unknown>;
      events.push({
        kind: 'attachment',
        ts,
        attachmentType: String(att.type || 'attachment'),
        raw: envelope,
        lineIndex: i,
      });
      continue;
    }
    if (type === 'assistant') {
      const { blocks, usage } = decodeAssistant(envelope.message);
      events.push({
        kind: 'assistant',
        ts,
        usage,
        blocks,
        raw: envelope,
        lineIndex: i,
      });
      continue;
    }
    if (type === 'user') {
      const ev = decodeUserEvent(envelope, envelope, i);
      if (ev) events.push(ev);
      continue;
    }
    // Unknown envelope types fall through silently — survive in the
    // rawLines list for the copy affordance, just don't surface in
    // the event stream.
  }
  return { events, warnings, rawLines };
}
