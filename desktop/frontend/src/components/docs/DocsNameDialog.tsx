import { useEffect, useState } from 'react';
import Modal from '../Modal';

// DocsNameDialog — the one "type a name" dialog behind every naming
// mutation on the Documents page: new page, new folder, rename page, rename
// folder. One component rather than four because they differ only in their
// labels, and because they all need the same thing the app's other create
// flows need: somewhere to show the store's refusal NEXT TO the input.
//
// That last part is load-bearing. Document filenames stay globally unique
// per repo (folders are purely organisational — two pages in different
// folders still cannot share a name), and sibling folder names must be
// unique within their parent. Both refusals come back from the store as
// plain errors; routing them to the global error modal would close this
// dialog and lose what the user typed. So the caller hands the message back
// through `error` and the dialog stays open with the text intact.

type DocsNameDialogProps = {
  open: boolean;
  title: string;
  // Field label, e.g. "Page name".
  label: string;
  placeholder?: string;
  hint?: string;
  initialValue: string;
  submitLabel: string;
  busyLabel: string;
  busy: boolean;
  // Inline validation / store refusal. Empty string = no error.
  error: string;
  onSubmit: (value: string) => void;
  onClose: () => void;
};

export default function DocsNameDialog({
  open,
  title,
  label,
  placeholder,
  hint,
  initialValue,
  submitLabel,
  busyLabel,
  busy,
  error,
  onSubmit,
  onClose,
}: DocsNameDialogProps) {
  const [value, setValue] = useState(initialValue);

  // Re-seed on open (and whenever the caller changes what it's naming) so a
  // rename lands pre-filled with the current name and a create lands empty.
  useEffect(() => {
    if (open) setValue(initialValue);
  }, [open, initialValue]);

  const submit = () => {
    const trimmed = value.trim();
    if (!trimmed || busy) return;
    onSubmit(trimmed);
  };

  return (
    <Modal
      open={open}
      onClose={busy ? undefined : onClose}
      title={title}
      preventClickOutsideClose={busy}
    >
      <div className="mk-docs-dialog">
        <fieldset className="mk-sync-setup-fields" disabled={busy}>
          <label className="mk-tmpl-add-field">
            <span>{label}</span>
            <input
              autoFocus
              className="mk-tmpl-input"
              value={value}
              placeholder={placeholder}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                }
              }}
            />
          </label>
          {hint && <p className="mk-settings-hint">{hint}</p>}
        </fieldset>

        {error && <p className="mk-docs-dialog-error">{error}</p>}

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
