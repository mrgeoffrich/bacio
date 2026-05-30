import React from 'react';

// RelationsPanel (BACI-114) is the issue-workspace rail's full
// relations surface — Blocked by / Blocking / Relates to / Duplicate
// of, each rendered as a group of clickable chips that navigate to
// the related issue.
//
// Option A from the design doc: chip enrichment is a client-side join
// over the same `cards` array the App already polls. A target on a
// different board / archived-and-hidden / not-on-the-current-board
// renders as a degraded chip (bare key, neutral "?" pill) — still
// clickable; the workspace brief fetch resolves the title on the
// follow-up. No async path of its own, so no error / loading branch.
//
// Renders nothing when every relation group is empty so the rail
// stays quiet on issues with no edges.

const TYPE_LABEL = {
  blocked_by: 'Blocked by',
  blocking: 'Blocking',
  relates_to: 'Relates to',
  duplicate_of: 'Duplicate of',
};

// stateLabel mirrors api.http.ts:STATE_LABELS / KanbanCard.jsx
// (single source of truth lives in api.http.ts; duplicated here so a
// degraded chip doesn't need an extra prop drill). Keep in sync.
const STATE_LABELS = {
  todo: 'Todo',
  in_review: 'In Review',
  done: 'Done',
  cancelled: 'Cancelled',
  in_pipeline: 'In Pipeline',
  to_be_shipped: 'To Be Shipped',
};
function stateLabel(s) {
  return STATE_LABELS[s] ?? s;
}

export default function RelationsPanel({ relations, cards, onNavigate }) {
  if (!relations) return null;

  // Fold the brief's outgoing + incoming arrays into four label-
  // relative groups. A `blocks` outgoing edge becomes "Blocking";
  // a `blocks` incoming edge becomes "Blocked by". `relates_to` and
  // `duplicate_of` are symmetric — fold both directions into the
  // outgoing label.
  const groups = {
    blocked_by: [],
    blocking: [],
    relates_to: [],
    duplicate_of: [],
  };
  for (const r of relations.outgoing || []) {
    if (r.type === 'blocks') groups.blocking.push(r.otherKey);
    else if (r.type === 'relates_to') groups.relates_to.push(r.otherKey);
    else if (r.type === 'duplicate_of') groups.duplicate_of.push(r.otherKey);
  }
  for (const r of relations.incoming || []) {
    if (r.type === 'blocks') groups.blocked_by.push(r.otherKey);
    else if (r.type === 'relates_to') groups.relates_to.push(r.otherKey);
    else if (r.type === 'duplicate_of') groups.duplicate_of.push(r.otherKey);
  }

  const total =
    groups.blocked_by.length +
    groups.blocking.length +
    groups.relates_to.length +
    groups.duplicate_of.length;
  if (total === 0) return null;

  const cardByKey = new Map((cards || []).map(c => [c.key, c]));

  const renderGroup = (label, keys) => {
    if (keys.length === 0) return null;
    return (
      <div className="mk-rel-group" key={label}>
        <div className="mk-rel-group-label">{label}</div>
        <ul className="mk-rel-chips">
          {keys.map(k => {
            const c = cardByKey.get(k);
            return (
              <li key={k}>
                <button
                  type="button"
                  className="mk-rel-chip"
                  onClick={() => onNavigate?.(k)}
                  aria-label={c ? `${k} ${c.title}` : `${k} (not on current board)`}
                >
                  <span className="mk-card-id">{k}</span>
                  {c ? (
                    <>
                      <span className={`mk-pill mk-status-${c.column}`}>{c.columnLabel ?? stateLabel(c.column)}</span>
                      <span className="mk-rel-chip-title">{c.title}</span>
                    </>
                  ) : (
                    <span className="mk-pill mk-status-unknown" title="Issue not on current board">?</span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      </div>
    );
  };

  return (
    <section className="mk-drawer-section">
      <div className="mk-drawer-label">Relations</div>
      {renderGroup(TYPE_LABEL.blocked_by, groups.blocked_by)}
      {renderGroup(TYPE_LABEL.blocking, groups.blocking)}
      {renderGroup(TYPE_LABEL.relates_to, groups.relates_to)}
      {renderGroup(TYPE_LABEL.duplicate_of, groups.duplicate_of)}
    </section>
  );
}
