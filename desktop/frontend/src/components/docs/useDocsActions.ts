import { useCallback, useMemo, useState } from 'react';
import * as api from '../../api';
import type { DocFolder, DocFolderDeletePreview } from '../../api';
import { reportError } from '../../errors';
import { folderAncestry } from '../../lib/docsFilter';

// useDocsActions — every mutation the Documents page can perform on the
// page tree, plus the two dialogs that drive them.
//
// It lives beside the view rather than in a provider because none of this
// state is global: it is one screen's create/rename/delete/move machine.
// What it owns is the pending-dialog state and the api round trip; the view
// keeps ownership of selection, navigation and the resources being
// refreshed, and passes those in as callbacks.
//
// Two rules from the seam shape the code below and are worth stating up
// front, because getting either wrong fails only at runtime:
//
//   1. **`createDoc` requires a non-empty body.** The HTTP route mirrors
//      `bacio doc add` and 400s on an empty one (an agent creating a blank
//      document is almost always a mistake). "New page" therefore seeds a
//      `# <title>` heading — which is also just the right thing for a page
//      you are about to write in.
//   2. **`''` is the tree root**, not a missing argument. A `folderUuid` /
//      `parentUuid` of `''` files something at the top level, and that is
//      the only way to address it.

// What the name dialog is currently naming.
type PendingName =
  | { kind: 'new-page'; folderUuid: string }
  | { kind: 'new-folder'; parentUuid: string }
  | { kind: 'rename-page'; filename: string }
  | { kind: 'rename-folder'; uuid: string; name: string };

// What the delete dialog is currently confirming. A folder carries the
// dry-run preview so the dialog can state the real blast radius.
type PendingDelete =
  | { kind: 'page'; filename: string }
  | { kind: 'folder'; uuid: string; name: string; preview: DocFolderDeletePreview | null };

export type NameDialogState = {
  open: boolean;
  title: string;
  label: string;
  placeholder: string;
  hint: string;
  initialValue: string;
  submitLabel: string;
  busyLabel: string;
  busy: boolean;
  error: string;
  onSubmit: (value: string) => void;
  onClose: () => void;
};

export type DeleteDialogState = {
  open: boolean;
  title: string;
  subject: string;
  lines: string[];
  busy: boolean;
  error: string;
  // confirmDisabled holds the destructive button back while `lines` is
  // still a placeholder. A folder delete opens on a dry run, so for one
  // round trip the dialog can only say "Checking what this would
  // remove…" — and a confirmation whose stated consequence is still
  // loading is a confirmation of nothing. Same gate the Kanban lane
  // delete applies (LaneDeleteDialog), and it matters more here: the
  // folder's subtree really is deleted, where a lane only unsets
  // membership.
  confirmDisabled: boolean;
  onConfirm: () => void;
  onClose: () => void;
};

export type DocsActions = {
  nameDialog: NameDialogState;
  deleteDialog: DeleteDialogState;
  requestNewPage: (folderUuid: string) => void;
  requestNewFolder: (parentUuid: string) => void;
  requestRenameDoc: (filename: string) => void;
  requestRenameFolder: (uuid: string) => void;
  requestDeleteDoc: (filename: string) => void;
  requestDeleteFolder: (uuid: string) => void;
  moveDoc: (filename: string, folderUuid: string, position: number | null) => void;
  moveFolder: (uuid: string, newParentUuid: string) => void;
};

type UseDocsActionsArgs = {
  repo: string;
  folders: DocFolder[];
  refreshDocs: () => void;
  refreshFolders: () => void;
  // Open a page in the editor (navigates — the URL is the source of truth
  // for the open document).
  openDoc: (filename: string) => void;
  // Clear the editor back to the empty state (after deleting what's open).
  clearSelection: () => void;
  // Select a folder, i.e. open its folder page.
  selectFolder: (uuid: string) => void;
  selectedDoc: string | null;
  selectedFolder: string | null;
  expandFolders: (uuids: string[]) => void;
};

function messageOf(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  const s = String(err ?? '');
  return s || 'Something went wrong';
}

// A page name typed as "Sync design" becomes "sync-design.md" on disk while
// "notes.md" is taken verbatim — a filename is a filename, but nobody wants
// to type an extension to make a note.
export function pageFilename(input: string): string {
  const trimmed = input.trim();
  if (/\.[A-Za-z0-9]{1,8}$/.test(trimmed)) return trimmed;
  const slug = trimmed.replace(/\s+/g, '-');
  return `${slug}.md`;
}

// The seed body. A `# Title` heading is the conventional opening of every
// document bacio writes, and it satisfies createDoc's non-empty rule
// honestly rather than with filler.
export function seedBody(filename: string): string {
  const title = filename.replace(/\.[A-Za-z0-9]{1,8}$/, '').replace(/[-_]+/g, ' ').trim();
  return `# ${title || filename}\n`;
}

