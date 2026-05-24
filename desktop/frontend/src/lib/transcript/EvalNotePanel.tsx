// EvalNotePanel renders one eval comment inline with the transcript
// it was posted against (BACI-141). One panel per comment; sits below
// the matching <EventCard> body for anchored notes, or below the
// <DispatchPromptCard> prompt for unanchored ones.
//
// Read-only — the per-event composer is its own component
// (<EvalComposer>) so this surface stays focused on review.

import React from 'react';
import MarkdownView from '../markdownView';
import type { EvalComment } from './types';

type EvalNotePanelProps = {
  note: EvalComment;
};

function formatTs(ts: string): string {
  try {
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  } catch {
    return ts;
  }
}

export default function EvalNotePanel({
  note,
}: EvalNotePanelProps): React.ReactElement {
  return (
    <div className="mk-transcript-eval-note" role="note">
      <div className="mk-transcript-eval-note-head">
        <span className="mk-transcript-eval-note-pill">eval</span>
        <b className="mk-transcript-eval-note-author">{note.author}</b>
        <span className="mk-transcript-eval-note-spacer" />
        {note.createdAt && (
          <span className="mk-transcript-eval-note-time" title={note.createdAt}>
            {formatTs(note.createdAt)}
          </span>
        )}
      </div>
      <MarkdownView className="mk-markdown mk-transcript-eval-note-body">
        {note.body}
      </MarkdownView>
    </div>
  );
}
