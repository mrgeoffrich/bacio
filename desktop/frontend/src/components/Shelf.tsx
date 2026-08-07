import React from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import Icon from './Icon';
import './shelf.css';

// Shelf is a right-docked, full-height overlay panel — the surface for a
// list too long to hang off a trigger as an anchored popover. It is a
// sibling of Modal, not a variant of it: Modal's `variant` is a *skin*
// on one centred-box layout, whereas this is a different layout, a
// different entrance and a different a11y label. Nesting the two is
// supported and expected (the repo picker opens its add-repo Modal from
// inside the shelf).
//
// Built on Radix Dialog rather than hand-rolled for one specific reason:
// the dismissable-layer stack. A hand-rolled outside-click + Escape
// listener has to be suspended by hand while a child modal is open,
// because the child portals outside the shelf's DOM subtree and a click
// inside it would otherwise dismiss the shelf and unmount the child
// mid-edit. Radix tracks the layers itself, so Escape and
// outside-pointer-down always hit the topmost one and the shelf survives.
// Focus trap, scroll-lock and the portal come along with it.
//
// The portal is also what keeps the panel clickable in the Wails desktop
// build: `.mk-topbar` sets `--wails-draggable: drag` and only opts
// `button`/`input`/`select` descendants back out, so a panel rendered
// inline under the topbar trigger would turn its own `<div>`s into
// window-drag handles. Portaled to document.body, the shelf is outside
// that subtree entirely and inherits nothing.
//
// z-index ladder (all hard-coded, no tokens): topbar 5 → anchored
// popovers 20 → shelf scrim 50 → shelf 60 → Modal 100/101 → error
// modal 200/201. See shelf.css.
type ShelfProps = {
  open: boolean;
  onClose: () => void;
  title: React.ReactNode;
  /** Panel aria-label, when the visible title isn't the right phrasing for it. */
  label?: string;
  /**
   * Runs on the open animation's focus step. Call `e.preventDefault()` and
   * focus something yourself to override Radix's first-focusable default —
   * e.g. to land on a search field rather than the close button.
   */
  onOpenAutoFocus?: (e: Event) => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
};

export default function Shelf({
  open,
  onClose,
  title,
  label,
  onOpenAutoFocus,
  children,
  footer,
}: ShelfProps) {
  return (
    <Dialog.Root open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="mk-shelf-scrim" />
        <Dialog.Content
          className="mk-shelf"
          aria-label={label}
          // Radix warns when a Dialog has no Description; every other
          // dialog in the app opts out the same way (see Modal.tsx).
          aria-describedby={undefined}
          onOpenAutoFocus={onOpenAutoFocus}
        >
          <header className="mk-shelf-head">
            <Dialog.Title className="mk-shelf-title">{title}</Dialog.Title>
            <Dialog.Close className="mk-icbtn mk-shelf-close" aria-label="Close">
              <Icon name="x" />
            </Dialog.Close>
          </header>
          <div className="mk-shelf-body">{children}</div>
          {footer && <div className="mk-shelf-foot">{footer}</div>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
