// Minimap is the sticky left rail of one-tick-per-event ticks
// (BACI-125). Each tick is a real <button> so a keyboard user can
// navigate via Tab; the aria-label carries the tool name + index +
// error state so a screen reader caller knows what they're jumping
// to. Clicking scrolls the matching event card into view.
//
// The IntersectionObserver-driven cursor highlight lives one level up
// in TranscriptView (passing `cursorIdx`) so all event cards share
// one observer rather than the rail creating its own.

import React from 'react';
import type { RenderItem } from './types';

// EvalTick (BACI-141) is one synthetic eval-comment marker on the
// rail. TranscriptView builds these by walking the filtered eval
// comments and resolving each one's anchor (tool_use_id or
// line_index) to an item index; unanchored notes anchor to the
// dispatch prompt card at index 0. Each tick is its own clickable
// button on the rail so a reviewer can jump straight to the matching
// <EvalNotePanel>.
export type EvalTick = {
  anchorIndex: number;
  label: string;
  anchorId: string;
};

type MinimapProps = {
  items: RenderItem[];
  cursorIdx: number;
  // Scroll-anchor id format the rail dereferences. Stays in
  // lockstep with the id TranscriptView passes to each EventCard.
  anchorId: (item: RenderItem) => string;
  evalTicks?: EvalTick[];
};

function tickClass(item: RenderItem): string {
  const base = 'mk-transcript-tick';
  let kindCls = '';
  let errorCls = '';
  switch (item.kind) {
    case 'dispatch':
      kindCls = 'is-dispatch';
      break;
    case 'assistant':
      kindCls = 'is-assistant';
      break;
    case 'tool-call':
      kindCls = 'is-tool';
      if (item.result?.isError || item.orphanedResult) errorCls = 'is-error';
      break;
    case 'system-reminder':
      kindCls = 'is-sysmeta';
      break;
    case 'attachment':
      kindCls = 'is-attachment';
      break;
  }
  return [base, kindCls, errorCls].filter(Boolean).join(' ');
}

function tickLabel(item: RenderItem, idx: number): string {
  switch (item.kind) {
    case 'dispatch':
      return `Dispatch prompt · #${idx + 1}`;
    case 'assistant':
      return `Assistant · #${idx + 1}`;
    case 'tool-call':
      return `${item.toolName || 'tool'} · #${idx + 1}${
        item.result?.isError ? ' · error' : ''
      }`;
    case 'system-reminder':
      return `System reminder · #${idx + 1}`;
    case 'attachment':
      return `Attachment · #${idx + 1}`;
  }
}

export default function Minimap({
  items,
  cursorIdx,
  anchorId,
  evalTicks,
}: MinimapProps): React.ReactElement {
  const scrollTo = (id: string) => {
    const el = typeof document !== 'undefined' ? document.getElementById(id) : null;
    if (el && typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  };
  // Group eval ticks by their anchor item index so the rail can
  // render them inline next to the matching item tick. Multiple
  // notes on the same event collapse to a single visible mark with
  // a count in the label.
  const evalByIndex = new Map<number, EvalTick[]>();
  for (const t of evalTicks || []) {
    const list = evalByIndex.get(t.anchorIndex);
    if (list) list.push(t);
    else evalByIndex.set(t.anchorIndex, [t]);
  }
  return (
    <nav className="mk-transcript-minimap" aria-label="Transcript event minimap">
      {items.map((item, i) => {
        const evals = evalByIndex.get(i) || [];
        const evalLabel = evals.length > 0
          ? `${evals.length} eval note${evals.length === 1 ? '' : 's'} — ${evals[0].label}`
          : '';
        return (
          <span key={item.id} className="mk-transcript-tick-row">
            <button
              type="button"
              className={`${tickClass(item)} ${i === cursorIdx ? 'is-cursor' : ''}`}
              aria-label={tickLabel(item, i)}
              title={tickLabel(item, i)}
              onClick={() => scrollTo(anchorId(item))}
            />
            {evals.length > 0 && (
              <button
                type="button"
                className="mk-transcript-tick is-eval"
                aria-label={evalLabel}
                title={evalLabel}
                onClick={() => scrollTo(evals[0].anchorId)}
              />
            )}
          </span>
        );
      })}
    </nav>
  );
}
