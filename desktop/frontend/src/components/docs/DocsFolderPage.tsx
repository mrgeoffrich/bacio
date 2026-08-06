import { FileText, Folder as FolderIcon, FolderPlus, Link2, PanelLeftOpen, Pencil, Trash2 } from 'lucide-react';
import { formatWhen } from '../../lib/formatWhen';
import { DocsBreadcrumbs } from './DocsNav';
import type { Crumb } from './DocsNav';
import type { DocTreeNode } from '../../lib/docsFilter';

// DocsFolderPage — the folder-selected surface.
//
// Clicking a folder in the rail must never be a dead end. In a plain file
// browser a folder only expands; here it opens a real page in the editor
// pane carrying a LIVE children index, which is the difference between a
// file browser and Confluence: "where am I / what's in here" gets an answer
// without a middle pane.
//
// The index is derived, not stored. Folders carry no body of their own in
// the store (a DocFolder is `{uuid, name, parentUuid, position, …}`), so
// there is nothing to render through <MarkdownView> here — the page is the
// heading, the summary line, and the table, all built from the same
// already-filtered tree the rail is showing. Drag a page into this folder
// and the row appears on the next list refresh with no edit.

function typeLabel(t: string | null | undefined): string {
  return t ? t.replace(/_/g, ' ') : '';
}

type DocsFolderPageProps = {
  folderUuid: string;
  folderName: string;
  crumbs: Crumb[];
  // The folder's immediate children, in the rail's render order. Named
  // `nodes` rather than `children` so it can't be confused with React's
  // slot — this is data, not markup.
  nodes: DocTreeNode[];
  onSelectDoc: (filename: string) => void;
  onSelectFolder: (uuid: string) => void;
  onNewPage: (folderUuid: string) => void;
  onNewFolder: (parentUuid: string) => void;
  onRenameFolder: (uuid: string) => void;
  onDeleteFolder: (uuid: string) => void;
  onOpenIssue: (issueKey: string) => void;
  // BACI-234 parity: while the rail is collapsed the editor pane is the only
  // host for the re-open affordance.
  panelsCollapsed: boolean;
  onExpandPanels: () => void;
};

