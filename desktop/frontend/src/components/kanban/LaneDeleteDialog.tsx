import Modal from '../Modal';
import { deleteLaneLines } from './kanbanLanes';
import * as api from '../../api';
import type { KanbanColumnDeletePreview } from '../../api';
import { useAsyncResource } from '../../lib/hooks/useAsyncResource';

// LaneDeleteDialog — the confirmation behind the one irreversible Kanban
// mutation.
//
// Deleting a lane is softer than it looks, and the number is what makes
// that legible: the cards it holds come OFF the board (their
// `kanban_column_id` goes back to NULL) and the issues themselves are
// never touched. That count comes from `previewDeleteKanbanColumn`, the
// dry-run twin of the real delete, so the dialog reports the actual blast
// radius rather than the lane length this window happens to be rendering
// — another window may have moved cards in or out since the last poll.
//
// Confirm stays disabled until the preview lands. A confirmation whose
// stated consequence is still loading is a confirmation of nothing.
type LaneDeleteDialogProps = {
  repoPrefix: string;
  uuid: string;
  name: string;
  busy: boolean;
  // Inline store refusal, kept next to the action for the same reason
  // LaneNameDialog does it.
  error: string;
  onConfirm: () => void;
  onClose: () => void;
};

export default function LaneDeleteDialog({
  repoPrefix,
  uuid,
  name,
  busy,
  error,
  onConfirm,
  onClose,
}: LaneDeleteDialogProps) {
  const { data: preview } = useAsyncResource<KanbanColumnDeletePreview | null>(
    () => api.previewDeleteKanbanColumn(repoPrefix, uuid),
    null,
    [repoPrefix, uuid],
    { errorHeadline: "Couldn't check what deleting this lane would remove" },
  );

  return (
    <Modal
      open
      onClose={busy ? undefined : onClose}
      title="Delete lane?"
      preventClickOutsideClose={busy}
    >
      <div className="mk-lane-dialog">
        <p className="mk-lane-dialog-subject">{name}</p>
        {preview === null ? (
          <p className="mk-settings-hint">Checking what this would take off the board…</p>
        ) : (
          <ul className="mk-lane-dialog-lines">
            {deleteLaneLines(preview.issuesRemovedFromBoard).map((line, i) => (
              <li key={i}>{line}</li>
            ))}
          </ul>
        )}

        {error && <p className="mk-lane-dialog-error">{error}</p>}

        <div className="mk-modal-actions">
          <button type="button" className="mk-segmented-btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="mk-segmented-btn is-danger"
            onClick={onConfirm}
            disabled={busy || preview === null}
          >
            {busy ? 'Deleting…' : 'Delete lane'}
          </button>
        </div>
      </div>
    </Modal>
  );
}
