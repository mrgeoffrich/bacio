import { useState, useRef, useEffect, useMemo } from 'react';
import type { Board } from '../api';
import Modal from './Modal';
import Shelf from './Shelf';
import Icon from './Icon';
import { WEB_MODE } from '../env';
import { useActiveRepo } from '../state/RepoProvider';
import { useRepoActivity } from '../state/useRepoActivity';
import { activityByPrefix, rankRepos, groupReposByKind } from './repoPickerOrder';
import WorkspaceCreateModal from './workspace/WorkspaceCreateModal';
import { formatWhen } from '../lib/formatWhen';
import './workspace/workspace.css';

// Web-only Add-Repository modal state: the path/name/prefix the user is
// typing. Null when the modal is closed.
type AddWebState = { path: string; name: string; prefix: string };

// RepoPicker is the topbar's repository selector. Clicking the trigger
// opens a right-docked <Shelf> with a filter input, the matching repos
// grouped by kind, and the two ways to add a project.
//
// It was an anchored 280px dropdown until the list outgrew it: with
// workspaces alongside git repos there are simply more rows than a
// popover under the trigger can show without its own inner scrollbar.
// The shelf is full-height, so the list gets the whole viewport.
//
// Moving to <Shelf> (Radix Dialog) also deleted this component's
// hand-rolled dismissal: an outside-click + Escape listener plus a
// `modalOpen` flag that had to suspend it while WorkspaceCreateModal or
// the web add-repo Modal was open, because those portal outside the
// picker's subtree and a click inside one would otherwise dismiss the
// dropdown and unmount the modal mid-edit. Radix's dismissable-layer
// stack handles that: the child modal is the topmost layer, so Escape
// and outside-pointer-down dismiss it and leave the shelf open.
//
// **Add Git Repository…** forks on WEB_MODE: desktop pops a native folder
// picker via Wails, web pops a path-input modal that POSTs the typed path
// to /repos (BACI-50). The onAddRepository callback handles both: desktop
// ignores its payload, web reads {path, name, prefix?} off it.
//
// **New Workspace…** does not fork. A workspace is a pathless, git-less
// project (`repos.kind = 'workspace'`), so there is no folder to pick and
// nothing native to invoke — the same WorkspaceCreateModal collects
// {name, prefix?} on both transports and calls api.addWorkspace.
//
// BACI-361: reads the board list + active repo + pick/add helpers from
// useActiveRepo() rather than props.
//
// BACI-369: rows are ordered by recent activity, with repos that have
// agent jobs in flight floated to the top and badged with the running
// count. The order is snapshotted when the menu opens so a poll tick
// can't reshuffle rows under the user's cursor; the pills keep updating
// live off the polled data.
export default function RepoPicker() {
  const {
    boards,
    activeBoard,
    pickBoard: onPick,
    addRepository: onAddRepository,
    refreshBoards,
  } = useActiveRepo();
  const { activity } = useRepoActivity();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  // Prefixes in the order the open menu is rendering them. Empty until
  // the first open (and while closed) — the render falls back to
  // `boards` as-is then.
  const [order, setOrder] = useState<string[]>([]);
  // Web-only: { path, name, prefix } modal state. Null = closed.
  const [addingWeb, setAddingWeb] = useState<AddWebState | null>(null);
  const [addError, setAddError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  // Workspace modal open flag. Same on both transports — see the header.
  const [addingWorkspace, setAddingWorkspace] = useState(false);
  // Prefix of a just-created workspace we still owe a navigation to. The
  // active repo is URL-derived (BACI-285), so navigating before the
  // provider's board list carries the new prefix would flash RepoNotFound;
  // the effect below waits for the refreshed list instead.
  const [pendingPrefix, setPendingPrefix] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const active = boards.find((b: Board) => b.prefix === activeBoard);
  const label = active?.name || activeBoard || 'Select repository';

  // Dismissal is Shelf's (Radix's) job now — including keeping the shelf
  // open behind a child modal. All that's left is resetting the filter so
  // the next open starts clean.
  const closeShelf = () => {
    setOpen(false);
    setQuery('');
  };

  // A workspace is created through the api seam directly (RepoProvider's
  // addRepository is the git path and owns the folder picker), so the
  // provider's board list has to be refreshed and then navigated to. Wait
  // for the refreshed list to actually carry the new prefix before
  // routing: activeBoard is derived from the URL, and an unknown prefix
  // renders RepoNotFound until the boards land.
  useEffect(() => {
    if (!pendingPrefix) return;
    if (!boards.some((b: Board) => b.prefix === pendingPrefix)) return;
    setPendingPrefix('');
    onPick(pendingPrefix);
  }, [boards, pendingPrefix, onPick]);

  const byPrefix = useMemo(() => activityByPrefix(activity), [activity]);

  // Freeze the ranked order at open time. A click handler is the honest
  // place for "snapshot on open" — an effect would fight the deps lint
  // and re-rank on every poll tick.
  const toggleMenu = () => {
    if (open) {
      closeShelf();
      return;
    }
    setOrder(rankRepos(boards, byPrefix).map((b: Board) => b.prefix));
    setOpen(true);
  };

  // Render order: the frozen snapshot, with any repo added mid-session
  // (absent from it) appended at the end. No snapshot yet = boards as-is.
  const ordered = useMemo(() => {
    if (order.length === 0) return boards;
    const rank = new Map(order.map((p, i) => [p, i]));
    return [...boards].sort((a: Board, b: Board) =>
      (rank.get(a.prefix) ?? order.length) - (rank.get(b.prefix) ?? order.length));
  }, [boards, order]);

  // Filter after ordering so typing narrows the list without reshuffling it.
  const q = query.trim().toLowerCase();
  const filtered = q
    ? ordered.filter((b: Board) =>
        b.name.toLowerCase().includes(q) || b.prefix.toLowerCase().includes(q))
    : ordered;

  // Split the (already frozen-ordered, already filtered) list into its two
  // kinds. The partition is stable, so each section keeps the frozen rank
  // order it had; `kind` never changes for a live board, so a poll tick
  // can't move a row across sections either.
  const groups = useMemo(() => groupReposByKind(filtered), [filtered]);
  // Label the sections only once the store actually holds a workspace. A
  // git-only store — every store, until someone makes one — renders the
  // flat list it always has, with no headers to explain away. The flag
  // reads the whole board list, not the filtered one, so narrowing the
  // search down to a single section doesn't drop its label.
  const showGroupHeads = useMemo(
    () => boards.some((b: Board) => b.kind === 'workspace'),
    [boards],
  );

  const pick = (prefix: string) => {
    onPick(prefix);
    closeShelf();
  };

  // renderRow is shared by both sections — one row shape, two groupings.
  const renderRow = (b: Board) => {
    const act = byPrefix.get(b.prefix);
    const jobs = act?.activeJobs ?? 0;
    return (
      <button
        key={b.prefix}
        className={`mk-repo-picker-item ${b.prefix === activeBoard ? 'is-active' : ''}`}
        onClick={() => pick(b.prefix)}
        role="option"
        aria-selected={b.prefix === activeBoard}
        title={act?.lastActivityAt ? `Last active ${formatWhen(act.lastActivityAt)}` : undefined}
      >
        <span className="mk-repo-picker-item-name">{b.name}</span>
        {jobs > 0 && (
          <span
            className="mk-pill mk-status-busy mk-repo-picker-item-jobs"
            aria-label={`${jobs} job${jobs === 1 ? '' : 's'} running`}
          >
            {jobs}
          </span>
        )}
        <span className="mk-repo-picker-item-prefix">{b.prefix}</span>
      </button>
    );
  };

  const renderGroup = (label: string, rows: Board[]) => {
    if (rows.length === 0) return null;
    return (
      <div className="mk-repo-picker-group" role="group" aria-label={label}>
        {showGroupHeads && <div className="mk-repo-picker-group-head">{label}</div>}
        {rows.map(renderRow)}
      </div>
    );
  };

  // Desktop path: hand straight off to the native folder picker.
  const addDesktop = () => {
    closeShelf();
    onAddRepository();
  };

  // Workspace path: identical on both transports — no folder picker, no
  // path field, no WEB_MODE fork.
  const openWorkspaceModal = () => {
    setAddingWorkspace(true);
  };
  const onWorkspaceCreated = (board: Board) => {
    setAddingWorkspace(false);
    closeShelf();
    // Pull the new row into the provider's list, then let the effect above
    // route to it once it's there.
    refreshBoards();
    if (board.prefix) setPendingPrefix(board.prefix);
  };

  // Web path: open the inline modal, gather path/name/prefix, then submit.
  const openWebModal = () => {
    setAddError('');
    setAddingWeb({ path: '', name: '', prefix: '' });
  };
  const closeWebModal = () => {
    if (submitting) return;
    setAddingWeb(null);
    setAddError('');
  };
  const submitWebAdd = async () => {
    if (!addingWeb || submitting) return;
    const path = addingWeb.path.trim();
    const name = addingWeb.name.trim();
    if (!path || !name) {
      setAddError('Both path and name are required.');
      return;
    }
    setSubmitting(true);
    setAddError('');
    try {
      const result = await onAddRepository({
        path,
        name,
        prefix: addingWeb.prefix.trim() || undefined,
      });
      if (result) {
        setAddingWeb(null);
        closeShelf();
      }
    } catch (err) {
      // App.tsx already routes the failure through the global error
      // modal; surface the message inline too so the modal stays open
      // and the user can correct the typed path.
      const message = err instanceof Error ? err.message : '';
      setAddError(message || 'Failed to add repository');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mk-repo-picker">
      <button
        className="mk-repo-picker-trigger"
        onClick={toggleMenu}
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        <Icon name="branch" />
        <span className="mk-repo-picker-label">{label}</span>
        {active && (
          <>
            <span className="mk-repo-picker-sep">·</span>
            <span className="mk-repo-picker-code">{active.prefix}</span>
          </>
        )}
        {/* Once selected, the name + prefix alone can't say which kind
            you're in, and a workspace behaves differently (no working
            tree, no agent dispatch). Mark it on the closed trigger. */}
        {active?.kind === 'workspace' && (
          <span className="mk-repo-picker-kind">Workspace</span>
        )}
      </button>

      <Shelf
        open={open}
        onClose={closeShelf}
        title="Switch project"
        // Radix would otherwise focus the first tabbable node (the close
        // button); the filter is the useful landing spot.
        onOpenAutoFocus={(e) => { e.preventDefault(); inputRef.current?.focus(); }}
        footer={
          /* Two ways in, not one. The git option keeps today's behaviour
             verbatim on both transports; the workspace option is the
             same modal everywhere. */
          <div className="mk-repo-picker-add-group">
            <button
              className="mk-repo-picker-add"
              onClick={WEB_MODE ? openWebModal : addDesktop}
            >
              <Icon name="plus" />
              Add Git Repository…
            </button>
            <button
              className="mk-repo-picker-add"
              onClick={openWorkspaceModal}
            >
              <Icon name="plus" />
              New Workspace…
            </button>
          </div>
        }
      >
        <input
          ref={inputRef}
          className="mk-repo-picker-search"
          type="text"
          placeholder="Search repositories…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        {/* role="listbox" sits on the scrolling list itself — the shelf
            around it is a dialog, not a listbox. */}
        <div className="mk-repo-picker-list" role="listbox" aria-label="Projects">
          {filtered.length === 0 ? (
            <div className="mk-repo-picker-empty">No matching repositories.</div>
          ) : (
            <>
              {renderGroup('Repositories', groups.git)}
              {renderGroup('Workspaces', groups.workspaces)}
            </>
          )}
        </div>

        {/* Both add-modals render inside the shelf. Radix stacks them as
            the topmost dismissable layer, so Escape / an outside click
            closes just the modal and the shelf stays put. */}
        <WorkspaceCreateModal
          open={addingWorkspace}
          onClose={() => setAddingWorkspace(false)}
          onCreated={onWorkspaceCreated}
        />

        <Modal
          open={!!addingWeb}
          onClose={() => { if (!submitting) closeWebModal(); }}
          title="Add Git Repository"
        >
          {addingWeb && (
            <>
              <p className="mk-settings-hint">
                Path is on the server&apos;s filesystem
                {' '}({window.location.host || 'this host'}), not your local machine.
                Point at the git working tree you want bacio to track.
              </p>
              <label className="mk-tmpl-add-field">
                <span>Path</span>
                <input
                  autoFocus
                  className="mk-tmpl-input"
                  value={addingWeb.path}
                  placeholder="/Users/you/Code/my-project"
                  onChange={e => setAddingWeb({ ...addingWeb, path: e.target.value })}
                />
              </label>
              <label className="mk-tmpl-add-field">
                <span>Name</span>
                <input
                  className="mk-tmpl-input"
                  value={addingWeb.name}
                  placeholder="my-project"
                  onChange={e => setAddingWeb({ ...addingWeb, name: e.target.value })}
                />
              </label>
              <label className="mk-tmpl-add-field">
                <span>Prefix (optional)</span>
                <input
                  className="mk-tmpl-input"
                  value={addingWeb.prefix}
                  placeholder="MYPR (auto-allocated if blank)"
                  maxLength={4}
                  onChange={e => setAddingWeb({ ...addingWeb, prefix: e.target.value })}
                />
              </label>
              {addError && (
                <p className="mk-settings-hint" style={{ color: 'var(--status-blocked, #d44)' }}>
                  {addError}
                </p>
              )}
              <div className="mk-modal-actions">
                <button
                  className="mk-segmented-btn"
                  onClick={closeWebModal}
                  disabled={submitting}
                >
                  Cancel
                </button>
                <button
                  className="mk-segmented-btn is-active"
                  onClick={submitWebAdd}
                  disabled={submitting}
                >
                  {submitting ? 'Adding…' : 'Add'}
                </button>
              </div>
            </>
          )}
        </Modal>
      </Shelf>
    </div>
  );
}
