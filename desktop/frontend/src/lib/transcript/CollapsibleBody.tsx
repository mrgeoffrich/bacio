// CollapsibleBody is the shared "no truncation" helper every long-body
// renderer in the transcript viewer uses (BACI-125). The design goal
// the ticket calls out: long bodies must remain reachable — collapse
// by default with a visible line count and a "show all" affordance,
// never an `…` ellipsis or a silent cut.
//
// Counts visible lines of the raw text and, if past the threshold,
// hides the body behind a `▼ <label> · N lines` button. Clicking
// expands the entire body — every line is in the DOM after expansion.
//
// Persistence of open/close state is the caller's job; pass the
// optional `id` + `isOpen` + `onToggle` props to tie the state to the
// shared `expanded` map on `TranscriptView`. Without those props the
// component manages its own local state.

import React, { useState } from 'react';
import { countLines } from './format';

type CollapsibleBodyProps = {
  // The raw text to count for the "N lines" header. The visible body
  // (`children`) is what the renderer ultimately wants on screen — it
  // may be the same string wrapped in a <pre>, or it may be JSX for a
  // bespoke per-tool renderer.
  text: string;
  // The renderable body. A function form is supported so callers can
  // skip building the JSX until expansion (saves work on the largest
  // tool outputs, where the body might be 500 KB of fenced code).
  children: React.ReactNode | (() => React.ReactNode);
  // Label rendered after the disclosure arrow — "result" / "source" /
  // "input" / etc. Defaults to "body".
  label?: string;
  // Threshold above which the body collapses by default. Renderers
  // pick a value per content type.
  linesThreshold?: number;
  // Optional caller-owned open state. Without it the component owns
  // its own toggle.
  isOpen?: boolean;
  onToggle?: (open: boolean) => void;
};

export default function CollapsibleBody({
  text,
  children,
  label = 'body',
  linesThreshold = 12,
  isOpen,
  onToggle,
}: CollapsibleBodyProps): React.ReactElement {
  const [localOpen, setLocalOpen] = useState(false);
  const lines = countLines(text);
  const collapsible = lines > linesThreshold;
  const open = collapsible ? (isOpen ?? localOpen) : true;

  const renderChildren = () =>
    typeof children === 'function' ? (children as () => React.ReactNode)() : children;

  if (!collapsible) {
    return <div className="mk-transcript-body-inner">{renderChildren()}</div>;
  }

  const toggle = () => {
    const next = !open;
    if (onToggle) onToggle(next);
    else setLocalOpen(next);
  };

  return (
    <div className="mk-transcript-collapse-wrap">
      <button
        type="button"
        className={`mk-transcript-collapse ${open ? 'is-open' : ''}`}
        onClick={toggle}
        aria-expanded={open}
      >
        <span className="mk-transcript-collapse-arrow" aria-hidden="true">
          {open ? '▾' : '▸'}
        </span>
        <span className="mk-transcript-collapse-label">{label}</span>
        <span className="mk-transcript-collapse-count">· {lines} lines</span>
      </button>
      {open && <div className="mk-transcript-body-inner">{renderChildren()}</div>}
    </div>
  );
}
