import { FilePlus2, FolderPlus, GitBranch, Layers, PanelLeftClose, Plus, Search, X } from 'lucide-react';
import DocsList from '../DocsList';
import DocsFacetRail from '../DocsFacetRail';
import type { SpaceOption } from '../DocsFacetRail';
import DocsTreeNode from './DocsTreeNode';
import type { DocsTreeActions } from './DocsTreeNode';
import type { DocsTreeDrag } from './useDocsTreeDrag';
import type { Doc, DocsCounts, DocsQuery, DocTreeNode } from '../../lib/docsFilter';
import type { RepoKind } from '../../api';

// DocsTreeRail — the single left rail of the redesigned Documents page.
//
// It replaces BOTH pre-pivot left panes (the facet rail and the document
// list), which is what buys the editor its width: 296px of chrome instead of
// 654px, so the TipTap canvas sits at its designed 900px measure with air
// either side on a 1440-wide laptop.
//
// Top to bottom: a head strip (new folder / new page / collapse), the search
// box, the `Tree | All docs` mode switch, the space banner, the rail body,
// and a foot carrying the demoted facet fold plus the New page button.
//
// **The body has two modes and the user is usually not the one choosing.**
// `mode` is derived by the parent from `shouldFlatten(query)`: typing in the
// search box or activating a facet auto-flips the body to the flat ranked
// list (DocsList, kept whole), and clearing it flips back to the tree. The
// segmented switch is the manual override for the moments the automatic
// choice is wrong. This is what keeps the rail usable in a repo with 200+
// auto-generated session retros — a tree is the wrong shape for those, and
// without the flip the rail would be a wall.

export type RailMode = 'tree' | 'flat';

type DocsTreeRailProps = {
  // ─ head ─
  onNewPage: () => void;
  onNewFolder: () => void;
  onCollapse: () => void;
  // ─ search + mode ─
  query: DocsQuery;
  onQueryChange: (patch: Partial<DocsQuery>) => void;
  mode: RailMode;
  onModeChange: (mode: RailMode) => void;
  totalDocs: number;
  // ─ space banner ─
  spaceName: string;
  spaceKind: RepoKind;
  // ─ tree body ─
  tree: DocTreeNode[];
  expanded: Set<string>;
  onToggleFolder: (uuid: string) => void;
  selectedDoc: string | null;
  selectedFolder: string | null;
  actions: DocsTreeActions;
  drag: DocsTreeDrag;
  // ─ flat body (DocsList, verbatim) ─
  visible: Doc[];
  hasDocs: boolean;
  // ─ foot ─
  counts: DocsCounts;
  spaces: SpaceOption[];
  activeSpace: string;
  onPickSpace: (prefix: string) => void;
};

