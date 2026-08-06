import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { MoreHorizontal } from 'lucide-react';

// LaneMenu — the lane-header "⋯" carrying the lane CRUD Phase 6A left to
// the CLI: rename, nudge left/right, delete.
//
// Reuses the orphaned `.mk-card-action-menu` / `.mk-card-action-item`
// popover the pre-pivot card menu wore, so the Kanban's two dropdowns
// (this and the card's) share one visual vocabulary with the Documents
// tree's row menu without inventing a third.
//
// The move items are omitted rather than disabled at the ends of the
// board: a greyed-out row invites a click that does nothing, and a lane
// that is already leftmost has nothing to say about moving left. Creating
// a lane is NOT here — it belongs to the board, not to any one lane, so
// it lives on the trailing "Add lane" slot at the right-hand end.
type LaneMenuProps = {
  laneName: string;
  canMoveLeft: boolean;
  canMoveRight: boolean;
  onRename: () => void;
  onMoveLeft: () => void;
  onMoveRight: () => void;
  onDelete: () => void;
};

export default function LaneMenu({
  laneName,
  canMoveLeft,
  canMoveRight,
  onRename,
  onMoveLeft,
  onMoveRight,
  onDelete,
}: LaneMenuProps) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-col-menu-btn"
          aria-label={`Actions for ${laneName} lane`}
          title={`Actions for ${laneName} lane`}
        >
          <MoreHorizontal size={14} strokeWidth={2} aria-hidden="true" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-card-action-menu"
          align="end"
          side="bottom"
          sideOffset={4}
          collisionPadding={8}
        >
          <DropdownMenu.Item className="mk-card-action-item" onSelect={onRename}>
            Rename lane…
          </DropdownMenu.Item>
          {canMoveLeft && (
            <DropdownMenu.Item className="mk-card-action-item" onSelect={onMoveLeft}>
              Move lane left
            </DropdownMenu.Item>
          )}
          {canMoveRight && (
            <DropdownMenu.Item className="mk-card-action-item" onSelect={onMoveRight}>
              Move lane right
            </DropdownMenu.Item>
          )}
          <DropdownMenu.Separator className="mk-card-action-sep" />
          <DropdownMenu.Item className="mk-card-action-item is-danger" onSelect={onDelete}>
            Delete lane…
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
