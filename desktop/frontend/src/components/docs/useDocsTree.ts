import { useCallback, useEffect, useMemo, useState } from 'react';
import { buildTree, flattenTreeDocs, folderAncestry } from '../../lib/docsFilter';
import type { Doc, DocTreeNode, Folder } from '../../lib/docsFilter';
import { readExpandedFolders, persistExpandedFolders } from '../DocsPersistence';

// useDocsTree — the Documents rail's hierarchy state.
//
// Owns three things and nothing else (the tree assembly itself is pure and
// lives in lib/docsFilter.ts, where it is unit-testable without React):
//
//   1. which folders are open, persisted per repo by uuid;
//   2. the derived tree + its render-order page list (what the header's
//      `‹ ›` peer-jump and the footer peer cards step through);
//   3. auto-expanding the chain down to whatever is selected — without it a
//      deep link to a page three folders down would open the editor with a
//      rail that shows no sign of where that page lives.

type UseDocsTreeArgs = {
  // Repo prefix — the persistence scope for the expanded set.
  repo: string;
  // The ALREADY-FILTERED doc list. The tree is a grouping of the same rows
  // the flat mode ranks, so archived/status handling comes along for free.
  docs: Doc[];
  folders: Folder[];
  selectedDoc: string | null;
  selectedFolder: string | null;
};

export type DocsTreeState = {
  tree: DocTreeNode[];
  // Pages in render order — folders first at each level, then pages.
  treeDocs: Doc[];
  expanded: Set<string>;
  toggleFolder: (uuid: string) => void;
  // Open a chain of folders (used when a mutation should reveal its result,
  // e.g. creating a page inside a collapsed folder).
  expandFolders: (uuids: string[]) => void;
};

export function useDocsTree({
  repo,
  docs,
  folders,
  selectedDoc,
  selectedFolder,
}: UseDocsTreeArgs): DocsTreeState {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(readExpandedFolders(repo)));

  // Re-hydrate from the per-repo preference on a repo switch. Persisting is
  // done by the mutators below rather than by an effect on `expanded`, so
  // this hydrate can't immediately write the value it just read back.
  useEffect(() => {
    setExpanded(new Set(readExpandedFolders(repo)));
  }, [repo]);

  const toggleFolder = useCallback(
    (uuid: string) => {
      setExpanded(prev => {
        const next = new Set(prev);
        if (next.has(uuid)) next.delete(uuid);
        else next.add(uuid);
        persistExpandedFolders(repo, Array.from(next));
        return next;
      });
    },
    [repo],
  );

  const expandFolders = useCallback(
    (uuids: string[]) => {
      if (uuids.length === 0) return;
      setExpanded(prev => {
        if (uuids.every(u => prev.has(u))) return prev;
        const next = new Set(prev);
        for (const u of uuids) next.add(u);
        persistExpandedFolders(repo, Array.from(next));
        return next;
      });
    },
    [repo],
  );

  const tree = useMemo(() => buildTree(docs, folders), [docs, folders]);
  const treeDocs = useMemo(() => flattenTreeDocs(tree), [tree]);

  // Reveal the selection: open every ancestor of the selected page's folder
  // (or of the selected folder itself). Runs on selection / folder-list
  // changes only, and only ever ADDS — a user who deliberately collapsed a
  // branch keeps it collapsed until they select into it.
  const selectedDocFolder = useMemo(() => {
    if (!selectedDoc) return null;
    const d = docs.find(x => x.filename === selectedDoc);
    return d ? (d.folderUuid ?? '') : null;
  }, [docs, selectedDoc]);

  useEffect(() => {
    const target = selectedFolder ?? selectedDocFolder;
    if (!target) return;
    const chain = folderAncestry(folders, target).map(f => f.uuid);
    if (chain.length === 0) return;
    expandFolders(chain);
  }, [selectedFolder, selectedDocFolder, folders, expandFolders]);

  return { tree, treeDocs, expanded, toggleFolder, expandFolders };
}
