import type React from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import {
  ChevronDown,
  CornerUpLeft,
  FileText,
  Folder as FolderIcon,
  FolderPlus,
  GripVertical,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash2,
} from 'lucide-react';
import type { DocTreeNode } from '../../lib/docsFilter';
import type { DocsTreeDrag } from './useDocsTreeDrag';

// DocsTreeNode — one row of the Documents page tree, plus its children.
//
// The nested `<ul>` + `border-left` indent guide costs ~13px per level, so
// five levels of nesting still leave ~200px of label inside the 296px rail
// (the measurement the design was chosen on). Rows are `<div>`s rather than
// `<button>`s because each carries its own nested buttons — the twisty and
// the hover `+` / `⋯` affordances — and a button inside a button is invalid
// HTML that browsers resolve by dropping the inner one.

// The row-level commands, bundled into one object so the recursive render
// threads a single stable prop instead of nine.
export type DocsTreeActions = {
  onSelectDoc: (filename: string) => void;
  onSelectFolder: (uuid: string) => void;
  onNewPage: (folderUuid: string) => void;
  onNewFolder: (parentUuid: string) => void;
  onRenameDoc: (filename: string) => void;
  onDeleteDoc: (filename: string) => void;
  onRenameFolder: (uuid: string) => void;
  onDeleteFolder: (uuid: string) => void;
  onMoveDocToRoot: (filename: string) => void;
};

type DocsTreeNodeProps = {
  node: DocTreeNode;
  // Index within the parent's children — the insertion point a drop-before
  // gesture resolves to.
  index: number;
  // The uuid of the folder this node sits in; '' is the tree root.
  parentUuid: string;
  expanded: Set<string>;
  onToggle: (uuid: string) => void;
  selectedDoc: string | null;
  selectedFolder: string | null;
  actions: DocsTreeActions;
  drag: DocsTreeDrag;
};

