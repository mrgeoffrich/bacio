import { useState } from 'react';

type CommentComposerProps = {
  defaultAuthor?: string;
  onSubmit: (author: string, body: string) => void | Promise<void>;
};

// CommentComposer is the inline write surface for the workspace's
// Activity section. Author defaults to whatever the parent passes (empty
// = OS-username fallback handled by BoardService.AddComment); body
// accepts multi-line markdown that round-trips through the same path
// the legacy edit modal used. Stays available even on taken / waiting
// issues — humans leaving notes for the in-flight agent is a real flow.
export default function CommentComposer({ defaultAuthor = '', onSubmit }: CommentComposerProps) {
  const [author, setAuthor] = useState(defaultAuthor);
  const [body, setBody] = useState('');
  const [sending, setSending] = useState(false);

  const canSend = body.trim().length > 0 && !sending;

  const submit = async () => {
    if (!canSend) return;
    setSending(true);
    try {
      await onSubmit(author, body);
      setBody('');
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="mk-comment-composer">
      <input
        className="mk-edit-input"
        type="text"
        placeholder="Author (defaults to your OS username)"
        value={author}
        disabled={sending}
        onChange={(e) => setAuthor(e.target.value)}
      />
      <textarea
        className="mk-edit-textarea"
        rows={3}
        placeholder="Write a comment…"
        value={body}
        disabled={sending}
        onChange={(e) => setBody(e.target.value)}
      />
      <div className="mk-edit-actions">
        <button
          type="button"
          className="mk-btn-primary"
          disabled={!canSend}
          onClick={submit}
        >
          {sending ? 'Sending…' : 'Add comment'}
        </button>
      </div>
    </div>
  );
}
