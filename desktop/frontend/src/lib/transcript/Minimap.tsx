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

// EvalTick (BACI-141) is a synthetic minimap entry inserted alongside
// the per-event ticks. anchorItemIndex points into the underlying
// `items` array (the same index space the cursor uses) so the tick
// renders at the matching event's vertical position; `count` lets a
// single tick stand in for N notes anchored to the same event.
export type EvalTick = {
  // Stable key for React reconciliation. The parent uses the comment
  // uuid (or a slash-joined set of uuids for a multi-note tick).
  key: string;
  // Index into `items` the tick sits next to. Unanchored notes pass 0
  // so they pin to the dispatch prompt card.
  anchorItemIndex: number;
  // Number of notes the tick represents — drives the aria-label only,
  // the rendered glyph stays the same size regardless.
  count: number;
  // The scroll-anchor id to jump to on click. Caller-supplied because
  // an unanchored note jumps to the dispatch prompt and an anchored
  // note jumps to its EventCard.
  scrollTargetId: string;
};

type MinimapProps = {
  items: RenderItem[];
  cursorIdx: number;
  // Scroll-anchor id format the rail dereferences. Stays in
  // lockstep with the id TranscriptView passes to each EventCard.
  anchorId: (item: RenderItem) => string;
  // BACI-141: extra ticks the viewer inserts for eval notes. Empty /
  // absent on transcripts with no eval material.
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
  // BACI-141: bucket eval ticks by anchorItemIndex so the rail can
  // render the per-event tick + its companion eval glyph(s) inline.
  // Multiple notes anchored to the same event get one combined tick
  // (the per-tick `count` lights up the aria-label).
  const evalByItemIndex = new Map<number, EvalTick[]>();
  for (const tick of evalTicks ?? []) {
    const existing = evalByItemIndex.get(tick.anchorItemIndex);
    if (existing) existing.push(tick);
    else evalByItemIndex.set(tick.anchorItemIndex, [tick]);
  }
  return (
    <nav className="mk-transcript-minimap" aria-label="Transcript event minimap">
      {items.map((item, i) => {
        const ticks = evalByItemIndex.get(i) ?? [];
        return (
          <React.Fragment key={item.id}>
            <button
              type="button"
              className={`${tickClass(item)} ${i === cursorIdx ? 'is-cursor' : ''}`}
              aria-label={tickLabel(item, i)}
              title={tickLabel(item, i)}
              onClick={() => scrollTo(anchorId(item))}
            />
            {ticks.map(tick => {
              const total = tick.count;
              const label = `Eval note${total === 1 ? '' : 's'} (${total}) at #${i + 1}`;
              return (
                <button
                  key={tick.key}
                  type="button"
                  className="mk-transcript-tick is-eval"
                  aria-label={label}
                  title={label}
                  onClick={() => scrollTo(tick.scrollTargetId)}
                />
              );
            })}
          </React.Fragment>
        );
      })}
    </nav>
  );
}
