import { useEffect, useRef, useState } from 'react';

// InlineTitleEditor renders the issue title as a click-to-edit field in
// the workspace header (BACI-292). Read mode is the same <h1> the header
// always showed; clicking it swaps in a single-line <input> seeded from
// the current title. Enter or blur commits via onSave(next); Esc reverts.
// Empty/whitespace-only titles are rejected client-side (the store rejects
// them too — handleIssueEdit returns invalid_input on an empty title), so
// a blank commit just reverts to the last good value.
//
// readOnly (taken/waiting issues) renders the plain <h1> with no
// click-to-edit affordance — same gate the description editor uses.
type InlineTitleEditorProps = {
  title: string;
  readOnly: boolean;
  onSave: (next: string) => void | Promise<void>;
};

export default function InlineTitleEditor({ title, readOnly, onSave }: InlineTitleEditorProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title ?? '');
  const [saving, setSaving] = useState(false);
  // Guards the double-commit when pressing Enter (which also blurs the
  // input): the keydown handler commits and the resulting blur must not
  // fire onSave a second time.
  const committedRef = useRef(false);

  // Resync the draft to a fresh title whenever it changes and we're not
  // mid-edit — a poll-driven re-render must not stomp the user's buffer.
  useEffect(() => {
    if (!editing) setDraft(title ?? '');
  }, [title, editing]);

  const startEdit = () => {
    if (readOnly) return;
    setDraft(title ?? '');
    committedRef.current = false;
    setEditing(true);
  };

  const cancelEdit = () => {
    setDraft(title ?? '');
    setEditing(false);
  };

  const commitSave = async () => {
    if (committedRef.current) return;
    committedRef.current = true;
    const next = draft.trim();
    // Empty or unchanged → revert without a round-trip.
    if (!next || next === (title ?? '')) {
      cancelEdit();
      return;
    }
    setSaving(true);
    try {
      await onSave(next);
      setEditing(false);
    } catch {
      // onSave reports its own error; stay in edit mode so the user can
      // retry or Esc out without losing the buffer.
      committedRef.current = false;
    } finally {
      setSaving(false);
    }
  };

  if (readOnly || !editing) {
    return (
      <h1
        className="mk-workspace-title"
        {...(readOnly
          ? {}
          : {
              role: 'button',
              tabIndex: 0,
              title: 'Click to edit title',
              onClick: startEdit,
              onKeyDown: (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  startEdit();
                }
              },
            })}
      >
        {title}
      </h1>
    );
  }

  return (
    <input
      className="mk-edit-input mk-workspace-title-input"
      type="text"
      value={draft}
      disabled={saving}
      autoFocus
      aria-label="Edit title"
      onFocus={(e) => e.target.select()}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commitSave}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          commitSave();
        } else if (e.key === 'Escape') {
          e.preventDefault();
          cancelEdit();
        }
      }}
    />
  );
}
