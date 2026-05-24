// EvalComposer is the reusable inline composer for posting an eval
// comment from inside the transcript viewer (BACI-141). Two mount
// points: the DispatchPromptCard (eventRef === '' — pins to the
// dispatch as a whole) and per-event EventCards (eventRef ===
// `tool_use_id:<id>` or `line_index:<n>` — anchored to a specific
// event).
//
// Mirrors the BACI-131 board-card eval composer (textarea + Cancel /
// Save buttons + Cmd/Ctrl-Enter submit + Escape cancel) so the user
// gets a consistent feel across surfaces. Stays open while a submit
// is in flight so a network error doesn't lose the typed body.

import React, { useEffect, useRef, useState } from 'react';

type EvalComposerProps = {
  // The per-event anchor handle the composer pins onto the comment.
  // '' is the dispatch-level case (the unanchored fallback).
  eventRef: string;
  // The caller's submit handler — typically the parent's onPostEval
  // forwarded down through TranscriptView. Receives the trimmed body
  // and the eventRef so the parent can route the call into
  // api.addComment with the right field set.
  onSubmit: (body: string, eventRef: string) => Promise<void> | void;
  // The "trigger" the composer hides behind in its closed state.
  // Caller-supplied so the dispatch / per-event mount points can
  // size + label the button differently.
  triggerLabel?: string;
};

export default function EvalComposer({
  eventRef,
  onSubmit,
  triggerLabel = 'Eval',
}: EvalComposerProps): React.ReactElement {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState('');
  const [sending, setSending] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (open) textareaRef.current?.focus();
  }, [open]);

  const close = () => {
    setOpen(false);
    setBody('');
  };

  const submit = async () => {
    const trimmed = body.trim();
    if (!trimmed || sending) return;
    setSending(true);
    try {
      await onSubmit(trimmed, eventRef);
      close();
    } catch {
      // Parent surfaces the toast; keep the composer open so the
      // user doesn't lose their typed note.
    } finally {
      setSending(false);
    }
  };

  if (!open) {
    return (
      <div className="mk-transcript-eval-composer-trigger-row">
        <button
          type="button"
          className="mk-transcript-eval-composer-trigger"
          onClick={() => setOpen(true)}
          aria-label={`Post an eval note${eventRef ? ` anchored to this event` : ''}`}
          title={`Post an eval note${eventRef ? ' anchored to this event' : ''}`}
        >
          {triggerLabel}
        </button>
      </div>
    );
  }

  return (
    <div className="mk-transcript-eval-composer">
      <textarea
        ref={textareaRef}
        className="mk-transcript-eval-composer-textarea"
        rows={3}
        placeholder="Eval note…"
        value={body}
        disabled={sending}
        onChange={e => setBody(e.target.value)}
        onKeyDown={e => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            void submit();
          }
          if (e.key === 'Escape') {
            e.preventDefault();
            close();
          }
        }}
      />
      <div className="mk-transcript-eval-composer-actions">
        <button
          type="button"
          className="mk-btn-ghost"
          disabled={sending}
          onClick={close}
        >
          Cancel
        </button>
        <button
          type="button"
          className="mk-btn-primary"
          disabled={!body.trim() || sending}
          onClick={() => void submit()}
        >
          {sending ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}
