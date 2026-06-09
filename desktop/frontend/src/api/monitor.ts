// Monitor-domain Wails calls (BACI-359, BACI-304/308/322): the reverse-proxy
// per-FQDN rollup and the capture / job-transcript drill-down. The proxy /
// transcript shapes keep their existing binding source (not folded into the
// shared contract) — re-exported here under the cross-transport names the web
// twin ships from src/lib.
import { MonitorService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type {
  ProxyFQDNStatDTO,
  ProxyCaptureRowDTO,
  JobTranscriptRowDTO,
} from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { ProxyMessage, AnthropicTranscript } from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';
import { normalize } from './normalize';

export type ProxyFQDNStat = ProxyFQDNStatDTO;
export type ProxyCaptureRow = ProxyCaptureRowDTO;
export type JobTranscriptRow = JobTranscriptRowDTO;
export type { ProxyMessage, AnthropicTranscript };

// listProxyStats (BACI-304) returns the Monitor screen's per-FQDN
// reverse-proxy traffic rollup, busiest host first. The proxy_requests
// table is cross-cutting (no repo_id), so unlike the per-repo reads above
// this takes no repo prefix — the stats are global. `sinceDays` windows
// the rollup to a rolling lookback of that many days; `0` (the default)
// is the "All-time" sentinel — no lower bound. The HTTP twin in
// api.http.ts MUST keep the same name + shape so MonitorView stays
// transport-agnostic.
export async function listProxyStats(sinceDays = 0): Promise<ProxyFQDNStat[]> {
  try {
    return await MonitorService.ProxyStats(sinceDays);
  } catch (err) {
    throw normalize(err);
  }
}

// listProxyCaptures (BACI-308) returns the filtered, newest-first capture
// list the Monitor sheet walks an FQDN row down into — each row enriched with
// its dispatch's issue-key + mode. `host` scopes to one upstream; `dispatchId`
// > 0 scopes to one job; `anthropicOnly` keeps only the parseable message-API
// captures; `sinceDays` windows to a rolling lookback (0 = all-time). The HTTP
// twin keeps the same name + shape so the sheet stays transport-agnostic.
export async function listProxyCaptures(
  host: string,
  dispatchId = 0,
  anthropicOnly = false,
  sinceDays = 0,
): Promise<ProxyCaptureRow[]> {
  try {
    return await MonitorService.ListCaptures(host, dispatchId, anthropicOnly, sinceDays);
  } catch (err) {
    throw normalize(err);
  }
}

// getProxyCaptureRaw (BACI-308) returns the inflated, auth-redacted .http
// capture text for one proxy_requests id — the raw fallback the sheet shows
// for a capture that isn't a parseable Anthropic turn. Throws when the row has
// no raw file or it's been pruned.
export async function getProxyCaptureRaw(id: number): Promise<string> {
  try {
    return await MonitorService.CaptureRaw(id);
  } catch (err) {
    throw normalize(err);
  }
}

// anthropicCapture (BACI-308) returns the parsed detail of one captured
// Anthropic SSE turn (proxy_messages row), or throws (404) when the capture
// wasn't parseable. The sheet uses it for the single-capture summary chrome.
export async function anthropicCapture(id: number): Promise<ProxyMessage> {
  try {
    const msg = await MonitorService.Capture(id);
    if (!msg) throw new Error('capture not found');
    return msg;
  } catch (err) {
    throw normalize(err);
  }
}

// jobTranscript (BACI-308) returns a dispatch's assembled per-job transcript —
// the ordered primary-thread messages, summed usage, and auxiliary turns. The
// sheet feeds this through anthropicTranscriptToParseResult into TranscriptView.
// Throws (404) when the dispatch has no parsed captures.
export async function jobTranscript(dispatchId: number): Promise<AnthropicTranscript> {
  try {
    const tr = await MonitorService.JobTranscript(dispatchId);
    if (!tr) throw new Error('transcript not found');
    return tr;
  } catch (err) {
    throw normalize(err);
  }
}

// listJobTranscripts (BACI-322) returns the Monitor Transcript page's
// row-per-dispatch list — one summary row per dispatch that has parsed
// captures. `repo` scopes to the active board's prefix; `issue` / `mode`
// narrow to one issue / one job mode; `session` / `agent` (BACI-348) narrow to
// one supervisor session / one subagent identity; `sinceDays` windows to a
// rolling lookback (0 = all-time). The HTTP twin keeps the same name + shape so
// TranscriptListPanel stays transport-agnostic.
export async function listJobTranscripts(
  repo = '',
  issue = '',
  mode = '',
  session = '',
  agent = '',
  sinceDays = 0,
): Promise<JobTranscriptRow[]> {
  try {
    return await MonitorService.ListJobTranscripts(repo, issue, mode, session, agent, sinceDays);
  } catch (err) {
    throw normalize(err);
  }
}
