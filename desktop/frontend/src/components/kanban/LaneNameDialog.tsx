import { useEffect, useState } from 'react';
import Modal from '../Modal';

// LaneNameDialog — the one "type a lane name" dialog behind both naming
// mutations on the Kanban: add a lane, rename a lane. One component
// rather than two because they differ only in their labels.
//
// Like the Documents dialogs it keeps the store's refusal INLINE. Lane
// names are unique per repo, so `createKanbanColumn` / `renameKanbanColumn`
// can come back with a plain "a lane called Doing already exists" error;
// routing that to the global error modal would close this dialog and lose
// what the user typed, so the caller hands the message back through
// `error` and the dialog stays open with the text intact.
type LaneNameDialogProps = {
  title: string;
  initialValue: string;
  submitLabel: string;
  busyLabel: string;
  busy: boolean;
  // Inline validation / store refusal. Empty string = no error.
  error: string;
  onSubmit: (value: string) => void;
  onClose: () => void;
};

export default function LaneNameDialog({
  title,
  initialValue,
  submitLabel,
  busyLabel,
  busy,
  error,
  onSubmit,
  onClose,
}: LaneNameDialogProps) {
  const [value, setValue] = useState(initialValue);

  // Re-seed whenever the caller changes what it is naming, so a rename
  // lands pre-filled with the current name and an add lands empty.
  useEffect(() => setValue(initialValue), [initialValue]);

  const submit = () => {
    const trimmed = value.trim();
    if (!trimmed || busy) return;
    onSubmit(trimmed);
  };

  return (
    <Modal open onClose={busy ? undefined : onClose} title={title} preventClickOutsideClose={busy}>
      <div className="mk-lane-dialog">
        <fieldset className="mk-sync-setup-fields" disabled={busy}>
          <label className="mk-tmpl-add-field">
            <span>Lane name</span>
            <input
              autoFocus
              className="mk-tmpl-input"
              value={value}
              placeholder="e.g. In Review"
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                }
              }}
            />
          </label>
        </fieldset>

        {error && <p className="mk-lane-dialog-error">{error}</p>}

        <div className="mk-modal-actions">
          <button type="button" className="mk-segmented-btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="mk-segmented-btn is-active"
            onClick={submit}
            disabled={busy || value.trim() === ''}
          >
            {busy ? busyLabel : submitLabel}
          </button>
        </div>
      </div>
    </Modal>
  );
}