export function useDocsActions({
  repo,
  folders,
  refreshDocs,
  refreshFolders,
  openDoc,
  clearSelection,
  selectFolder,
  selectedDoc,
  selectedFolder,
  expandFolders,
}: UseDocsActionsArgs): DocsActions {
  const [pendingName, setPendingName] = useState<PendingName | null>(null);
  const [pendingDelete, setPendingDelete] = useState<PendingDelete | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const closeName = useCallback(() => {
    setPendingName(null);
    setError('');
  }, []);
  const closeDelete = useCallback(() => {
    setPendingDelete(null);
    setError('');
  }, []);

  const open = useCallback((p: PendingName) => {
    setError('');
    setPendingName(p);
  }, []);

  const requestNewPage = useCallback((folderUuid: string) => open({ kind: 'new-page', folderUuid }), [open]);
  const requestNewFolder = useCallback((parentUuid: string) => open({ kind: 'new-folder', parentUuid }), [open]);
  const requestRenameDoc = useCallback((filename: string) => open({ kind: 'rename-page', filename }), [open]);
  const requestRenameFolder = useCallback(
    (uuid: string) => {
      const f = folders.find(x => x.uuid === uuid);
      open({ kind: 'rename-folder', uuid, name: f?.name ?? '' });
    },
    [folders, open],
  );

  const requestDeleteDoc = useCallback((filename: string) => {
    setError('');
    setPendingDelete({ kind: 'page', filename });
  }, []);

  // The folder delete opens on the dry run: previewDeleteDocFolder reports
  // what would go without deleting anything, so the dialog can be specific
  // instead of scary. A failed preview surfaces globally and opens nothing —
  // confirming a delete whose blast radius we couldn't read would be worse.
  const requestDeleteFolder = useCallback(
    (uuid: string) => {
      const f = folders.find(x => x.uuid === uuid);
      setError('');
      setPendingDelete({ kind: 'folder', uuid, name: f?.name ?? '', preview: null });
      api.previewDeleteDocFolder(repo, uuid)
        .then(preview => {
          setPendingDelete(cur => (cur && cur.kind === 'folder' && cur.uuid === uuid ? { ...cur, preview } : cur));
        })
        .catch(err => {
          setPendingDelete(cur => (cur && cur.kind === 'folder' && cur.uuid === uuid ? null : cur));
          reportError(err, { headline: "Couldn't check what deleting this folder would remove" });
        });
    },
    [folders, repo],
  );

  const submitName = useCallback(
    (value: string) => {
      if (!pendingName || busy) return;
      setBusy(true);
      setError('');

      const done = () => setBusy(false);
      const fail = (err: unknown) => {
        setBusy(false);
        // Store refusals — a duplicate filename, a duplicate sibling folder
        // name, an illegal character — stay INLINE next to the input. The
        // global modal would close the dialog and lose what was typed.
        setError(messageOf(err));
      };

      if (pendingName.kind === 'new-page') {
        const filename = pageFilename(value);
        if (filename.includes('/')) {
          setBusy(false);
          setError("Page names can't contain '/' — use folders to organise instead.");
          return;
        }
        api.createDoc(repo, filename, '', seedBody(filename), pendingName.folderUuid)
          .then(() => {
            done();
            setPendingName(null);
            refreshDocs();
            // Reveal it: open every folder above its home, then open the page.
            expandFolders(folderAncestry(folders, pendingName.folderUuid).map(f => f.uuid));
            openDoc(filename);
          })
          .catch(fail);
        return;
      }

      if (pendingName.kind === 'new-folder') {
        api.createDocFolder(repo, value, pendingName.parentUuid)
          .then(folder => {
            done();
            setPendingName(null);
            refreshFolders();
            expandFolders([...folderAncestry(folders, pendingName.parentUuid).map(f => f.uuid), folder.uuid]);
            selectFolder(folder.uuid);
          })
          .catch(fail);
        return;
      }

      if (pendingName.kind === 'rename-page') {
        const filename = pageFilename(value);
        api.renameDoc(repo, pendingName.filename, filename)
          .then(() => {
            done();
            const wasOpen = selectedDoc === pendingName.filename;
            setPendingName(null);
            refreshDocs();
            if (wasOpen) openDoc(filename);
          })
          .catch(fail);
        return;
      }

      api.renameDocFolder(repo, pendingName.uuid, value)
        .then(() => {
          done();
          setPendingName(null);
          refreshFolders();
        })
        .catch(fail);
    },
    [busy, expandFolders, folders, openDoc, pendingName, refreshDocs, refreshFolders, repo, selectFolder, selectedDoc],
  );

  const confirmDelete = useCallback(() => {
    if (!pendingDelete || busy) return;
    setBusy(true);
    setError('');

    if (pendingDelete.kind === 'page') {
      const { filename } = pendingDelete;
      api.deleteDoc(repo, filename)
        .then(() => {
          setBusy(false);
          setPendingDelete(null);
          refreshDocs();
          if (selectedDoc === filename) clearSelection();
        })
        .catch(err => {
          setBusy(false);
          setError(messageOf(err));
        });
      return;
    }

    const { uuid } = pendingDelete;
    api.deleteDocFolder(repo, uuid)
      .then(() => {
        setBusy(false);
        setPendingDelete(null);
        refreshFolders();
        // Pages in the subtree were re-rooted, not destroyed — their rows
        // changed folder, so the doc list needs re-reading too.
        refreshDocs();
        if (selectedFolder === uuid) clearSelection();
      })
      .catch(err => {
        setBusy(false);
        setError(messageOf(err));
      });
  }, [busy, clearSelection, pendingDelete, refreshDocs, refreshFolders, repo, selectedDoc, selectedFolder]);

  // Drag-driven moves have no dialog: they're reversible by dragging back,
  // so a failure is the only thing worth interrupting for.
  const moveDoc = useCallback(
    (filename: string, folderUuid: string, position: number | null) => {
      api.moveDocToFolder(repo, filename, folderUuid, position)
        .then(refreshDocs)
        .catch(err => reportError(err, { headline: "Couldn't move page" }));
    },
    [refreshDocs, repo],
  );

  const moveFolder = useCallback(
    (uuid: string, newParentUuid: string) => {
      api.moveDocFolder(repo, uuid, newParentUuid)
        .then(refreshFolders)
        .catch(err => reportError(err, { headline: "Couldn't move folder" }));
    },
    [refreshFolders, repo],
  );

  const nameDialog = useMemo<NameDialogState>(() => {
    const base = {
      open: !!pendingName,
      busy,
      error,
      onSubmit: submitName,
      onClose: closeName,
    };
    switch (pendingName?.kind) {
      case 'new-folder':
        return {
          ...base,
          title: pendingName.parentUuid ? 'New subfolder' : 'New folder',
          label: 'Folder name',
          placeholder: 'Architecture',
          hint: 'Folders organise pages. Sibling folders need distinct names.',
          initialValue: '',
          submitLabel: 'Create folder',
          busyLabel: 'Creating…',
        };
      case 'rename-page':
        return {
          ...base,
          title: 'Rename page',
          label: 'Page name',
          placeholder: 'sync-across-machines.md',
          hint: "Filenames are unique across the whole space, and can't contain '/'.",
          initialValue: pendingName.filename,
          submitLabel: 'Rename',
          busyLabel: 'Renaming…',
        };
      case 'rename-folder':
        return {
          ...base,
          title: 'Rename folder',
          label: 'Folder name',
          placeholder: 'Architecture',
          hint: 'Pages and subfolders keep their place.',
          initialValue: pendingName.name,
          submitLabel: 'Rename',
          busyLabel: 'Renaming…',
        };
      default:
        return {
          ...base,
          title: 'New page',
          label: 'Page name',
          placeholder: 'sync-across-machines',
          hint: "Saved as a .md file. Filenames are unique across the whole space, and can't contain '/'.",
          initialValue: '',
          submitLabel: 'Create page',
          busyLabel: 'Creating…',
        };
    }
  }, [busy, closeName, error, pendingName, submitName]);

  const deleteDialog = useMemo<DeleteDialogState>(() => {
    const base = {
      open: !!pendingDelete,
      busy,
      error,
      onConfirm: confirmDelete,
      onClose: closeDelete,
    };
    if (pendingDelete?.kind === 'folder') {
      const p = pendingDelete.preview;
      return {
        ...base,
        title: 'Delete folder',
        subject: p?.path || pendingDelete.name,
        // Nothing to confirm until the dry run lands.
        confirmDisabled: p === null,
        lines: p
          ? [
              p.subfolders === 1
                ? '1 subfolder is deleted with it.'
                : `${p.subfolders} subfolders are deleted with it.`,
              p.documentsReRooted === 1
                ? '1 page moves back to the top level — no page is deleted.'
                : `${p.documentsReRooted} pages move back to the top level — no page is deleted.`,
            ]
          : ['Checking what this would remove…'],
      };
    }
    return {
      ...base,
      title: 'Delete page',
      subject: pendingDelete?.kind === 'page' ? pendingDelete.filename : '',
      // A page delete has no preview to wait on — its consequences are
      // fixed and stated below.
      confirmDisabled: false,
      lines: [
        'The body and every link pointing at it are removed permanently.',
        'Archive instead if you only want it out of the way.',
      ],
    };
  }, [busy, closeDelete, confirmDelete, error, pendingDelete]);

  return useMemo(
    () => ({
      nameDialog,
      deleteDialog,
      requestNewPage,
      requestNewFolder,
      requestRenameDoc,
      requestRenameFolder,
      requestDeleteDoc,
      requestDeleteFolder,
      moveDoc,
      moveFolder,
    }),
    [
      nameDialog,
      deleteDialog,
      requestNewPage,
      requestNewFolder,
      requestRenameDoc,
      requestRenameFolder,
      requestDeleteDoc,
      requestDeleteFolder,
      moveDoc,
      moveFolder,
    ],
  );
}