export default function DocsFolderPage({
  folderUuid,
  folderName,
  crumbs,
  nodes,
  onSelectDoc,
  onSelectFolder,
  onNewPage,
  onNewFolder,
  onRenameFolder,
  onDeleteFolder,
  onOpenIssue,
  panelsCollapsed,
  onExpandPanels,
}: DocsFolderPageProps) {
  const folders = nodes.filter((c): c is Extract<DocTreeNode, { kind: 'folder' }> => c.kind === 'folder');
  const pages = nodes.filter((c): c is Extract<DocTreeNode, { kind: 'doc' }> => c.kind === 'doc');

  return (
    <>
      <header className="mk-docs-viewer-header">
        {panelsCollapsed && (
          <button
            type="button"
            className="mk-icbtn mk-docs-panels-expand"
            onClick={onExpandPanels}
            title="Show the page tree"
            aria-label="Show the page tree"
          >
            <PanelLeftOpen size={14} strokeWidth={2} aria-hidden="true" />
          </button>
        )}
        <DocsBreadcrumbs crumbs={crumbs} />
        <span className="mk-docs-bar-status">
          {summaryLine(folders.length, pages.length)}
        </span>
        <div className="mk-docs-bar-actions">
          <button
            type="button"
            className="mk-btn-secondary"
            onClick={() => onRenameFolder(folderUuid)}
            title="Rename this folder"
          >
            <Pencil size={14} strokeWidth={2} aria-hidden="true" />
            <span>Rename</span>
          </button>
          <button
            type="button"
            className="mk-btn-secondary"
            onClick={() => onDeleteFolder(folderUuid)}
            title="Delete this folder"
          >
            <Trash2 size={14} strokeWidth={2} aria-hidden="true" />
            <span>Delete</span>
          </button>
          <button
            type="button"
            className="mk-btn-secondary"
            onClick={() => onNewFolder(folderUuid)}
            title="New subfolder"
          >
            <FolderPlus size={14} strokeWidth={2} aria-hidden="true" />
            <span>New folder</span>
          </button>
          {/* The primary action lives HERE as well as in the rail foot —
              the moment you land on an empty folder is the moment you most
              want to write in it. */}
          <button
            type="button"
            className="mk-btn-primary"
            onClick={() => onNewPage(folderUuid)}
          >
            New page
          </button>
        </div>
      </header>

      <div className="mk-docs-editor">
        <article className="mk-docs-folder-page">
          <h1 className="mk-docs-folder-title">{folderName}</h1>
          <p className="mk-docs-folder-lead">{leadLine(folders.length, pages.length)}</p>

          <section className="mk-docs-folder-index">
            <div className="mk-docs-folder-index-head">
              <FolderIcon size={13} strokeWidth={2} aria-hidden="true" />
              <span className="mk-docs-folder-index-title">Children</span>
              <span className="mk-docs-folder-index-live">live</span>
            </div>
            {nodes.length === 0 ? (
              <div className="mk-docs-folder-empty">
                <p>Nothing filed here yet.</p>
                <button
                  type="button"
                  className="mk-btn-primary"
                  onClick={() => onNewPage(folderUuid)}
                >
                  New page
                </button>
              </div>
            ) : (
              <table className="mk-docs-folder-table">
                <thead>
                  <tr>
                    <th className="col-title">Title</th>
                    <th className="col-type">Type</th>
                    <th className="col-when">Updated</th>
                    <th className="col-issue">Issue</th>
                  </tr>
                </thead>
                <tbody>
                  {folders.map((f) => (
                    <tr key={`f:${f.uuid}`}>
                      <td>
                        <button
                          type="button"
                          className="mk-docs-folder-link is-folder"
                          onClick={() => onSelectFolder(f.uuid)}
                        >
                          <FolderIcon size={13} strokeWidth={2} aria-hidden="true" />
                          <span>{f.name}</span>
                        </button>
                      </td>
                      <td>folder · {f.docCount}</td>
                      <td className="mk-docs-folder-when" />
                      <td />
                    </tr>
                  ))}
                  {pages.map((p) => {
                    const issueKey = (p.doc.links ?? []).find(l => l.issueKey)?.issueKey ?? '';
                    return (
                      <tr key={`d:${p.filename}`}>
                        <td>
                          <button
                            type="button"
                            className="mk-docs-folder-link"
                            onClick={() => onSelectDoc(p.filename)}
                          >
                            <FileText size={13} strokeWidth={2} aria-hidden="true" />
                            <span>{p.filename}</span>
                          </button>
                        </td>
                        <td>{typeLabel(p.doc.type)}</td>
                        <td className="mk-docs-folder-when">
                          {p.doc.updatedAt ? formatWhen(p.doc.updatedAt) : ''}
                        </td>
                        <td>
                          {issueKey && (
                            <button
                              type="button"
                              className="mk-docs-link-chip mk-docs-link-chip-issue"
                              onClick={() => onOpenIssue(issueKey)}
                              title={issueKey}
                            >
                              <Link2 size={12} strokeWidth={2} aria-hidden="true" />
                              <span>{issueKey}</span>
                            </button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </section>
        </article>
      </div>
    </>
  );
}

function summaryLine(folderCount: number, pageCount: number): string {
  const parts: string[] = [];
  parts.push(`${pageCount} ${pageCount === 1 ? 'page' : 'pages'}`);
  if (folderCount > 0) {
    parts.push(`${folderCount} ${folderCount === 1 ? 'subfolder' : 'subfolders'}`);
  }
  return parts.join(' · ');
}

function leadLine(folderCount: number, pageCount: number): string {
  if (folderCount === 0 && pageCount === 0) {
    return 'This folder is empty. Pages you create here — or drag in from the tree — show up in the index below.';
  }
  return `A live index of everything filed directly under this folder — ${summaryLine(folderCount, pageCount)}.`;
}
