import React from 'react';
import { m } from 'motion/react';
import OdometerNumber from './OdometerNumber.jsx';

// ShippedPill is the "Shipped · N" badge: the kit pill chrome
// (.mk-pill) tinted with the done-state palette (.mk-shipped-pill) plus
// a 3-digit OdometerNumber. Rendered by ShippedPopover as its trigger,
// which lives in the Pipeline's Shipping column. Pure presentational +
// click — owns no data.
//
// The optional Motion flight-target slot (`flyingKey`) is only mounted by
// the Pipeline's BACI-193 ship-flourish; consumers that don't fly cards
// into the pill leave it null and no slot renders.
export default function ShippedPill({
  count,
  onClick,
  disabled = false,
  flashing = false,
  flyingKey = null,
  onFlightDone,
  title,
  ariaHasPopup,
  ariaExpanded,
}) {
  const safeCount = Number.isFinite(count) && count > 0 ? count : 0;
  return (
    <button
      type="button"
      className={`mk-pill mk-shipped-pill${flashing ? ' is-flash' : ''}`}
      title={title}
      disabled={disabled}
      onClick={onClick}
      aria-haspopup={ariaHasPopup}
      aria-expanded={ariaExpanded}
      aria-label={`Shipped, ${safeCount}`}
    >
      <span className="mk-shipped-pill-label">Shipped</span>
      <OdometerNumber value={safeCount} />
      {flyingKey && (
        <m.div
          key={flyingKey}
          layoutId={flyingKey}
          className="mk-shipped-flight-target"
          aria-hidden="true"
          onLayoutAnimationComplete={onFlightDone}
        />
      )}
    </button>
  );
}
