// EventCard is the shared per-event card chrome the transcript viewer
// uses (BACI-125). One coloured header line — role / tool name, time
// delta, optional token usage, optional ERROR / system-reminder pill,
// and the `[▶]` toggle — sits above a body slot the caller fills in.
//
// The card body is intentionally opaque to this component: the parent
// view dispatches on RenderItem kind and passes the right body in,
// then EventCard only owns the visual frame and the raw-drawer
// toggle plumbing.

import React from 'react';
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
  // BACI-141: the eval comments anchored to THIS event (one or more,
  // newest-last by the caller's iteration order). Empty / absent on
  // events with no eval notes attached. Renders under the event body
  // via <EvalNotePanel>.
  evalNotes?: EvalComment[];
  // BACI-141: when set, surface the per-event eval composer. The
  // eventRef is the durable handle for this event — `tool_use_id:<id>`
  // for tool-call events, `line_index:<n>` for everything else. The
  // submit callback is forwarded straight from TranscriptView through
  // EvalComposer (which wraps the textarea + button chrome).
  evalEventRef?: string;
  onPostEval?: (body: string, eventRef: string) => Promise<void> | void;
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
  evalEventRef,
  onPostEval,
}: EventCardProps): React.ReactElement {
  const delta = formatTimeDelta(ts, prevTs);
  const notes = evalNotes ?? [];
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
      {rawOpen && raw !== undefined && <RawEventDrawer raw={raw} />}
      {notes.length > 0 && (
        <div className="mk-transcript-eval-notes">
          {notes.map(n => (
            <EvalNotePanel key={n.uuid} note={n} />
          ))}
        </div>
      )}
      {onPostEval && evalEventRef !== undefined && (
        <EvalComposer
          eventRef={evalEventRef}
          onSubmit={onPostEval}
          triggerLabel="Eval this event"
        />
      )}
    </div>
  );
}
