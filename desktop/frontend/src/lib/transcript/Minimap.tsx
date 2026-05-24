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

type MinimapProps = {
  items: RenderItem[];
  cursorIdx: number;
  // Scroll-anchor id format the rail dereferences. Stays in
  // lockstep with the id TranscriptView passes to each EventCard.
  anchorId: (item: RenderItem) => string;
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
}: MinimapProps): React.ReactElement {
  const scrollTo = (id: string) => {
    const el = typeof document !== 'undefined' ? document.getElementById(id) : null;
    if (el && typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  };
  return (
    <nav className="mk-transcript-minimap" aria-label="Transcript event minimap">
      {items.map((item, i) => (
        <button
          key={item.id}
          type="button"
          className={`${tickClass(item)} ${i === cursorIdx ? 'is-cursor' : ''}`}
          aria-label={tickLabel(item, i)}
          title={tickLabel(item, i)}
          onClick={() => scrollTo(anchorId(item))}
        />
      ))}
    </nav>
  );
}
