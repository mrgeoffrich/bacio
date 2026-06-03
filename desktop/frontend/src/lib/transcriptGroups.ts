// BACI-348 — the pure fold behind the Transcripts page's "Group by" control.
// The server returns one flat row per dispatch (api.listJobTranscripts); this
// folds those rows into the collapsible groups TranscriptListPanel renders
// when grouping by supervisor session or subagent identity. Dispatch mode is a
// passthrough sentinel — the panel renders the flat list directly and never
// calls this — so this module only ever folds for 'session' / 'agent'.
//
// Kept a pure, dependency-free function so it can be unit-tested with plain
// Node + assert (transcriptGroups.smoketest.mjs), the same precedent as
// lib/proxyStats.ts.

import type { JobTranscriptRow } from '../api';

// GroupByMode is the segmented control's three positions. 'dispatch' is the
// flat default (today's list); 'session' / 'agent' fold the rows.
export type GroupByMode = 'dispatch' | 'session' | 'agent';

// TranscriptGroup is one collapsed group — a supervisor session or a subagent
// identity — carrying its member rows (newest-first, as the server returned
// them) and the summed/derived header stats. key is the stable group key the
// expanded-set state and React `key` use; label is the human header text.
export type TranscriptGroup = {
  key: string;
  label: string;
  rows: JobTranscriptRow[];
  turnSum: number;
  tokenSum: number;
  lastSeen: string;
  dispatchCount: number;
};

// EMPTY_AGENT_KEY / EMPTY_AGENT_LABEL bucket dispatches with no claude_agent_id
// (the header was absent — e.g. a supervisor-thread capture) when grouping by
// agent, so nothing silently disappears from the list.
export const EMPTY_AGENT_KEY = '';
export const EMPTY_AGENT_LABEL = '(no agent id)';

// at-a-glance token total for one row — input + output, mirroring the flat
// list's totalTokens (cache / thinking detail lives on the full transcript).
function rowTokens(usage: JobTranscriptRow['usage'] | undefined): number {
  if (!usage) return 0;
  return (usage.inputTokens || 0) + (usage.outputTokens || 0);
}

// groupTranscripts folds a flat, newest-first transcript list into collapsible
// groups for the given mode. 'session' keys on sessionId (label = the
// server-enriched sessionLabel, else the short session id, else the raw key);
// 'agent' keys on claudeAgentId (empty → the "(no agent id)" bucket). Groups
// come back in first-appearance order, which preserves the server's
// newest-first ordering since each group's first row is its newest. 'dispatch'
// returns [] — the panel renders the flat list for that mode and never folds.
export function groupTranscripts(rows: JobTranscriptRow[], mode: GroupByMode): TranscriptGroup[] {
  if (mode === 'dispatch') return [];

  const order: string[] = [];
  const byKey = new Map<string, TranscriptGroup>();

  for (const r of rows) {
    let key: string;
    let label: string;
    if (mode === 'session') {
      key = r.sessionId || '';
      label = r.sessionLabel || r.sessionId || '(no session id)';
    } else {
      key = r.claudeAgentId || EMPTY_AGENT_KEY;
      label = r.claudeAgentId || EMPTY_AGENT_LABEL;
    }

    let g = byKey.get(key);
    if (!g) {
      g = { key, label, rows: [], turnSum: 0, tokenSum: 0, lastSeen: r.lastSeen, dispatchCount: 0 };
      byKey.set(key, g);
      order.push(key);
    }
    g.rows.push(r);
    g.turnSum += r.turnCount || 0;
    g.tokenSum += rowTokens(r.usage);
    g.dispatchCount += 1;
    // lastSeen is the group's most-recent capture; rows arrive newest-first so
    // the first row already holds it, but compare defensively in case the
    // server ever reorders.
    if (r.lastSeen > g.lastSeen) g.lastSeen = r.lastSeen;
  }

  return order.map(k => byKey.get(k)!);
}
