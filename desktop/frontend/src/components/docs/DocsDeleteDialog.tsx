import Modal from '../Modal';

// DocsDeleteDialog — the confirmation behind the two irreversible Documents
// mutations.
//
// Deleting a PAGE destroys the body and every link pointing at it; archiving
// is the reversible option and the dialog says so, because "Delete" sitting
// next to "Archive" in the same menu invites the wrong click.
//
// Deleting a FOLDER is softer than it looks and the numbers are what make
// that legible: descendant folders go with it, but every page in that
// subtree is RE-ROOTED, never destroyed. Those counts come from
// `previewDeleteDocFolder`, the dry-run twin of the real delete, so the
// dialog reports the actual blast radius rather than a guess.
//
// That preview is one round trip behind the dialog opening, so Delete
// stays disabled until it lands (`confirmDisabled`) — a confirmation
// whose stated consequence is still loading is a confirmation of
// nothing. Same rule as LaneDeleteDialog on the Kanban.

type DocsDeleteDialogProps = {
  open: boolean;
  title: string;
  // The thing being deleted, shown in the mono face.
  subject: string;
  // The consequence lines. Rendered as a list above the actions.
  lines: string[];
  busy: boolean;
  error: string;
  // True while `lines` is a placeholder rather than the real blast
  // radius — see the header note.
  confirmDisabled: boolean;
  onConfirm: () => void;
  onClose: () => void;
};

export default function DocsDeleteDialog({
  open,
  title,
  subject,
  lines,
  busy,
  error,
  confirmDisabled,
  onConfirm,
  onClose,
}: DocsDeleteDialogProps) {
  return (
    <Modal
      open={open}
      onClose={busy ? undefined : onClose}
      title={title}
      preventClickOutsideClose={busy}
    >
      <div className="mk-docs-dialog">
        <p className="mk-docs-dialog-subject">{subject}</p>
        <ul className="mk-docs-dialog-lines">
          {lines.map((l, i) => <li key={i}>{l}</li>)}
        </ul>

        {error && <p className="mk-docs-dialog-error">{error}</p>}

        <div className="mk-modal-actions">
          <button type="button" className="mk-segmented-btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="mk-segmented-btn is-danger"
            onClick={onConfirm}
            disabled={busy || confirmDisabled}
          >
            {busy ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      </div>
    </Modal>
  );
}
