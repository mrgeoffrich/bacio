import React, { useState, useEffect, useCallback } from 'react';
import Modal from './Modal.jsx';
import { subscribeErrors } from '../errors';

const DEV = !!(import.meta && import.meta.env && import.meta.env.DEV);
const MAX_MESSAGE_CHARS = 500;

// ErrorModal is mounted once at the App root. It subscribes to the
// error bus and renders one error at a time in a centred modal,
// queueing the rest. A repeated error (same headline + message,
// within the coalesce window in errors.ts) bumps an existing entry's
// count instead of pushing a fresh one — so a flapping poll loop
// shows as "Couldn't refresh board (×7)", not 7 modals.
export default function ErrorModal() {
  const [queue, setQueue] = useState([]);
  const [showDetail, setShowDetail] = useState(false);
  const [showMore, setShowMore] = useState(false);

  const dismiss = useCallback(() => {
    setQueue(prev => prev.slice(1));
    setShowDetail(false);
    setShowMore(false);
  }, []);

  useEffect(() => {
    return subscribeErrors(entry => {
      setQueue(prev => {
        const idx = prev.findIndex(e => e.id === entry.id);
        if (idx === -1) return [...prev, entry];
        // Coalesced update — replace in place so the count refreshes.
        const next = prev.slice();
        next[idx] = entry;
        return next;
      });
    });
  }, []);

  const current = queue[0];
  if (!current) return null;

  const remaining = queue.length - 1;
  const tooLong = current.message.length > MAX_MESSAGE_CHARS;
  const body = !tooLong || showMore
    ? current.message
    : current.message.slice(0, MAX_MESSAGE_CHARS) + '…';

  return (
    <Modal
      open
      onClose={dismiss}
      variant="error"
      preventClickOutsideClose
      title={
        <>
          {current.headline}
          {current.count > 1 && (
            <span className="mk-error-modal-count"> (×{current.count})</span>
          )}
        </>
      }
    >
      <p className="mk-error-modal-body">{body}</p>
      {tooLong && (
        <button
          type="button"
          className="mk-error-modal-link"
          onClick={() => setShowMore(s => !s)}
        >
          {showMore ? 'Show less' : 'Show more'}
        </button>
      )}
      {DEV && current.detail && (
        <details
          className="mk-error-modal-details"
          open={showDetail}
          onToggle={(e) => setShowDetail(e.currentTarget.open)}
        >
          <summary>Details (dev only)</summary>
          <pre className="mk-error-modal-stack">{current.detail}</pre>
        </details>
      )}
      {remaining > 0 && (
        <div className="mk-error-modal-more">
          {remaining} more {remaining === 1 ? 'error' : 'errors'} queued
        </div>
      )}
      <div className="mk-modal-actions">
        <button
          className="mk-segmented-btn is-active"
          onClick={dismiss}
        >
          Dismiss
        </button>
      </div>
    </Modal>
  );
}
