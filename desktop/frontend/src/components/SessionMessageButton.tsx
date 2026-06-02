import type React from 'react';
import { useState } from 'react';
import * as api from '../api';

// SessionMessageButton (BACI-286) renders a "Message" button that opens
// an inline compose box and pushes a user→agent steer message at a
// running worker's session via api.sendSessionMessage. Delivery is
// turn-boundary only — the channel injects the text into the worker's
// context at its next turn; it cannot interrupt a running tool call.
//
// Shared by the Pipeline running-job card (targets card.runningSessionId)
// and the Agents-page session card (targets a.sessionId). Renders nothing
// when sessionId is empty so a card with no running worker shows no
// affordance. On a successful send the box closes; an error stays inline
// so the user can retry without losing the text.
type SessionMessageButtonProps = {
  sessionId: string;
  compact?: boolean;
};

export default function SessionMessageButton({ sessionId, compact = false }: SessionMessageButtonProps) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');

  if (!sessionId) return null;

  const close = () => {
    setOpen(false);
    setText('');
    setError('');
  };

  const send = async () => {
    const body = text.trim();
    if (!body || sending) return;
    setSending(true);
    setError('');
    try {
      await api.sendSessionMessage(sessionId, body);
      close();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Cmd/Ctrl+Enter sends; Esc cancels — same shortcuts as the rest of
    // the app's inline composers.
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      send();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  };

  if (!open) {
    return (
      <button
        type="button"
        className={`mk-btn-message${compact ? ' mk-btn-message--compact' : ''}`}
        onClick={() => setOpen(true)}
        title="Push a message to the running worker (delivered at its next turn)"
      >
        Message
      </button>
    );
  }

  return (
    <div className="mk-message-composer">
      <textarea
        className="mk-message-composer-input"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder="Message the running worker…"
        rows={3}
        autoFocus
        disabled={sending}
      />
      {error && <div className="mk-message-composer-error" title={error}>{error}</div>}
      <div className="mk-message-composer-actions">
        <button
          type="button"
          className="mk-btn-message-cancel"
          onClick={close}
          disabled={sending}
        >
          Cancel
        </button>
        <button
          type="button"
          className="mk-btn-message-send"
          onClick={send}
          disabled={sending || text.trim() === ''}
        >
          {sending ? 'Sending…' : 'Send'}
        </button>
      </div>
    </div>
  );
}
