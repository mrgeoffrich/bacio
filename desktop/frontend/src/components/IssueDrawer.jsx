import React from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import ReactMarkdown from 'react-markdown';
import Icon from './Icon.jsx';

// prLabel shortens a GitHub PR URL to "owner/repo#N" when it matches the
// familiar shape; anything else falls back to the raw URL.
function prLabel(url) {
  const m = url.match(/github\.com\/([^/]+)\/([^/]+)\/pull\/(\d+)/);
  return m ? `${m[1]}/${m[2]}#${m[3]}` : url;
}

export default function IssueDrawer({ issue, onClose, onShip, onEdit }) {
  // Dispatching a prompt now happens from the per-card action button on
  // the Board (state-gated, auto-picks a free agent) — the drawer no
  // longer carries any agent-selection UI.
  const hasAttachments = (issue?.pullRequests.length ?? 0) > 0 || (issue?.documents.length ?? 0) > 0;

  return (
    <Dialog.Root open={!!issue} onOpenChange={(open) => { if (!open) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="mk-scrim" />
        <Dialog.Content className="mk-drawer" aria-describedby={undefined}>
          {issue && (
            <>
              <header className="mk-drawer-head">
                <span className="mk-card-id">{issue.key}</span>
                <span className={`mk-pill mk-status-${issue.column}`}>{issue.columnLabel}</span>
                <div style={{ marginLeft: 'auto', display: 'flex', gap: '4px', alignItems: 'center' }}>
                  <button className="mk-btn-secondary" onClick={onEdit}>Edit</button>
                  <Dialog.Close asChild>
                    <button className="mk-icbtn" aria-label="Close"><Icon name="x" /></button>
                  </Dialog.Close>
                </div>
              </header>

              <div className="mk-drawer-body">
                <Dialog.Title className="mk-drawer-title">{issue.title}</Dialog.Title>

                <div className="mk-drawer-meta">
                  <div className="mk-meta-row-grid">
                    <span className="mk-meta-key">Assignees</span>
                    <span className="mk-meta-val">
                      {issue.assignees.length === 0
                        ? <span className="mk-meta-empty">unassigned</span>
                        : issue.assignees.join(', ')}
                    </span>

                    <span className="mk-meta-key">Tags</span>
                    <span className="mk-meta-val">
                      {issue.tags.length > 0
                        ? issue.tags.map(t => <span key={t} className="mk-tag">{t}</span>)
                        : <span className="mk-meta-empty">—</span>}
                    </span>
                  </div>
                </div>

                <section className="mk-drawer-section">
                  <div className="mk-drawer-label">Description</div>
                  {issue.description
                    ? <div className="mk-drawer-text mk-markdown"><ReactMarkdown>{issue.description}</ReactMarkdown></div>
                    : <p className="mk-drawer-text mk-meta-empty">No description.</p>}
                </section>

                {hasAttachments && (
                  <section className="mk-drawer-section">
                    <div className="mk-drawer-label">Attachments</div>
                    <ul className="mk-attachments">
                      {issue.pullRequests.map(p => (
                        <li key={p.url} className="mk-attachment">
                          <span className="mk-attachment-badge">PR</span>
                          <a href={p.url} target="_blank" rel="noreferrer" className="mk-attachment-link">
                            {prLabel(p.url)}
                          </a>
                        </li>
                      ))}
                      {issue.documents.map(d => (
                        <li key={d.filename} className="mk-attachment">
                          <span className="mk-attachment-badge">{d.type || 'doc'}</span>
                          <span className="mk-attachment-name">{d.filename}</span>
                          {d.description && <span className="mk-attachment-why">{d.description}</span>}
                        </li>
                      ))}
                    </ul>
                  </section>
                )}

                {(issue.claimants || []).length > 0 && (
                  <section className="mk-drawer-section">
                    <div className="mk-drawer-label">
                      Claimed by {issue.taken && <span className="mk-pill mk-status-busy">taken</span>}
                    </div>
                    <ul className="mk-claimant-list">
                      {issue.claimants.map((c, i) => (
                        <li key={i} className={`mk-claimant ${c.open ? '' : 'is-released'}`}>
                          <span className="mk-claimant-who">
                            {c.agentName || c.sessionId.slice(0, 12)}
                            <span className="mk-claimant-state">{c.open ? 'open' : 'released'}</span>
                          </span>
                          {c.prompt && <span className="mk-claimant-prompt">{c.prompt}</span>}
                        </li>
                      ))}
                    </ul>
                  </section>
                )}

                <section className="mk-drawer-section">
                  <div className="mk-drawer-label">Activity</div>
                  {issue.comments.length > 0 ? (
                    <ul className="mk-timeline">
                      {issue.comments.map((c, i) => (
                        <li key={i} className="mk-tl-item">
                          <span className={`mk-tl-dot ${c.author === 'claude' ? 'is-claude' : ''}`} />
                          <span className="mk-tl-text"><b>{c.author}</b> {c.body}</span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="mk-drawer-text mk-meta-empty">No comments yet.</p>
                  )}
                </section>
              </div>

              <footer className="mk-drawer-foot">
                {issue.column !== 'done' ? (
                  <button className="mk-btn-secondary" onClick={onShip}>Ship it</button>
                ) : (
                  <Dialog.Close asChild>
                    <button className="mk-btn-secondary">Close</button>
                  </Dialog.Close>
                )}
              </footer>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
