// Proxy-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror model.ProxyFQDNStat /
// model.ProxyCaptureRow / model.JobTranscriptRow as the `bacio api` server
// serialises them; the reshapers map them into the camelCase shapes the
// Monitor / Transcript screens consume. The target DTOs are the TS-only
// interfaces lib/proxyStats + lib/proxyCaptures re-export under the same
// names as the Wails bindings — imported here directly (type-only) rather
// than via api.http.ts's re-export. Moved out of api.http.ts so the
// reshapes are unit testable; see ./issue.ts for the pattern.

import type { ProxyFQDNStat } from '../../lib/proxyStats';
import type { ProxyCaptureRow, JobTranscriptRow } from '../../lib/proxyCaptures';

// ApiProxyFQDNStat is the snake_case wire shape GET /proxy/stats returns
// (model.ProxyFQDNStat's JSON tags). reshapeProxyStat maps it into the
// camelCase ProxyFQDNStat the Monitor screen consumes.
export interface ApiProxyFQDNStat {
  host: string;
  request_count: number;
  bytes_in: number;
  bytes_out: number;
  error_count: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  first_seen: string;
  last_seen: string;
}

// ApiProxyCaptureRow is the snake_case wire shape GET /proxy/captures returns
// (model.ProxyCaptureRow — the embedded ProxyRequest fields plus the issue_key
// / mode enrichment). reshapeProxyCapture maps it into the camelCase
// ProxyCaptureRow the sheet consumes. raw_log_path is omitempty on the wire;
// its presence becomes hasRaw.
export interface ApiProxyCaptureRow {
  id: number;
  method: string;
  host: string;
  path: string;
  status: number;
  bytes_in: number;
  bytes_out: number;
  duration_ms: number;
  raw_log_path?: string;
  is_stream?: boolean;
  is_anthropic?: boolean;
  dispatch_id?: number | null;
  issue_key?: string;
  mode?: string;
  started_at: string;
}

// ApiJobTranscriptRow is the snake_case wire shape GET /proxy/jobs returns
// (model.JobTranscriptRow). reshapeJobTranscript maps it into the camelCase
// JobTranscriptRow the Transcript page consumes.
export interface ApiJobTranscriptRow {
  dispatch_id: number;
  issue_key?: string;
  mode?: string;
  agent_name?: string;
  repo_prefix?: string;
  model?: string;
  turn_count: number;
  usage: {
    input_tokens?: number;
    output_tokens?: number;
    cache_creation_input_tokens?: number;
    cache_read_input_tokens?: number;
    thinking_tokens?: number;
  };
  last_seen: string;
  session_id?: string;
  claude_agent_id?: string;
  session_label?: string;
}

export function reshapeProxyStat(r: ApiProxyFQDNStat): ProxyFQDNStat {
  return {
    host: r.host,
    requestCount: r.request_count,
    bytesIn: r.bytes_in,
    bytesOut: r.bytes_out,
    errorCount: r.error_count,
    errorRate: r.error_rate,
    p50Ms: r.p50_ms,
    p95Ms: r.p95_ms,
    firstSeen: r.first_seen,
    lastSeen: r.last_seen,
  };
}

export function reshapeProxyCapture(r: ApiProxyCaptureRow): ProxyCaptureRow {
  return {
    id: r.id,
    method: r.method,
    host: r.host,
    path: r.path,
    status: r.status,
    bytesIn: r.bytes_in,
    bytesOut: r.bytes_out,
    durationMs: r.duration_ms,
    isStream: !!r.is_stream,
    isAnthropic: !!r.is_anthropic,
    hasRaw: !!r.raw_log_path,
    dispatchId: r.dispatch_id,
    issueKey: r.issue_key,
    mode: r.mode,
    startedAt: r.started_at,
  };
}

export function reshapeJobTranscript(r: ApiJobTranscriptRow): JobTranscriptRow {
  return {
    dispatchId: r.dispatch_id,
    issueKey: r.issue_key,
    mode: r.mode,
    agentName: r.agent_name,
    repoPrefix: r.repo_prefix,
    model: r.model,
    turnCount: r.turn_count,
    usage: {
      inputTokens: r.usage?.input_tokens ?? 0,
      outputTokens: r.usage?.output_tokens ?? 0,
      cacheCreationTokens: r.usage?.cache_creation_input_tokens ?? 0,
      cacheReadTokens: r.usage?.cache_read_input_tokens ?? 0,
      thinkingTokens: r.usage?.thinking_tokens ?? 0,
    },
    lastSeen: r.last_seen,
    sessionId: r.session_id,
    claudeAgentId: r.claude_agent_id,
    sessionLabel: r.session_label,
  };
}
