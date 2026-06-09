import React from 'react';
import Modal from '../Modal';

type ConfirmModalProps = {
  open: boolean;
  onClose: () => void;
  title: string;
  confirmLabel: string;
  onConfirm: () => void;
  confirmDisabled?: boolean;
  children: React.ReactNode;
};

// ConfirmModal is the shared shell for SystemSettingsSection's rename /
// delete / restore overlays (BACI-364). The body is the caller's children
// (a confirmation message, or the rename form fields) and the footer is the
// standard Cancel / confirm button pair: the confirm button carries the
// `is-active` accent, and Cancel re-uses Modal.Close so the button, Escape,
// and click-outside all dismiss identically.
export default function ConfirmModal({
  open,
  onClose,
  title,
  confirmLabel,
  onConfirm,
  confirmDisabled,
  children,
}: ConfirmModalProps) {
  return (
    <Modal open={open} onClose={onClose} title={title}>
      {children}
      <div className="mk-modal-actions">
        <Modal.Close asChild>
          <button className="mk-segmented-btn">Cancel</button>
        </Modal.Close>
        <button
          className="mk-segmented-btn is-active"
          onClick={onConfirm}
          disabled={confirmDisabled}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
