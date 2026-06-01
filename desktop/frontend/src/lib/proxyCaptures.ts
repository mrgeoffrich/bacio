// BACI-308 Monitor capture drill-down — the shared cross-transport shapes the
// MonitorCaptureSheet consumes. Lives in one TS-only (runtime-free,
// browser-safe) module so the web bundle's api.http.ts and the Wails seam in
// api.ts ship the same `ProxyCaptureRow` / capture-detail / transcript names,
// the same split as lib/proxyStats.ts's ProxyFQDNStat.
//
// The Wails seam re-exports the generated ProxyCaptureRowDTO under the name
// `ProxyCaptureRow`; the HTTP seam re-exports the interface below under the
// same name (reshaping the snake_case wire row into it). MonitorCaptureSheet
// imports `ProxyCaptureRow` from `./api` and never knows which transport it's
// on.

// ProxyCaptureRow is one row of the capture drill-down list — identical fields
// to the desktop ProxyCaptureRowDTO so the two seams are interchangeable.
// issueKey / mode are empty when the capture has no dispatch; hasRaw gates the
// "View raw" affordance; isAnthropic gates the parsed-transcript affordance.
// startedAt is an ISO timestamp.
export interface ProxyCaptureRow {
  id: number;
  method: string;
  host: string;
  path: string;
  status: number;
  bytesIn: number;
  bytesOut: number;
  durationMs: number;
  isStream: boolean;
  isAnthropic: boolean;
  hasRaw: boolean;
  dispatchId?: number | null;
  issueKey?: string;
  mode?: string;
  startedAt: string;
}

// The capture-detail / job-transcript shapes the sheet feeds the transcript
// viewer are the snake_case AnthropicTranscript shape (model JSON tags aligned
// to the viewer's TS types). Re-exported from the adapter module so there is a
// single source of truth for the shape across both transports.
import type {
  AnthropicTranscriptJSON,
  AnthropicUsageJSON,
} from './transcript/anthropicAdapter';
export type {
  AnthropicTranscriptJSON as MonitorTranscript,
  AnthropicMessageJSON,
  AnthropicBlockJSON,
  AnthropicTurnJSON,
  AnthropicUsageJSON,
} from './transcript/anthropicAdapter';

// AnthropicTranscript is the cross-transport name for the assembled per-job
// transcript (the same snake_case shape model.AnthropicTranscript serialises
// to). api.ts re-exports the Wails-binding class under this name; the HTTP
// twin re-exports this alias.
export type AnthropicTranscript = AnthropicTranscriptJSON;

// ProxyMessage is the cross-transport name for one parsed capture's detail
// (model.ProxyMessage). The sheet reads only a handful of fields for the
// single-capture summary chrome; the snake_case fields mirror the Go struct
// JSON tags so the Wails binding and this TS twin are interchangeable.
export interface ProxyMessage {
  id: number;
  proxy_request_id: number;
  dispatch_id?: number | null;
  session_id?: string;
  claude_agent_id?: string;
  model?: string;
  system_fingerprint?: string;
  message_count: number;
  is_primary: boolean;
  stop_reason?: string;
  delta_json?: string;
  turn_json?: string;
  usage: AnthropicUsageJSON;
  started_at: string;
}

// JobTranscriptUsage is the camelCase token-usage twin the transcript list row
// carries — identical fields to the desktop JobTranscriptUsageDTO so the two
// seams are interchangeable (the BACI-322 row-per-dispatch counterpart of the
// other Monitor DTO splits).
export interface JobTranscriptUsage {
  inputTokens: number;
  outputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
  thinkingTokens: number;
}

// JobTranscriptRow is one row of the BACI-322 transcript browser list — one
// distinct dispatch that has parsed captures. dispatchId is the deep-link key
// the Transcript page navigates on; issueKey / mode / agentName / repoPrefix
// are best-effort enrichment (empty when the dispatch was deleted); model is
// the primary thread's; turnCount is the primary-capture count; usage is summed
// across the dispatch; lastSeen is an ISO timestamp. The Wails seam re-exports
// the generated JobTranscriptRowDTO under this name; the HTTP seam reshapes the
// snake_case wire row into it. TranscriptListPanel imports it from `./api` and
// never knows which transport it's on.
export interface JobTranscriptRow {
  dispatchId: number;
  issueKey?: string;
  mode?: string;
  agentName?: string;
  repoPrefix?: string;
  model?: string;
  turnCount: number;
  usage: JobTranscriptUsage;
  lastSeen: string;
}
