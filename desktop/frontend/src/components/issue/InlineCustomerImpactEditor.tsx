import { useEffect, useRef, useState } from 'react';

// InlineCustomerImpactEditor (BACI-349) renders the issue's one-line
// customer impact as a click-to-edit field in the workspace header,
// directly below the title. It mirrors InlineTitleEditor but with two
// deliberate differences:
//
//   - Empty is a first-class value (the "no user-facing change" state),
//     so a blank commit CLEARS the field rather than reverting — and the
//     read view, when blank, shows a muted "+ Add customer impact" prompt
//     so there's still an edit affordance.
//   - The read view renders a small subtitle line (not an <h1>), so it
//     reads as secondary to the title.
//
// readOnly (taken/waiting issues) renders the plain line with no
// click-to-edit affordance — same gate as the title / description editors.
type InlineCustomerImpactEditorProps = {
  customerImpact: string;
  readOnly: boolean;
  onSave: (next: string) => void | Promise<void>;
};

export default function InlineCustomerImpactEditor({
  customerImpact,
  readOnly,
  onSave,
}: InlineCustomerImpactEditorProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(customerImpact ?? '');
  const [saving, setSaving] = useState(false);
  // Guards the double-commit when pressing Enter (which also blurs): the
  // keydown handler commits and the resulting blur must not re-fire onSave.
  const committedRef = useRef(false);

  // Resync the draft to a fresh value whenever it changes and we're not
  // mid-edit — a poll-driven re-render must not stomp the user's buffer.
  useEffect(() => {
    if (!editing) setDraft(customerImpact ?? '');
  }, [customerImpact, editing]);

  const startEdit = () => {
    if (readOnly) return;
    setDraft(customerImpact ?? '');
    committedRef.current = false;
    setEditing(true);
  };

  const cancelEdit = () => {
    setDraft(customerImpact ?? '');
    setEditing(false);
  };

  const commitSave = async () => {
    if (committedRef.current) return;
    committedRef.current = true;
    const next = draft.trim();
    // Unchanged → revert without a round-trip. Unlike the title, an empty
    // value is a legitimate commit (clears the field), so only an
    // identical value short-circuits.
    if (next === (customerImpact ?? '')) {
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
    const hasImpact = !!customerImpact;
    // Read-only with no impact renders nothing — no point dangling an
    // empty placeholder the user can't act on.
    if (readOnly && !hasImpact) return null;
    return (
      <p
        className={`mk-workspace-impact${hasImpact ? '' : ' is-empty'}`}
        {...(readOnly
          ? {}
          : {
              role: 'button',
              tabIndex: 0,
              title: hasImpact ? 'Click to edit customer impact' : 'Add customer impact',
              onClick: startEdit,
              onKeyDown: (e: React.KeyboardEvent) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  startEdit();
                }
              },
            })}
      >
        {hasImpact ? customerImpact : '+ Add customer impact'}
      </p>
    );
  }

  return (
    <input
      className="mk-edit-input mk-workspace-impact-input"
      type="text"
      value={draft}
      disabled={saving}
      autoFocus
      placeholder="Customer impact in the user's terms (blank = no user-facing change)"
      aria-label="Edit customer impact"
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
