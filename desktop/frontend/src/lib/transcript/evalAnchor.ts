// BACI-141 anchor helpers, factored out of TranscriptView.tsx so the
// non-JSX logic can be unit-tested directly via the smoketest runner
// (Node's TS strip-types loader handles plain .ts but not JSX).

import type { RenderItem } from './types';

// resolveAnchor resolves a comment's `transcript_event_ref` to a
// RenderItem in `items` — either by matching `tool_use_id:<id>`
// against a tool-call item's `use.id`, or `line_index:<n>` against
// any item's originating event lineIndex. Returns null on any
// unrecognised ref shape or unresolved id, so the caller can fall
// through to the unanchored bucket without silently dropping the note.
export function resolveAnchor(items: RenderItem[], ref: string): RenderItem | null {
  if (!ref) return null;
  if (ref.startsWith('tool_use_id:')) {
    const id = ref.slice('tool_use_id:'.length);
    for (const it of items) {
      if (it.kind === 'tool-call' && it.use.id === id) return it;
    }
    return null;
  }
  if (ref.startsWith('line_index:')) {
    const n = Number(ref.slice('line_index:'.length));
    if (!Number.isFinite(n)) return null;
    for (const it of items) {
      const li = itemLineIndex(it);
      if (li === n) return it;
    }
    return null;
  }
  return null;
}

// evalEventRefFor returns the per-event anchor the composer should
// pin a fresh note to. Tool-call cards prefer their `tool_use_id`
// (durable across re-renders — the same id appears on the
// assistant tool_use event and the matching user-tool-result
// event); everything else falls back to the event's `line_index`.
// Returns empty when nothing usable is present (the comment should
// then be posted unanchored — pinned to the dispatch prompt card).
export function evalEventRefFor(item: RenderItem): string {
  if (item.kind === 'tool-call' && item.use.id) {
    return `tool_use_id:${item.use.id}`;
  }
  const li = itemLineIndex(item);
  return typeof li === 'number' ? `line_index:${li}` : '';
}

function itemLineIndex(item: RenderItem): number | undefined {
  switch (item.kind) {
    case 'dispatch': return item.ev.lineIndex;
    case 'assistant': return item.ev.lineIndex;
    case 'tool-call': return item.assistantEv.lineIndex;
    case 'system-reminder': return item.ev.lineIndex;
    case 'attachment': return item.ev.lineIndex;
  }
}