export default function DocsTreeRail({
  onNewPage,
  onNewFolder,
  onCollapse,
  query,
  onQueryChange,
  mode,
  onModeChange,
  totalDocs,
  spaceName,
  spaceKind,
  tree,
  expanded,
  onToggleFolder,
  selectedDoc,
  selectedFolder,
  actions,
  drag,
  visible,
  hasDocs,
  counts,
  spaces,
  activeSpace,
  onPickSpace,
}: DocsTreeRailProps) {
  const isWorkspace = spaceKind === 'workspace';
  const rootDrop = drag.rootDropProps();

  return (
    <aside className="mk-docs-tree-rail">
      <div className="mk-docs-tree-head">
        <span className="mk-docs-tree-title">Documents</span>
        <button
          type="button"
          className="mk-icbtn mk-docs-tree-headbtn"
          onClick={onNewFolder}
          title="New folder"
          aria-label="New folder"
        >
          <FolderPlus size={14} strokeWidth={2} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="mk-icbtn mk-docs-tree-headbtn"
          onClick={onNewPage}
          title="New page"
          aria-label="New page"
        >
          <FilePlus2 size={14} strokeWidth={2} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="mk-icbtn mk-docs-tree-headbtn"
          onClick={onCollapse}
          title="Hide sidebar"
          aria-label="Hide sidebar"
        >
          <PanelLeftClose size={14} strokeWidth={2} aria-hidden="true" />
        </button>
      </div>

      {/* The search box stays visible rather than moving behind a ⌘K
          palette: hiding it would take the glanceable facet counts with it,
          which was the one real regression in the palette-first design. */}
      <label className="mk-features-search mk-docs-tree-search">
        <Search className="mk-features-search-icon" strokeWidth={2} aria-hidden="true" />
        <input
          type="search"
          className="mk-features-search-input"
          value={query.search}
          onChange={(e) => onQueryChange({ search: e.target.value })}
          placeholder="Search pages"
          aria-label="Search pages"
        />
        {query.search && (
          <button
            type="button"
            className="mk-features-search-clear"
            aria-label="Clear search"
            onClick={() => onQueryChange({ search: '' })}
          >
            <X strokeWidth={2} aria-hidden="true" />
          </button>
        )}
      </label>

      <div className="mk-docs-tree-mode" role="tablist" aria-label="Rail mode">
        <button
          type="button"
          role="tab"
          aria-selected={mode === 'tree'}
          className={mode === 'tree' ? 'is-active' : ''}
          onClick={() => onModeChange('tree')}
        >
          Tree
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === 'flat'}
          className={mode === 'flat' ? 'is-active' : ''}
          onClick={() => onModeChange('flat')}
        >
          All docs <span className="mk-docs-tree-mode-count">{totalDocs}</span>
        </button>
      </div>

      {/* The at-a-glance git-vs-workspace signal. The Space facet group in
          the fold below is the switcher; this is the "where am I". */}
      <div className={`mk-docs-space-banner ${isWorkspace ? 'is-workspace' : ''}`}>
        {isWorkspace
          ? <Layers size={14} strokeWidth={2} aria-hidden="true" />
          : <GitBranch size={14} strokeWidth={2} aria-hidden="true" />}
        <span className="mk-docs-space-name" title={spaceName}>{spaceName}</span>
        <span className="mk-pill mk-docs-space-pill">{isWorkspace ? 'workspace' : 'git'}</span>
      </div>

      {mode === 'tree' ? (
        // The scroll container doubles as the root drop zone: dragging a
        // page or folder onto the empty space below the tree files it at the
        // top level.
        <div className="mk-docs-tree-scroll" {...rootDrop}>
          {tree.length === 0 ? (
            <div className="mk-docs-list-empty">
              {hasDocs ? 'No pages match the current filters.' : 'No documents in this space yet.'}
            </div>
          ) : (
            <ul className="mk-docs-tree" role="tree" aria-label="Page tree">
              {tree.map((node, i) => (
                <DocsTreeNode
                  key={node.kind === 'folder' ? `f:${node.uuid}` : `d:${node.filename}`}
                  node={node}
                  index={i}
                  parentUuid=""
                  expanded={expanded}
                  onToggle={onToggleFolder}
                  selectedDoc={selectedDoc}
                  selectedFolder={selectedFolder}
                  actions={actions}
                  drag={drag}
                />
              ))}
            </ul>
          )}
        </div>
      ) : (
        // DocsList survives whole — sticky toolbar, sort dropdown and the
        // three-line rich rows all come along. It stopped being the default
        // surface; it did not stop being the right one for a ranked result.
        <DocsList
          visible={visible}
          hasDocs={hasDocs}
          query={query}
          onQueryChange={onQueryChange}
          selected={selectedDoc}
          onSelect={actions.onSelectDoc}
        />
      )}

      <div className="mk-docs-rail-foot">
        <DocsFacetRail
          counts={counts}
          query={query}
          onQueryChange={onQueryChange}
          spaces={spaces}
          activeSpace={activeSpace}
          onPickSpace={onPickSpace}
        />
        <button type="button" className="mk-docs-rail-new" onClick={onNewPage}>
          <Plus size={14} strokeWidth={2} aria-hidden="true" />
          <span>New page</span>
        </button>
      </div>
    </aside>
  );
}
