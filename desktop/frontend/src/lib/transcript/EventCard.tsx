// EventCard is the shared per-event card chrome the transcript viewer
// uses (BACI-125). One coloured header line — role / tool name, time
// delta, optional token usage, optional ERROR / system-reminder pill,
// and the `[▶]` toggle — sits above a body slot the caller fills in.
//
// The card body is intentionally opaque to this component: the parent
// view dispatches on RenderItem kind and passes the right body in,
// then EventCard only owns the visual frame and the raw-drawer
// toggle plumbing.

import React, { useState } from 'react';
import EvalComposer from './EvalComposer';
import EvalNotePanel from './EvalNotePanel';
import RawEventDrawer from './RawEventDrawer';
import { formatTimeDelta } from './format';
import type { EvalComment } from './types';

type EventCardProps = {
  // The card's id — used to wire the raw drawer open state into
  // TranscriptView's `rawOpenIds` map (caller-owned).
  id: string;
  // CSS modifier classes for the card colour (`is-user` / `is-assistant`
  // / `is-tool` / `is-error`).
  modifiers?: string;
  // Mini-map / scroll target. The Minimap clicks
  // `getElementById('tr-evt-' + id)`.
  scrollAnchorId?: string;
  // Header content. Composed of a few canonical pieces — see below.
  headLeft: React.ReactNode;
  headMiddle?: React.ReactNode;
  headRight?: React.ReactNode;
  pill?: React.ReactNode;
  ts?: string;
  prevTs?: string;
  // Body content the parent renders.
  children: React.ReactNode;
  // Raw envelope for the `[▶]` drawer. When omitted the toggle hides.
  raw?: unknown;
  rawOpen?: boolean;
  onToggleRaw?: () => void;
  // BACI-141: eval notes already posted against this event (one or
  // more rows whose `transcriptEventRef` matches this card's anchor)
  // and the per-event composer's submit hook + the anchor string the
  // composer should pin a fresh note to. Both optional — passing
  // neither keeps the card a pure renderer.
  evalNotes?: EvalComment[];
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
  evalEventRef?: string;
};

export default function EventCard({
  modifiers = '',
  scrollAnchorId,
  headLeft,
  headMiddle,
  headRight,
  pill,
  ts,
  prevTs,
  children,
  raw,
  rawOpen,
  onToggleRaw,
  evalNotes,
  onPostEval,
  evalEventRef,
}: EventCardProps): React.ReactElement {
  const delta = formatTimeDelta(ts, prevTs);
  const [composerOpen, setComposerOpen] = useState(false);
  const canCompose = !!(onPostEval && evalEventRef);
  return (
    <div
      id={scrollAnchorId}
      className={`mk-transcript-event ${modifiers}`}
    >
      <div className="mk-transcript-event-head">
        <span className="mk-transcript-event-role">{headLeft}</span>
        {delta && <span className="mk-transcript-event-delta">{delta}</span>}
        {headMiddle && (
          <span className="mk-transcript-event-middle">{headMiddle}</span>
        )}
        {pill && <span className="mk-transcript-event-pill">{pill}</span>}
        <span className="mk-transcript-event-spacer" />
        {headRight && (
          <span className="mk-transcript-event-right">{headRight}</span>
        )}
        {canCompose && (
          <button
            type="button"
            className="mk-transcript-eval-toggle"
            onClick={() => setComposerOpen(o => !o)}
            aria-expanded={composerOpen}
            aria-label="Eval this event"
            title="Add an eval note pinned to this event"
          >
            Eval
          </button>
        )}
        {raw !== undefined && onToggleRaw && (
          <button
            type="button"
            className={`mk-transcript-raw-toggle ${rawOpen ? 'is-open' : ''}`}
            onClick={onToggleRaw}
            aria-expanded={rawOpen}
            aria-label="Toggle raw event JSON"
            title="Show raw event JSON"
          >
            {rawOpen ? '▾' : '▸'} Raw
          </button>
        )}
      </div>
      <div className="mk-transcript-event-body">{children}</div>
      {evalNotes && evalNotes.length > 0 && (
        <div className="mk-transcript-eval-notes">
          {evalNotes.map(n => (
            <EvalNotePanel key={n.uuid} note={n} />
          ))}
        </div>
      )}
      {composerOpen && canCompose && (
        <EvalComposer
          eventRef={evalEventRef!}
          onSubmit={onPostEval!}
          onClose={() => setComposerOpen(false)}
        />
      )}
      {rawOpen && raw !== undefined && <RawEventDrawer raw={raw} />}
    </div>
  );
}
