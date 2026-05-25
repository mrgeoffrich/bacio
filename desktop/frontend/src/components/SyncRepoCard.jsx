import React from 'react';
import Tooltip from './Tooltip.jsx';

// formatSyncTime renders an ISO timestamp as a short local string.
// Mirrors Topbar.jsx's formatSyncTime so the per-card subtitle reads
// the same as the topbar pill's hover.
function formatSyncTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

// pillClassFor picks the mk-sync-badge modifier the same way Topbar.jsx
// does — inlined verbatim (rather than lifted to a shared component)
// per the BACI-108 plan: with only two callers, a tiny class-pick
// duplication beats the over-abstraction of <SyncStatusBadge>.
function pillClassFor(entry) {
  let cls = 'mk-pill mk-sync-badge';
  if (entry.inProgress) cls += ' is-syncing';
  else if (entry.lastError) cls += ' is-error';
  return cls;
}

function pillLabelFor(entry) {
  if (entry.inProgress) return 'Syncing…';
  if (entry.lastError) return 'Sync Failed';
  if (entry.lastSyncAt) return 'Sync Enabled';
  return 'Sync Configured';
}

function pillTitleFor(entry) {
  if (entry.inProgress) return 'Background sync in progress';
  if (entry.lastError) return `Last sync failed: ${entry.lastError}`;
  if (entry.lastSyncAt) return `Last synced ${formatSyncTime(entry.lastSyncAt)}`;
  return 'Background sync configured';
}

// SyncRepoCard is a presentational sub-component used by SyncView to
// render one `sync_remotes` registry entry: header (URL-derived label,
// the remote URL in monospace, the status pill), the local clone path,
// and a nested project list. Phantom rows surface a disabled `Link
// local…` button — wired in BACI-112; today the button is muted with a
// "Coming in BACI-112" tooltip.
//
// onLinkPhantom is reserved for BACI-112: when undefined (the v1
// default), the Link button stays disabled.
export default function SyncRepoCard({ entry, onLinkPhantom }) {
  const projects = entry.projects ?? [];
  return (
    <div className="mk-tmpl mk-sync-repo-card">
      <div className="mk-tmpl-head">
        <span className="mk-tmpl-label">
          {entry.label}
          <span className="mk-tmpl-slug"> · <code>{entry.remoteUrl}</code></span>
        </span>
        <Tooltip label={pillTitleFor(entry)}>
          <span className={pillClassFor(entry)}>{pillLabelFor(entry)}</span>
        </Tooltip>
      </div>
      <div className="mk-sync-repo-meta">
        <span className="mk-sync-repo-meta-label">Local clone</span>
        <code className="mk-sync-repo-meta-value">{entry.localPath}</code>
      </div>
      {projects.length === 0 ? (
        <div className="mk-sync-section-empty">No projects in this sync repo yet.</div>
      ) : (
        <ul className="mk-sync-project-list">
          {projects.map(p => {
            const isPhantom = p.status === 'phantom';
            const isAbsent = p.status === 'absent';
            const stateClass = `mk-pill mk-status-${p.status}`;
            return (
              <li
                key={p.prefix}
                className={`mk-sync-project-row${isPhantom || isAbsent ? ' is-muted' : ''}`}
              >
                <span className="mk-sync-project-prefix">{p.prefix}</span>
                <span className="mk-sync-project-name">{p.name || '—'}</span>
                <span className={stateClass}>{p.status}</span>
                {isPhantom && (
                  <Tooltip label="Coming in BACI-112">
                    <span>
                      <button
                        className="mk-tmpl-reset"
                        type="button"
                        disabled
                        onClick={onLinkPhantom ? () => onLinkPhantom(p) : undefined}
                      >
                        Link local…
                      </button>
                    </span>
                  </Tooltip>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
