import React from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import Icon from '../Icon';
import { blockedByMode } from '../../lib/blockedByBadge';
import type { BoardCardBlocker } from '../../api';

// BLOCKER_STATE_LABEL maps the open-state set carried by a blocker
// (BoardCardBlocker.state, always one of todo | in_progress |
// needs_action | in_review) to the human label shown on the multi-blocker
// popover's state pill. Mirrors STATE_LABELS in api.http.ts (module-
// private there); kept tiny and local since only the popover needs it.
const BLOCKER_STATE_LABEL: Partial<Record<string, string>> = {
  todo: 'Todo',
  in_progress: 'In Progress',
  needs_action: 'Needs Action',
  in_review: 'In Review',
};

// BlockedByBadge — the per-card blocked-by indicator (BACI-310). Purely
// informational: it never disables Start. card.blockedBy is the open
// `blocks` edges pointing AT this card (the server filters out
// done/cancelled blockers, so the badge clears automatically when a
// blocker finishes).
//
//   single blocker → a 🔒 chip showing the blocker's key directly;
//   multiple        → a 🔒 "blocked by N" chip opening a Radix popover
//                      (re-uses the .mk-card-blocked-menu* CSS) listing
//                      each blocker key + state pill.
//
// Hovering a blocker reference calls onHighlight(key) so PipelineView can
// red-highlight that blocker's card if it is currently on screen, and
// onHighlight(null) on leave. Clicking navigates to the blocker via
// onOpenIssue. All click/select handlers stopPropagation so opening the
// popover or following a link never starts a drag or trips the card's
// onOpen.
//
// BACI-342 drag-to-block: the badge doubles as the block-drag grab handle.
// When canBlock is set (the card is an in-scope Backlog / In-Pipeline card)
// the chip is `draggable`; its dragstart fires onBlockDragStart and
// stopPropagation()s so the card's own move-drag never co-fires (the two
// gestures live on the same physical surface, disambiguated by which one
// starts). A card with no blockers still gets a faint grab-chip so every
// in-scope card is a drag source — without it an unblocked card would have
// no handle to start the gesture from. dragHandlers bundles the three
// drag props shared by all three render modes.
type BlockedByBadgeProps = {
  blockedBy?: BoardCardBlocker[];
  onOpenIssue?: (key: string) => void;
  onHighlight?: (key: string | null) => void;
  sourceKey: string;
  canBlock?: boolean;
  onBlockDragStart?: (key: string) => void;
  onBlockDragEnd?: () => void;
};

export default function BlockedByBadge({ blockedBy, onOpenIssue, onHighlight, sourceKey, canBlock, onBlockDragStart, onBlockDragEnd }: BlockedByBadgeProps) {
  const mode = blockedByMode(blockedBy);
  const dragHandlers: React.HTMLAttributes<HTMLButtonElement> & { draggable?: boolean } = canBlock
    ? {
        draggable: true,
        onDragStart: (e) => { e.stopPropagation(); onBlockDragStart?.(sourceKey); },
        onDragEnd: (e) => { e.stopPropagation(); onBlockDragEnd?.(); },
      }
    : {};

  if (mode === 'none') {
    // No blockers yet: render a faint grab-chip only when the card can be a
    // block-drag source. Otherwise (Shipping cards) render nothing, same as
    // before BACI-342.
    if (!canBlock) return null;
    return (
      <button
        type="button"
        className="mk-pl-blocked-btn is-grab"
        aria-label="Drag onto another card to block this one"
        title="Drag onto another card to mark this one blocked by it"
        onClick={(e) => e.stopPropagation()}
        {...dragHandlers}
      >
        <Icon name="lock" />
      </button>
    );
  }

  if (mode === 'single') {
    // mode === 'single' means blockedByMode saw exactly one blocker, so
    // blockedBy is non-empty — the same invariant the original code relied
    // on when it indexed blockedBy[0] unguarded.
    const { key } = blockedBy![0];
    return (
      <button
        type="button"
        className="mk-pl-blocked-btn"
        aria-label={`Blocked by ${key}`}
        title={`Blocked by ${key}`}
        onClick={(e) => { e.stopPropagation(); onOpenIssue?.(key); }}
        onMouseEnter={() => onHighlight?.(key)}
        onMouseLeave={() => onHighlight?.(null)}
        {...dragHandlers}
      >
        <Icon name="lock" />
        <span className="mk-pl-blocked-lbl">{key}</span>
      </button>
    );
  }

  // multi — mode === 'multi' means blockedByMode saw two or more blockers,
  // so blockedBy is non-empty (same invariant as the single branch).
  const blockers = blockedBy!;
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-pl-blocked-btn"
          aria-label={`Blocked by ${blockers.length} issues`}
          title={`Blocked by ${blockers.length} issues`}
          onClick={(e) => e.stopPropagation()}
          {...dragHandlers}
        >
          <Icon name="lock" />
          <span className="mk-pl-blocked-lbl">blocked by {blockers.length}</span>
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-card-blocked-menu"
          align="start"
          side="bottom"
          sideOffset={4}
          collisionPadding={8}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="mk-card-blocked-menu-label">Blocked by</div>
          {blockers.map(b => (
            <DropdownMenu.Item
              key={b.key}
              className="mk-card-blocked-item"
              onSelect={() => onOpenIssue?.(b.key)}
              onMouseEnter={() => onHighlight?.(b.key)}
              onMouseLeave={() => onHighlight?.(null)}
            >
              <span className="mk-card-id">{b.key}</span>
              <span className={`mk-pill mk-status-${b.state}`}>
                {BLOCKER_STATE_LABEL[b.state] ?? b.state}
              </span>
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
