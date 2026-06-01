import React from 'react';
import { useNavigate, useLocation } from 'react-router';
import { viewPath, monitorTranscriptsPath } from '../lib/routes';
import NetworkPanel from './NetworkPanel';
import TranscriptListPanel from './TranscriptListPanel';

// SUBTABS is the canonical ordered list the segmented control renders. `id`
// keys the active state; `label` is the chip text; `path` builds the route the
// chip navigates to under the active repo prefix.
const SUBTABS = [
  { id: 'network', label: 'Network', path: (prefix) => viewPath(prefix, 'monitor') },
  { id: 'transcripts', label: 'Transcripts', path: (prefix) => monitorTranscriptsPath(prefix) },
];

// MonitorView is the BACI-322 Monitor screen shell — one Topbar entry, two
// URL-synced sub-tabs (Network | Transcripts). The Network sub-tab is today's
// raw per-FQDN traffic table + capture drill-down (lifted verbatim into
// NetworkPanel); the Transcripts sub-tab is the new searchable per-dispatch
// transcript browser. The active sub-tab is derived from the URL — `/monitor`
// is Network, `/monitor/transcripts` is Transcripts — so a deep-link / refresh
// lands on the right tab and the segmented control mirrors the location
// (SettingsView's one-entry / URL-synced-sections model the app already uses).
export default function MonitorView({ activeBoard }) {
  const navigate = useNavigate();
  const location = useLocation();

  const active = location.pathname.endsWith('/transcripts') ? 'transcripts' : 'network';

  return (
    <div className="mk-monitor">
      <header className="mk-monitor-bar">
        <h2 className="mk-monitor-title">Monitor</h2>
        <div className="mk-monitor-subtabs" role="tablist" aria-label="Monitor sections">
          {SUBTABS.map(tab => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              aria-selected={active === tab.id}
              className={`mk-monitor-subtab${active === tab.id ? ' is-active' : ''}`}
              onClick={() => navigate(tab.path(activeBoard))}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </header>

      {active === 'transcripts'
        ? <TranscriptListPanel activeBoard={activeBoard} />
        : <NetworkPanel />}
    </div>
  );
}
