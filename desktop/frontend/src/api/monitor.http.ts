// Monitor-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, callText } from './client.http';
import { reshapeProxyStat, reshapeProxyCapture, reshapeJobTranscript } from './wire/proxy';
import type { ApiProxyFQDNStat, ApiProxyCaptureRow, ApiJobTranscriptRow } from './wire/proxy';

import type { ProxyFQDNStat } from '../lib/proxyStats';
import type { ProxyCaptureRow, ProxyMessage, AnthropicTranscript } from '../lib/proxyCaptures';
import type { JobTranscriptRow } from '../lib/proxyCaptures';
export type { ProxyFQDNStat };
export type { ProxyCaptureRow, ProxyMessage, AnthropicTranscript };
export type { JobTranscriptRow };

// listProxyStats (BACI-304) is the HTTP twin of api.ts's listProxyStats —
// the Monitor screen's per-FQDN reverse-proxy rollup. GET /proxy/stats is
// cross-cutting (no repo_id), so it takes no repo prefix. `sinceDays === 0`
// is the "All-time" sentinel (no ?since= parameter); a positive value maps
// onto the server's rolling-duration `?since=Nd` lookback, mirroring the
// Shipped pill's convention. Reshapes the snake_case wire rows into the
// camelCase ProxyFQDNStat shape api.ts re-exports.
export async function listProxyStats(sinceDays = 0): Promise<ProxyFQDNStat[]> {
  const query: Record<string, string | number> = {};
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiProxyFQDNStat[]>('/proxy/stats', { query });
  return (rows ?? []).map(reshapeProxyStat);
}

// listProxyCaptures (BACI-308) is the HTTP twin of api.ts's listProxyCaptures —
// the Monitor drill-down's filtered capture list. GET /proxy/captures is
// cross-cutting (no repo prefix). Reshapes the snake_case wire rows into the
// camelCase ProxyCaptureRow shape api.ts re-exports.
export async function listProxyCaptures(
  host: string,
  dispatchId = 0,
  anthropicOnly = false,
  sinceDays = 0,
): Promise<ProxyCaptureRow[]> {
  const query: Record<string, string | number | boolean> = {};
  if (host) query.host = host;
  if (dispatchId > 0) query.dispatch_id = dispatchId;
  if (anthropicOnly) query.is_anthropic = true;
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiProxyCaptureRow[]>('/proxy/captures', { query });
  return (rows ?? []).map(reshapeProxyCapture);
}

// getProxyCaptureRaw (BACI-308) is the HTTP twin of api.ts's getProxyCaptureRaw —
// the raw .http capture text for one proxy_requests id, served as text/plain.
export async function getProxyCaptureRaw(id: number): Promise<string> {
  return callText(`/proxy/captures/${id}/raw`);
}

// anthropicCapture (BACI-308) is the HTTP twin — the parsed detail of one
// captured Anthropic SSE turn. The wire shape is already the snake_case
// ProxyMessage the sheet consumes, so no reshape is needed.
export async function anthropicCapture(id: number): Promise<ProxyMessage> {
  return call<ProxyMessage>(`/proxy/captures/${id}`);
}

// jobTranscript (BACI-308) is the HTTP twin — a dispatch's assembled per-job
// transcript. The wire shape is the snake_case AnthropicTranscript the adapter
// feeds the viewer, so no reshape is needed.
export async function jobTranscript(dispatchId: number): Promise<AnthropicTranscript> {
  return call<AnthropicTranscript>(`/proxy/jobs/${dispatchId}/transcript`);
}

// listJobTranscripts (BACI-322) is the HTTP twin of api.ts's
// listJobTranscripts — the Monitor Transcript page's row-per-dispatch list.
// GET /proxy/jobs is cross-cutting (no repo prefix in the path); `repo` scopes
// to the active board, `issue` / `mode` narrow, `session` / `agent` (BACI-348)
// narrow to one supervisor session / subagent identity, `sinceDays === 0` is
// the all-time sentinel. Reshapes the snake_case wire rows into the camelCase
// JobTranscriptRow shape api.ts re-exports.
export async function listJobTranscripts(
  repo = '',
  issue = '',
  mode = '',
  session = '',
  agent = '',
  sinceDays = 0,
): Promise<JobTranscriptRow[]> {
  const query: Record<string, string | number> = {};
  if (repo) query.repo = repo;
  if (issue) query.issue = issue;
  if (mode) query.mode = mode;
  if (session) query.session = session;
  if (agent) query.agent = agent;
  if (sinceDays > 0) query.since = `${sinceDays}d`;
  const rows = await call<ApiJobTranscriptRow[]>('/proxy/jobs', { query });
  return (rows ?? []).map(reshapeJobTranscript);
}
