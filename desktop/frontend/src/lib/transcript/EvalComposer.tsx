// EvalComposer is the per-event eval composer inside the transcript
// viewer (BACI-141). Mirrors the BACI-131 board composer's behaviour —
// a textarea, Cancel / Save buttons, Cmd/Ctrl+Enter to submit, Escape
// to dismiss — so the UX is consistent across both surfaces.
//
// One implementation, two mount points: <DispatchPromptCard> mounts
// it with eventRef="" (the unanchored fallback the brief calls out),
// <EventCard> mounts it with the event's tool_use_id or line_index
// anchor.

import React, { useEffect, useRef, useState } from 'react';

type EvalComposerProps = {
  eventRef: string;
  onSubmit: (body: string, eventRef: string) => Promise<void> | void;
  onClose: () => void;
};

export default function EvalComposer({
  eventRef,
  onSubmit,
  onClose,
}: EvalComposerProps): React.ReactElement {
  const [body, setBody] = useState('');
  const [sending, setSending] = useState(false);
  const ref = useRef<HTMLTextAreaElement | null>(null);
  useEffect(() => {
    ref.current?.focus();
  }, []);

  const submit = async () => {
    const trimmed = body.trim();
    if (!trimmed || sending) return;
    setSending(true);
    try {
      await onSubmit(trimmed, eventRef);
      setBody('');
      onClose();
    } catch {
      // The App-level error handler surfaces the toast; keep the
      // composer open so the user doesn't lose their typed note.
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="mk-transcript-eval-composer">
      <textarea
        ref={ref}
        className="mk-transcript-eval-composer-textarea"
        rows={3}
        placeholder="Eval note…"
        value={body}
        disabled={sending}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            submit();
          }
          if (e.key === 'Escape') {
            e.preventDefault();
            onClose();
          }
        }}
      />
      <div className="mk-transcript-eval-composer-actions">
        <button
          type="button"
          className="mk-btn-ghost"
          disabled={sending}
          onClick={onClose}
        >Cancel</button>
        <button
          type="button"
          className="mk-btn-primary"
          disabled={!body.trim() || sending}
          onClick={submit}
        >{sending ? 'Saving…' : 'Save'}</button>
      </div>
    </div>
  );
}
