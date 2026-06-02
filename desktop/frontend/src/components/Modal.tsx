import React from 'react';
import * as Dialog from '@radix-ui/react-dialog';

type ModalProps = {
  open: boolean;
  onClose?: () => void;
  title: React.ReactNode;
  variant?: 'error';
  preventClickOutsideClose?: boolean;
  children?: React.ReactNode;
};

// Modal wraps Radix Dialog with bacio's mk-modal* classes. Variants:
//   - default: confirmation/form dialogs (z-index 100/101)
//   - "error": red-accented, sits above other overlays (z-index 200/201)
// Pass preventClickOutsideClose when the modal must be dismissed via an
// in-content action (e.g. the error modal's Dismiss button).
// Modal.Close re-exports Dialog.Close for asChild Cancel buttons.
export default function Modal({
  open,
  onClose,
  title,
  variant,
  preventClickOutsideClose,
  children,
}: ModalProps) {
  const isError = variant === 'error';
  const backdropClass = isError
    ? 'mk-modal-backdrop mk-error-modal-backdrop'
    : 'mk-modal-backdrop';
  const contentClass = isError ? 'mk-modal mk-error-modal' : 'mk-modal';
  const titleClass = isError
    ? 'mk-modal-title mk-error-modal-title'
    : 'mk-modal-title';

  const contentProps: React.ComponentProps<typeof Dialog.Content> = {
    className: contentClass,
    'aria-describedby': undefined,
  };
  if (preventClickOutsideClose) {
    contentProps.onPointerDownOutside = (e) => e.preventDefault();
  }

  return (
    <Dialog.Root open={open} onOpenChange={(o) => { if (!o) onClose?.(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className={backdropClass} />
        <Dialog.Content {...contentProps}>
          <Dialog.Title className={titleClass}>{title}</Dialog.Title>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

Modal.Close = Dialog.Close;
