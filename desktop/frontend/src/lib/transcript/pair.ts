// pair turns the flat event stream from `parse` into the visible
// RenderItem list (BACI-125):
//
//   * The first `user-prompt` event is the dispatch prompt — always
//     surfaced as its own DispatchPromptCard.
//   * An assistant event with mixed `text` and `tool_use` blocks splits
//     into one `assistant` item (text-only blocks) plus one `tool-call`
//     item per `tool_use`, in source order. The visible timeline matches
//     what the agent emitted while pairing keeps each tool_use ↔
//     tool_result together as one card.
//   * A `user-tool-result` event whose results have all been paired is
//     dropped from the visible stream (still reachable via the raw
//     drawer of its paired call). A leftover result with no matching
//     call surfaces as a standalone `tool-call` item with
//     `orphanedResult: true` so it doesn't silently disappear.
//   * `user-meta` (system reminders) and `attachment` events keep their
//     own item kinds — the controls strip hides attachments by default.

import type { RenderItem, ToolResult, TranscriptEvent } from './types';

export function pair(events: TranscriptEvent[]): RenderItem[] {
  // Index every tool_result by toolUseId so the walker can fold them
  // onto the matching assistant `tool_use` block in O(N).
  const resultByCallId = new Map<
    string,
    {
      result: ToolResult;
      ev: Extract<TranscriptEvent, { kind: 'user-tool-result' }>;
    }
  >();
  for (const ev of events) {
    if (ev.kind !== 'user-tool-result') continue;
    for (const r of ev.results) {
      if (!resultByCallId.has(r.toolUseId)) {
        resultByCallId.set(r.toolUseId, { result: r, ev });
      }
    }
  }
  const consumedResultIds = new Set<string>();

  const out: RenderItem[] = [];
  let dispatchSeen = false;
  let counter = 0;
  const nextId = (kind: string, line: number) =>
    `tr-${kind}-${line}-${counter++}`;

  for (const ev of events) {
    if (ev.kind === 'user-prompt') {
      if (!dispatchSeen) {
        dispatchSeen = true;
        out.push({
          kind: 'dispatch',
          id: nextId('dispatch', ev.lineIndex),
          ts: ev.ts,
          ev,
        });
      } else {
        // A subsequent string-content user message — render as a
        // dispatch-like card so it's still readable (rare in
        // dispatched-subagent transcripts but possible).
        out.push({
          kind: 'dispatch',
          id: nextId('user', ev.lineIndex),
          ts: ev.ts,
          ev,
        });
      }
      continue;
    }
    if (ev.kind === 'user-meta') {
      out.push({
        kind: 'system-reminder',
        id: nextId('meta', ev.lineIndex),
        ts: ev.ts,
        ev,
      });
      continue;
    }
    if (ev.kind === 'attachment') {
      out.push({
        kind: 'attachment',
        id: nextId('att', ev.lineIndex),
        ts: ev.ts,
        ev,
      });
      continue;
    }
    if (ev.kind === 'assistant') {
      const textBlocks = ev.blocks.filter(
        b => b.type === 'text' || b.type === 'thinking',
      ) as Extract<typeof ev.blocks[number], { type: 'text' } | { type: 'thinking' }>[];
      // Emit one assistant item with the text/thinking blocks (if any
      // are non-empty), then one tool-call item per tool_use in source
      // order.
      const hasText = textBlocks.some(
        b =>
          (b.type === 'text' && b.text.trim() !== '') ||
          (b.type === 'thinking' && b.thinking.trim() !== ''),
      );
      if (hasText) {
        out.push({
          kind: 'assistant',
          id: nextId('asst', ev.lineIndex),
          ts: ev.ts,
          blocks: textBlocks,
          usage: ev.usage,
          ev,
        });
      }
      for (const b of ev.blocks) {
        if (b.type !== 'tool_use') continue;
        const paired = resultByCallId.get(b.id);
        if (paired) {
          consumedResultIds.add(b.id);
          out.push({
            kind: 'tool-call',
            id: nextId('tool', ev.lineIndex),
            ts: ev.ts,
            toolName: b.name,
            use: b,
            result: paired.result,
            assistantEv: ev,
            resultEv: paired.ev,
          });
        } else {
          out.push({
            kind: 'tool-call',
            id: nextId('tool', ev.lineIndex),
            ts: ev.ts,
            toolName: b.name,
            use: b,
            assistantEv: ev,
          });
        }
      }
      continue;
    }
    if (ev.kind === 'user-tool-result') {
      // Hide this event if every result it carried was consumed by a
      // call upstream. Any leftover surfaces as a standalone item
      // tagged orphaned.
      const leftover = ev.results.filter(r => !consumedResultIds.has(r.toolUseId));
      if (leftover.length === 0) continue;
      for (const r of leftover) {
        out.push({
          kind: 'tool-call',
          id: nextId('orphan', ev.lineIndex),
          ts: ev.ts,
          toolName: '(orphan tool_result)',
          use: { type: 'tool_use', id: r.toolUseId, name: '', input: {} },
          result: r,
          // Synthetic assistantEv: we point at this user event so the
          // [▶] drawer still works; downstream rendering tolerates the
          // assistantEv being a user-tool-result envelope.
          assistantEv: ev as unknown as Extract<
            TranscriptEvent,
            { kind: 'assistant' }
          >,
          resultEv: ev,
          orphanedResult: true,
        });
      }
      continue;
    }
  }
  return out;
}
