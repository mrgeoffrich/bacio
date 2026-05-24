// DispatchPromptCard renders the first user-prompt event — the bacio
// dispatch payload (BACI-125). The payload carries `<issue_id>` /
// `<mode>` / `<dispatch_id>` tags the supervisor's stub emits; we
// highlight those as chips at the top, then drop the rest of the
// prompt as monospace below so the worker's brief reads cleanly.
//
// A non-dispatch user message routes through the same component for
// rendering consistency; the chip slot is then empty.
//
// BACI-141: the card also anchors any eval comments that couldn't be
// pinned to a specific transcript event (no `transcriptEventRef` on
// the comment row) — the "dispatch-level note" fallback the brief
// calls out. The per-event composer is also mounted here so a
// reviewer can post a fresh dispatch-level note without scrolling
// down through the stream.

import React, { useState } from 'react';
import CollapsibleBody from './CollapsibleBody';
import { extractDispatchTags } from './dispatchTags';
import EvalComposer from './EvalComposer';
import EvalNotePanel from './EvalNotePanel';
import type { EvalComment } from './types';

// Re-export the helper for callers that pulled it from this module
// before BACI-125 split it out.
export { extractDispatchTags };
export type { DispatchTags } from './dispatchTags';

type DispatchPromptCardProps = {
  text: string;
  evalNotes?: EvalComment[];
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
};

export default function DispatchPromptCard({
  text,
  evalNotes,
  onPostEval,
}: DispatchPromptCardProps): React.ReactElement {
  const tags = extractDispatchTags(text);
  const [composerOpen, setComposerOpen] = useState(false);
  return (
    <div className="mk-transcript-dispatch">
      {(tags.issue || tags.mode || tags.dispatchId) && (
        <div className="mk-transcript-dispatch-tags">
          {tags.issue && (
            <span className="mk-transcript-chip is-issue">{tags.issue}</span>
          )}
          {tags.mode && (
            <span className="mk-transcript-chip is-mode">{tags.mode} mode</span>
          )}
          {tags.dispatchId && (
            <span className="mk-transcript-chip is-dispatch">
              dispatch #{tags.dispatchId}
            </span>
          )}
        </div>
      )}
      <CollapsibleBody text={text} label="prompt" linesThreshold={10}>
        <pre className="mk-transcript-pre">{text}</pre>
      </CollapsibleBody>
      {evalNotes && evalNotes.length > 0 && (
        <div className="mk-transcript-eval-notes">
          {evalNotes.map(n => (
            <EvalNotePanel key={n.uuid} note={n} />
          ))}
        </div>
      )}
      {onPostEval && (
        composerOpen ? (
          <EvalComposer
            eventRef=""
            onSubmit={onPostEval}
            onClose={() => setComposerOpen(false)}
          />
        ) : (
          <div className="mk-transcript-eval-actions">
            <button
              type="button"
              className="mk-btn-ghost mk-transcript-eval-add"
              onClick={() => setComposerOpen(true)}
            >
              + Eval the dispatch
            </button>
          </div>
        )
      )}
    </div>
  );
}