export default function DocsTreeNode({
  node,
  index,
  parentUuid,
  expanded,
  onToggle,
  selectedDoc,
  selectedFolder,
  actions,
  drag,
}: DocsTreeNodeProps) {
  if (node.kind === 'folder') {
    const isOpen = expanded.has(node.uuid);
    const isActive = selectedFolder === node.uuid;
    const isDragSrc = drag.dragItem?.kind === 'folder' && drag.dragItem.uuid === node.uuid;
    const dropProps = drag.folderDropProps(node.uuid);
    return (
      <li
        className="mk-docs-tree-node"
        role="treeitem"
        aria-expanded={isOpen}
        aria-selected={isActive}
      >
        <div
          className={rowClass('is-folder', isActive, isDragSrc, drag.isDropInto(node.uuid), false)}
          // Selecting a folder opens its folder page — never a dead end.
          onClick={() => actions.onSelectFolder(node.uuid)}
          onKeyDown={(e) => activateOnKey(e, () => actions.onSelectFolder(node.uuid))}
          tabIndex={0}
          {...drag.dragProps({ kind: 'folder', uuid: node.uuid, parentUuid })}
          {...dropProps}
        >
          <span className="mk-docs-tree-grip" aria-hidden="true">
            <GripVertical size={12} strokeWidth={2} />
          </span>
          <button
            type="button"
            className="mk-docs-tree-twisty"
            aria-label={isOpen ? `Collapse ${node.name}` : `Expand ${node.name}`}
            onClick={(e) => { e.stopPropagation(); onToggle(node.uuid); }}
          >
            <ChevronDown
              size={12}
              strokeWidth={2}
              aria-hidden="true"
              className={isOpen ? '' : 'is-collapsed'}
            />
          </button>
          <FolderIcon size={14} strokeWidth={2} aria-hidden="true" className="mk-docs-tree-ico" />
          <span className="mk-docs-tree-label" title={node.name}>{node.name}</span>
          <span className="mk-docs-tree-count">{node.docCount}</span>
          <span className="mk-docs-tree-actions">
            <button
              type="button"
              className="mk-docs-tree-act"
              title={`New page in ${node.name}`}
              aria-label={`New page in ${node.name}`}
              onClick={(e) => { e.stopPropagation(); actions.onNewPage(node.uuid); }}
            >
              <Plus size={12} strokeWidth={2} aria-hidden="true" />
            </button>
            <RowMenu label={`Actions for ${node.name}`}>
              <MenuItem icon={<Plus size={12} strokeWidth={2} />} onSelect={() => actions.onNewPage(node.uuid)}>
                New page here
              </MenuItem>
              <MenuItem icon={<FolderPlus size={12} strokeWidth={2} />} onSelect={() => actions.onNewFolder(node.uuid)}>
                New subfolder
              </MenuItem>
              <MenuItem icon={<Pencil size={12} strokeWidth={2} />} onSelect={() => actions.onRenameFolder(node.uuid)}>
                Rename…
              </MenuItem>
              <MenuItem icon={<Trash2 size={12} strokeWidth={2} />} danger onSelect={() => actions.onDeleteFolder(node.uuid)}>
                Delete…
              </MenuItem>
            </RowMenu>
          </span>
        </div>
        {isOpen && node.children.length > 0 && (
          <ul className="mk-docs-tree-children" role="group">
            {node.children.map((child, i) => (
              <DocsTreeNode
                key={child.kind === 'folder' ? `f:${child.uuid}` : `d:${child.filename}`}
                node={child}
                index={i}
                parentUuid={node.uuid}
                expanded={expanded}
                onToggle={onToggle}
                selectedDoc={selectedDoc}
                selectedFolder={selectedFolder}
                actions={actions}
                drag={drag}
              />
            ))}
          </ul>
        )}
      </li>
    );
  }

  const { doc, filename } = node;
  const isActive = selectedDoc === filename;
  const isDragSrc = drag.dragItem?.kind === 'doc' && drag.dragItem.filename === filename;
  const archived = !!doc.archivedAt;
  const issueKey = (doc.links ?? []).find(l => l.issueKey)?.issueKey ?? '';
  const dropProps = drag.docDropProps(filename, parentUuid, index);
  return (
    <li className="mk-docs-tree-node" role="treeitem" aria-selected={isActive}>
      {drag.isDropBefore(filename) && <div className="mk-docs-tree-dropline" aria-hidden="true" />}
      <div
        className={rowClass('', isActive, isDragSrc, false, archived)}
        onClick={() => actions.onSelectDoc(filename)}
        onKeyDown={(e) => activateOnKey(e, () => actions.onSelectDoc(filename))}
        tabIndex={0}
        {...drag.dragProps({ kind: 'doc', filename, parentUuid })}
        {...dropProps}
      >
        <span className="mk-docs-tree-grip" aria-hidden="true">
          <GripVertical size={12} strokeWidth={2} />
        </span>
        <span className="mk-docs-tree-twisty is-leaf" aria-hidden="true" />
        <FileText size={14} strokeWidth={2} aria-hidden="true" className="mk-docs-tree-ico" />
        <span className="mk-docs-tree-label" title={filename}>{filename}</span>
        {issueKey && <span className="mk-docs-tree-badge is-issue">{issueKey}</span>}
        {archived && <span className="mk-docs-tree-badge is-archived">arch</span>}
        <span className="mk-docs-tree-actions">
          <RowMenu label={`Actions for ${filename}`}>
            <MenuItem icon={<Pencil size={12} strokeWidth={2} />} onSelect={() => actions.onRenameDoc(filename)}>
              Rename…
            </MenuItem>
            {parentUuid !== '' && (
              <MenuItem icon={<CornerUpLeft size={12} strokeWidth={2} />} onSelect={() => actions.onMoveDocToRoot(filename)}>
                Move to top level
              </MenuItem>
            )}
            <MenuItem icon={<Trash2 size={12} strokeWidth={2} />} danger onSelect={() => actions.onDeleteDoc(filename)}>
              Delete…
            </MenuItem>
          </RowMenu>
        </span>
      </div>
    </li>
  );
}

function rowClass(
  base: string,
  active: boolean,
  dragSrc: boolean,
  dropInto: boolean,
  archived: boolean,
): string {
  return [
    'mk-docs-tree-row',
    base,
    active ? 'is-active' : '',
    dragSrc ? 'is-drag-src' : '',
    dropInto ? 'is-drop-into' : '',
    archived ? 'is-archived' : '',
  ].filter(Boolean).join(' ');
}

// The rows are divs (see the header comment), so they need the keyboard
// activation a <button> would have given for free.
function activateOnKey(e: React.KeyboardEvent, run: () => void) {
  if (e.key !== 'Enter' && e.key !== ' ') return;
  e.preventDefault();
  run();
}

type RowMenuProps = {
  label: string;
  children: React.ReactNode;
};

function RowMenu({ label, children }: RowMenuProps) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-docs-tree-act"
          title={label}
          aria-label={label}
          onClick={(e) => e.stopPropagation()}
        >
          <MoreHorizontal size={12} strokeWidth={2} aria-hidden="true" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-docs-menu"
          align="start"
          side="bottom"
          sideOffset={4}
          collisionPadding={8}
          onClick={(e) => e.stopPropagation()}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

type MenuItemProps = {
  icon: React.ReactNode;
  onSelect: () => void;
  danger?: boolean;
  children: React.ReactNode;
};

function MenuItem({ icon, onSelect, danger, children }: MenuItemProps) {
  return (
    <DropdownMenu.Item
      className={`mk-docs-menu-item ${danger ? 'is-danger' : ''}`}
      onSelect={onSelect}
    >
      <span className="mk-docs-menu-item-icon" aria-hidden="true">{icon}</span>
      <span>{children}</span>
    </DropdownMenu.Item>
  );
}
