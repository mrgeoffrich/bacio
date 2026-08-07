import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { ChevronDown, Columns3, FilePlus2, Layers, Plus } from 'lucide-react';
import { useNavigate } from 'react-router';
import Tooltip from './Tooltip';
import { useActiveRepo } from '../state/RepoProvider';
import { requestNavigation } from '../lib/navGuard';
import { newEpicPath, viewPath } from '../lib/routes';

// The unified create affordance.
//
// One rule, stated once and then applied everywhere:
//
//   > A Plus in the top-right of the container the thing will be created
//   > into. It opens a MENU when the container accepts more than one
//   > type, and the create flow DIRECTLY when it accepts one.
//
// The Topbar is the outermost container — the space itself — which is why
// the global instance offers all three record types. A Kanban lane
// accepts one type, the Epics list accepts one, the Docs rail accepts two
// (and satisfies "more than one" with two adjacent buttons rather than a
// menu, which is cheaper and already shipped).
//
// This module holds both halves of that rule so they cannot drift:
// `CreateMenu` is the multi-type Topbar menu, `ScopedCreateButton` is the
// single-type plus every surface header wears. They share `.mk-create-btn`,
// which is lifted from the Kanban lane's own `+` and now replaces
// `.mk-pl-new-issue` as well.
//
// Why a global instance exists at all: `navFor()` hides the Pipeline tab
// when `showAgentSurfaces` is off, which is the DEFAULT for a manual
// workspace — and the Pipeline Backlog header was the only place the "new
// issue" button lived. On a workspace, no button in the application
// created an issue. That is the hole this closes; consistency is how, not
// why.

type ScopedCreateButtonProps = {
  // The accessible name, and the native `title` when there is no Radix
  // tooltip to carry the hover copy.
  label: string;
  onClick: () => void;
  // Omit for no tooltip; `Tooltip` is a no-op when the label is empty, so
  // wrapping unconditionally is safe.
  tooltip?: string;
  disabled?: boolean;
};

// ScopedCreateButton is the 22px plus that sits in a surface header when
// the container accepts exactly one type: the Epics list head, the
// Pipeline Backlog head. It renders the create flow directly rather than a
// menu, per the rule.
export function ScopedCreateButton({ label, onClick, tooltip, disabled }: ScopedCreateButtonProps) {
  return (
    <Tooltip label={tooltip}>
      <button
        type="button"
        className="mk-create-btn"
        aria-label={label}
        // Radix does not suppress a `title` you set yourself, so setting
        // both shows the styled bubble and the browser's own beside it.
        title={tooltip ? undefined : label}
        disabled={disabled}
        onClick={onClick}
      >
        <Plus size={15} strokeWidth={2} aria-hidden="true" />
      </button>
    </Tooltip>
  );
}

type CreateMenuProps = {
  // Opening the IssueComposer is a Shell-owned overlay flag, threaded the
  // same way `onOpenSettings` / `onOpenSync` already are.
  onNewIssue: () => void;
};

// CreateMenu is the global `+ New ▾` in the Topbar — the only filled
// control in `.mk-topbar-right`, and that is the point: it must read as
// the action among four status utilities.
//
// It does NOT re-take the corner BACI-287 gave the notification bell. The
// bell keeps the corner and the settings gear keeps the far edge; what
// lands one slot inboard is a labelled, type-agnostic menu, not the
// issue-only Pipeline-bound button that moved out.
export default function CreateMenu({ onNewIssue }: CreateMenuProps) {
  const { activeBoard } = useActiveRepo();
  const navigate = useNavigate();
  // The same gate App's ⌘N handler and the Pipeline `+` already apply. The
  // board list is undefined for a tick on first paint; treat that as
  // disabled so the strip never reflows.
  const enabled = !!activeBoard && activeBoard !== 'all';

  if (!enabled) {
    // Disabled, not hidden — a control that vanishes teaches nothing, and
    // the merged "All repos" board is a place users land often.
    //
    // `aria-disabled` rather than `disabled`: a truly disabled button
    // swallows pointer events, which would take the tooltip explaining WHY
    // it is off with it. The click handler is simply absent.
    return (
      <Tooltip label="Pick a repo or workspace first">
        <button
          type="button"
          className="mk-create-btn mk-create-btn--lg"
          aria-label="Create…"
          aria-disabled={true}
        >
          <Plus size={15} strokeWidth={2} aria-hidden="true" />
          <span className="mk-create-btn-label">New</span>
          <ChevronDown size={13} strokeWidth={2} aria-hidden="true" />
        </button>
      </Tooltip>
    );
  }

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-create-btn mk-create-btn--lg"
          aria-label="Create…"
        >
          <Plus size={15} strokeWidth={2} aria-hidden="true" />
          <span className="mk-create-btn-label">New</span>
          <ChevronDown size={13} strokeWidth={2} aria-hidden="true" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-card-action-menu mk-create-menu"
          align="end"
          side="bottom"
          sideOffset={6}
          collisionPadding={8}
        >
          {/* The only thing in the menu that answers "into WHAT" — and the
              rule is about containers. */}
          <div className="mk-card-action-menu-label">In {activeBoard}</div>
          {/* Ordered by frequency, not by nav order. Issues are created
              constantly, epics occasionally; pages sit in between but
              already have four other entries, so they go last. */}
          {/* Through `requestNavigation` like its two siblings: the
              composer routes on create, and a mounted route holding
              unsaved work must get its confirm before that happens. */}
          <DropdownMenu.Item
            className="mk-card-action-item"
            onSelect={() => requestNavigation(onNewIssue)}
          >
            <Columns3 size={13} strokeWidth={2} aria-hidden="true" />
            <span>New issue</span>
            <span className="mk-create-menu-kbd">⌘N</span>
          </DropdownMenu.Item>
          <DropdownMenu.Item
            className="mk-card-action-item"
            onSelect={() => requestNavigation(() => navigate(newEpicPath(activeBoard)))}
          >
            <Layers size={13} strokeWidth={2} aria-hidden="true" />
            <span>New epic</span>
          </DropdownMenu.Item>
          {/* The page dialog lives inside DocsView (it needs the folder
              tree), so the menu routes to the Documents surface and asks
              it to open the dialog at the tree root. DocsView clears the
              flag on mount so a reload doesn't re-open it. */}
          <DropdownMenu.Item
            className="mk-card-action-item"
            onSelect={() =>
              requestNavigation(() => navigate(`${viewPath(activeBoard, 'docs')}?new=page`))
            }
          >
            <FilePlus2 size={13} strokeWidth={2} aria-hidden="true" />
            <span>New page</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
