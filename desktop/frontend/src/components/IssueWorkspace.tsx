import React, { useEffect, useState, useMemo } from 'react';
import { Link } from 'react-router';
import MarkdownView from '../lib/markdownView';
import Icon from './Icon';
import IssueLockBanner from './issue/IssueLockBanner';
import LinkedDocPanel from './issue/LinkedDocPanel';
import InlineDescriptionEditor from './issue/InlineDescriptionEditor';
import InlineTitleEditor from './issue/InlineTitleEditor';
import CommentComposer from './issue/CommentComposer';
import RelationsPanel from './issue/RelationsPanel';
import PrAttachModal from './issue/PrAttachModal';
import IssueTranscriptList from './issue/IssueTranscriptList';
import { documentPath } from '../lib/routes';
import prLabel from '../lib/prLabel';

// IssueWorkspace is the top-level per-issue screen — replaces the right-
// side drawer + centred edit modal with one routed view. Primary column
// carries the description + linked-doc plan + activity timeline; the
// secondary rail carries feature/assignee/tags metadata, PR attachments,
// and claimants.
//
// All data is in `brief` (an IssueBriefDTO) which the parent App loads
// via api.getIssueBrief and polls every 10s while this view is mounted.
// `brief === null` is the loading skeleton state — render the issue key
// from `openIssueKey` so the head doesn't flash empty during the first
// fetch.
export default function IssueWorkspace({
  activeBoard,
  openIssueKey,
  brief,
  cards,
  onClose,
  onSaveTitle,
  onSaveDescription,
  onAddComment,
  onDeleteComment,
  onCancelWaiting,
  onAttachPR,
  onNavigateIssue,
  onDescEditingChange,
}) {
  // PR attach is behind a modal (BACI-339) — the rail shows a compact
  // "+ Attach PR" button that opens it, so the URL field doesn't occupy
  // rail space unless the user wants to attach.
  const [prModalOpen, setPrModalOpen] = useState(false);

  const issueMeta = brief?.issue;
  const taken = !!brief?.taken;
  // BACI-255: the brief no longer carries a denormalised
  // waitingForClaim boolean — the brief's structured `waitingState`
  // (BACI-145) is the sole signal. A non-null waitingState means the
  // card has an active queued/pending/delivered dispatch and should
  // render as waiting; `taken` still wins so a card with both an open
  // claim and a stale-but-not-yet-acked dispatch renders as taken.
  const waiting = !!brief?.waitingState && !taken;

  // BACI-203: LinkedDocPanel no longer renders bodies inline (it's a
  // link to the canonical /documents/<filename> page now), so the
  // per-event composer that used to live inside the inline transcript
  // view is gone. Eval-flagged comments continue to surface in the
  // main Activity timeline below; the BACI-141 transcript-anchored
  // composer is deferred to the document detail page (follow-up).

  // BACI-145: the brief now carries the structured WaitingState used
  // by the IssueLockBanner's inline label. Fall back to null when the
  // server didn't populate it (a brief read race against the dispatch
  // tables, or pre-BACI-145 payloads — the banner copes).
  const waitingState = brief?.waitingState || null;
  // The most recent open claimant — the holder of an active claim.
  // ClaimantDTO is shaped {sessionId, agentName, prompt, open, ...}.
  const openClaimant = useMemo(() => {
    if (!brief?.claimants) return null;
    return brief.claimants.find(c => c.open) ?? null;
  }, [brief]);

  // Column-scoped prev/next siblings — same column, sorted by key.
  const siblings = useMemo(() => {
    if (!issueMeta || !cards) return [];
    return cards
      .filter(c => c.column === issueMeta.column)
      .slice()
      .sort((a, b) => a.key.localeCompare(b.key));
  }, [cards, issueMeta]);
  const currentIdx = useMemo(
    () => (issueMeta ? siblings.findIndex(c => c.key === issueMeta.key) : -1),
    [siblings, issueMeta],
  );
  const prevSibling = currentIdx > 0 ? siblings[currentIdx - 1] : null;
  const nextSibling = currentIdx >= 0 && currentIdx < siblings.length - 1
    ? siblings[currentIdx + 1]
    : null;

  // [ / ] hotkeys for prev/next within column. esc → close is wired in
  // App.jsx so it can defer to the command palette's own esc handler.
  useEffect(() => {
    const onKey = (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const tag = e.target?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target?.isContentEditable) return;
      if (e.key === '[' && prevSibling) {
        e.preventDefault();
        onNavigateIssue(prevSibling.key);
      } else if (e.key === ']' && nextSibling) {
        e.preventDefault();
        onNavigateIssue(nextSibling.key);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [prevSibling, nextSibling, onNavigateIssue]);

  // Skeleton state — render the head with the bare key so the breadcrumb
  // and screen stay aligned while the first fetch is in flight.
  if (!brief || !issueMeta) {
    return (
      <div className="mk-workspace">
        <header className="mk-workspace-head">
          <span className="mk-card-id">{openIssueKey}</span>
          <span className="mk-pill mk-skeleton-pill" />
          <div style={{ marginLeft: 'auto', display: 'flex', gap: '4px' }}>
            <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
              <Icon name="x" />
            </button>
          </div>
        </header>
        <div className="mk-workspace-body">
          <div className="mk-workspace-primary">
            <div className="mk-skeleton-block" />
            <div className="mk-skeleton-block" />
          </div>
          <aside className="mk-workspace-rail">
            <div className="mk-skeleton-block" />
          </aside>
        </div>
      </div>
    );
  }

  return (
    <div className="mk-workspace">
      <header className="mk-workspace-head">
        <span className="mk-card-id">{issueMeta.key}</span>
        <span className={`mk-pill mk-status-${issueMeta.column}`}>{issueMeta.columnLabel}</span>
        <InlineTitleEditor
          title={issueMeta.title}
          readOnly={taken || waiting}
          onSave={onSaveTitle}
        />
        {/*
          BACI-216: prominent "Open plan" link in the workspace header
          so the plan a worker just attached isn't buried alongside
          review / transcript docs in the Documents list. Mirrors the
          per-card affordance on the kanban; same documentPath() target
          as LinkedDocPanel so BACI-215's deep-link route is the
          landing page. Hidden when no plan-typed doc is linked.
        */}
        {issueMeta.latestPlan && (
          <Link
            to={documentPath(activeBoard, issueMeta.latestPlan.filename)}
            className="mk-workspace-plan-link"
            title={`Open plan: ${issueMeta.latestPlan.filename}`}
          >
            <Icon name="plan" />
            <span className="mk-workspace-plan-link-text">Plan</span>
          </Link>
        )}
        <IssueLockBanner
          taken={taken}
          waiting={waiting}
          waitingState={waitingState}
          claimant={openClaimant}
          onCancelWaiting={onCancelWaiting}
        />
        <div className="mk-workspace-nav">
          <button
            type="button"
            className="mk-icbtn"
            aria-label="Previous issue in column"
            disabled={!prevSibling}
            onClick={() => prevSibling && onNavigateIssue(prevSibling.key)}
            title={prevSibling ? `← ${prevSibling.key}` : 'No previous issue'}
          >
            ‹
          </button>
          <button
            type="button"
            className="mk-icbtn"
            aria-label="Next issue in column"
            disabled={!nextSibling}
            onClick={() => nextSibling && onNavigateIssue(nextSibling.key)}
            title={nextSibling ? `${nextSibling.key} →` : 'No next issue'}
          >
            ›
          </button>
          <button
            type="button"
            className="mk-icbtn"
            aria-label="Close issue"
            onClick={onClose}
            title="Close (esc)"
          >
            <Icon name="x" />
          </button>
        </div>
      </header>

      <div className="mk-workspace-body">
        <main className="mk-workspace-primary">
          <InlineDescriptionEditor
            description={issueMeta.description}
            readOnly={taken || waiting}
            onSave={onSaveDescription}
            onEditingChange={onDescEditingChange}
          />

          {brief.documents.length > 0 && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">Plan &amp; linked docs</div>
              <div className="mk-linked-doc-list">
                {brief.documents.map(d => (
                  <LinkedDocPanel
                    key={`${d.filename}:${(d.linkedVia || []).join('+')}`}
                    doc={d}
                    activeBoard={activeBoard}
                  />
                ))}
              </div>
            </section>
          )}

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Activity</div>
            {brief.comments.length > 0 ? (
              <ul className="mk-timeline">
                {brief.comments.map((c, i) => (
                  <li key={c.uuid || i} className={`mk-tl-item ${c.eval ? 'is-eval' : ''}`}>
                    <span className={`mk-tl-dot ${c.author === 'claude' ? 'is-claude' : ''} ${c.eval ? 'is-eval' : ''}`} />
                    {/* MarkdownView emits block elements (p / ul / pre /
                        table) that mustn't nest inside a <span>, so the
                        outer row is a <div>; the timeline CSS pins the
                        dot to the first line via align-items: flex-start
                        + dot margin-top so a single-line comment still
                        reads the same. */}
                    <div className="mk-tl-text">
                      <b className="mk-tl-author">{c.author}</b>
                      {/* BACI-131: visible marker that this row is a
                          quality-review note posted from the kanban
                          quick-eval composer. */}
                      {c.eval && (
                        <span className="mk-tl-eval-pill" title="Eval / quality-review note">eval</span>
                      )}
                      <MarkdownView className="mk-markdown mk-tl-body">{c.body}</MarkdownView>
                      {c.eval && (c.mode || c.agentName || c.agentSessionId) && (
                        <div className="mk-tl-eval-footer">
                          during: {c.mode || 'no mode'}
                          {(c.agentName || c.agentSessionId) && ' · '}
                          {c.agentName || (c.agentSessionId ? c.agentSessionId.slice(0, 8) : '')}
                        </div>
                      )}
                    </div>
                    {onDeleteComment && c.uuid && (
                      <button
                        type="button"
                        className="mk-tl-delete"
                        title="Delete comment"
                        aria-label="Delete comment"
                        onClick={() => {
                          if (window.confirm('Delete this comment? This cannot be undone.')) {
                            onDeleteComment(c.uuid);
                          }
                        }}
                      >
                        ✕
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mk-drawer-text mk-meta-empty">No comments yet.</p>
            )}
            <CommentComposer onSubmit={onAddComment} />
          </section>
        </main>

        <aside className="mk-workspace-rail">
          {brief.feature && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">Feature</div>
              <span className="mk-tag">{brief.feature.title}</span>
            </section>
          )}

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Assignees</div>
            <div className="mk-meta-val">
              {issueMeta.assignees.length === 0
                ? <span className="mk-meta-empty">unassigned</span>
                : issueMeta.assignees.join(', ')}
            </div>
          </section>

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Tags</div>
            <div className="mk-tag-row">
              {issueMeta.tags.length > 0
                ? issueMeta.tags.map(t => <span key={t} className="mk-tag">{t}</span>)
                : <span className="mk-meta-empty">—</span>}
            </div>
          </section>

          {/*
            BACI-114: full per-edge-type relations rail. Renders nothing
            when the issue has no relations of any kind, so quiet issues
            stay quiet. Sits above Pull requests — relations rank as
            higher-priority context than PRs per the design doc.
          */}
          <RelationsPanel
            relations={brief.relations}
            cards={cards}
            onNavigate={onNavigateIssue}
          />

          <section className="mk-drawer-section">
            <div className="mk-drawer-label">Pull requests</div>
            {brief.pullRequests.length > 0 ? (
              <ul className="mk-attachments">
                {brief.pullRequests.map(p => (
                  <li key={p.url} className="mk-attachment">
                    <span className="mk-attachment-badge">PR</span>
                    <a href={p.url} target="_blank" rel="noreferrer" className="mk-attachment-link">
                      {prLabel(p.url)}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mk-meta-empty">No PR attached.</p>
            )}
            {!(taken || waiting) && (
              <button
                type="button"
                className="mk-btn-secondary mk-pr-attach-btn"
                onClick={() => setPrModalOpen(true)}
              >
                + Attach PR
              </button>
            )}
          </section>

          <IssueTranscriptList activeBoard={activeBoard} issueKey={issueMeta.key} />

          {brief.claimants.length > 0 && (
            <section className="mk-drawer-section">
              <div className="mk-drawer-label">Claim history</div>
              <ul className="mk-claimant-list">
                {brief.claimants.map((c, i) => (
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
        </aside>
      </div>

      <PrAttachModal
        open={prModalOpen}
        onClose={() => setPrModalOpen(false)}
        onAttach={onAttachPR}
      />
    </div>
  );
}
