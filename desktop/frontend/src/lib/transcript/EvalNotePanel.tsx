// EvalNotePanel renders one eval comment inline with the transcript
// event it's anchored to (BACI-141). Read-only — the composer for
// posting new notes lives in <EvalComposer>; this is the surface for
// the notes that already exist.
//
// Body goes through MarkdownView so eval notes render with the same
// formatting as the issue-workspace timeline. The header carries the
// author + relative time so a reader scanning the transcript can see
// at a glance who said what about which event.

import React from 'react';
import MarkdownView from '../markdownView';
import { formatTimestamp } from './format';
import type { EvalComment } from './types';

type EvalNotePanelProps = {
  note: EvalComment;
};

export default function EvalNotePanel({
  note,
}: EvalNotePanelProps): React.ReactElement {
  const ts = formatTimestamp(note.createdAt);
  return (
    <div className="mk-transcript-eval-note">
      <div className="mk-transcript-eval-note-head">
        <span className="mk-transcript-eval-note-pill" title="Eval / quality-review note">
          eval
        </span>
        <b className="mk-transcript-eval-note-author">{note.author}</b>
        {ts && (
          <span className="mk-transcript-eval-note-ts" title={note.createdAt}>
            {ts}
          </span>
        )}
      </div>
      <MarkdownView className="mk-markdown mk-transcript-eval-note-body">
        {note.body}
      </MarkdownView>
    </div>
  );
}
