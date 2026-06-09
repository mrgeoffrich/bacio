// Pipeline-domain Wails calls (BACI-359, Phase 4): card ordering, process
// assignment, the per-card engine controls, and the Shipping-column reads.
// The job-control verbs filter the nullable binding chain down to the
// non-null PipelineJob[] the UI consumes (guard narrows against the binding).
import { BoardService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { PipelineJob as WirePipelineJob } from '../../bindings/github.com/mrgeoffrich/bacio/internal/model';
import type { BoardCard, PipelineJob, ShippedListDTO } from './contract';
import type { ProcessSelection } from '../lib/pipelineProcesses';
import { normalize } from './normalize';

// reorderCard moves a card to a 1-based position within its (repo, state)
// ordering band — the Backlog / Shipping drag-to-reorder.
export async function reorderCard(
  repoPrefix: string,
  key: string,
  position: number,
): Promise<BoardCard> {
  try {
    return await BoardService.ReorderCard(repoPrefix, key, position);
  } catch (err) {
    throw normalize(err);
  }
}

// createRelation wires a `blocks` edge so `blocked` ends up blocked by
// `blocker` — the Pipeline drag-to-block gesture (BACI-342). A `blocks`
// edge is stored from = blocker, to = blocked, so the drop target (the
// blocker) is the first arg and the dragged card (which becomes blocked)
// is the second. The HTTP twin in api.http.ts hard-codes type 'blocks'
// the same way; the server's INSERT OR IGNORE makes a duplicate a silent
// no-op. The caller owns the optimistic badge update, so no value comes
// back.
export async function createRelation(
  repoPrefix: string,
  blocker: string,
  blocked: string,
): Promise<void> {
  try {
    await BoardService.CreateRelation(repoPrefix, blocker, blocked);
  } catch (err) {
    throw normalize(err);
  }
}

// setCardProcess assigns a process (see lib/pipelineProcesses),
// materialising the card's pending job chain. The selection is either an
// explicit ordered stage list (the cumulative-stepper picker) or a preset
// slug (the kept skip-Plan buttons) — mutually exclusive. Returns the new
// chain.
export async function setCardProcess(
  repoPrefix: string,
  key: string,
  selection: ProcessSelection,
): Promise<PipelineJob[]> {
  try {
    const process = 'process' in selection ? selection.process : '';
    const stages = 'stages' in selection ? selection.stages : [];
    // Wails maps Go []*model.PipelineJob to (PipelineJob | null)[]; the
    // store never returns nil elements, so drop them to match the
    // non-null contract the HTTP twin already satisfies.
    const jobs = await BoardService.SetCardProcess(repoPrefix, key, process, stages);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// editCardProcessTail edits the pending tail of an in_pipeline card's job
// chain (BACI-294): the completed / running / cancelled jobs stay as a
// locked prefix; `stages` is the re-ordered pending tail that replaces the
// card's pending jobs. Returns the refreshed chain.
export async function editCardProcessTail(
  repoPrefix: string,
  key: string,
  stages: string[],
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.EditCardProcessTail(repoPrefix, key, stages);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// resetCardProcess wipes an in_pipeline card's ENTIRE job chain (BACI-314)
// — including completed / cancelled history — so the card drops back to the
// from-scratch "Pick a process" picker. Refused by the engine while a job
// is running (the card hides Reset in that state). Returns the refreshed
// (empty) chain.
export async function resetCardProcess(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.ResetCardProcess(repoPrefix, key);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// startCardJob is the manual Start control — advance one step (start the
// next pending job, or run the Ship hand-off). Returns the refreshed chain.
export async function startCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.StartCardJob(repoPrefix, key);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// stopCardJob is the manual Stop control — cancel the running job and
// halt Auto. Returns the refreshed chain.
export async function stopCardJob(
  repoPrefix: string,
  key: string,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.StopCardJob(repoPrefix, key);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// rerunCardJob is the per-job Re-run control on an aborted step — reset the
// cancelled job at `seq` to pending and re-dispatch it. Returns the
// refreshed chain.
export async function rerunCardJob(
  repoPrefix: string,
  key: string,
  seq: number,
): Promise<PipelineJob[]> {
  try {
    const jobs = await BoardService.RerunCardJob(repoPrefix, key, seq);
    return (jobs ?? []).filter((j): j is WirePipelineJob => j != null);
  } catch (err) {
    throw normalize(err);
  }
}

// setEngineMode sets the controller engine's per-card drive mode
// ("off" | "auto") while the card is in_pipeline. Returns the updated card.
export async function setEngineMode(
  repoPrefix: string,
  key: string,
  mode: string,
): Promise<BoardCard> {
  try {
    return await BoardService.SetCardEngineMode(repoPrefix, key, mode);
  } catch (err) {
    throw normalize(err);
  }
}

// shipCard is the in_pipeline → to_be_shipped hand-off (no agent — the
// ship agent fires from the Shipping column). Returns the updated card.
export async function shipCard(
  repoPrefix: string,
  key: string,
): Promise<BoardCard> {
  try {
    return await BoardService.ShipCard(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// markDoneCard is the in_pipeline → done hand-off (BACI-352): close a card
// out as done directly, bypassing the Shipping column and the ship agent.
// The direct-done counterpart to shipCard. Returns the updated card.
export async function markDoneCard(
  repoPrefix: string,
  key: string,
): Promise<BoardCard> {
  try {
    return await BoardService.MarkCardDone(repoPrefix, key);
  } catch (err) {
    throw normalize(err);
  }
}

// setAutoShip toggles the per-repo Shipping-column auto-ship setting.
// Returns the persisted value.
export async function setAutoShip(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetAutoShip(repoPrefix, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// getAutoShip reads the per-repo Shipping-column auto-ship setting — the
// DB value the controller's auto-ship ticker acts on, so the Pipeline's
// Shipping switch seeds from the source of truth.
export async function getAutoShip(repoPrefix: string): Promise<boolean> {
  try {
    return await BoardService.GetAutoShip(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// setBacklogCollapsed (BACI-288) persists the per-repo Pipeline
// Backlog-column collapse preference. Returns the persisted value.
export async function setBacklogCollapsed(
  repoPrefix: string,
  collapsed: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetBacklogCollapsed(repoPrefix, collapsed);
  } catch (err) {
    throw normalize(err);
  }
}

// getBacklogCollapsed (BACI-288) reads the per-repo Pipeline
// Backlog-column collapse preference so the rail state seeds from the
// persisted KV.
export async function getBacklogCollapsed(repoPrefix: string): Promise<boolean> {
  try {
    return await BoardService.GetBacklogCollapsed(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// setImpactPrimary (BACI-349) persists the per-repo Pipeline
// impact-primary display preference. Returns the persisted value.
export async function setImpactPrimary(
  repoPrefix: string,
  impactPrimary: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetImpactPrimary(repoPrefix, impactPrimary);
  } catch (err) {
    throw normalize(err);
  }
}

// getImpactPrimary (BACI-349) reads the per-repo Pipeline impact-primary
// display preference so the toggle seeds from the persisted KV.
export async function getImpactPrimary(repoPrefix: string): Promise<boolean> {
  try {
    return await BoardService.GetImpactPrimary(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// listShippedIssues (BACI-187, reshaped for BACI-221) returns the
// Pipeline Shipping-column shipping-log popover rows for one repo (newest-first) wrapped
// with the total count under the same scope so the popover header can
// render "showing N of TOTAL". `sinceDays` clamps the relative window
// (0 = "Forever" — no lower bound on terminal_at); `sinceTs` (BACI-312)
// is the absolute local-midnight "Today" cutoff (RFC3339) and wins over
// sinceDays when non-empty; `limit` caps the row count (0 = the server's
// default 20, max 100). The HTTP twin in api.http.ts MUST keep the same
// name + shape so callers stay transport-agnostic.
export async function listShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  sinceTs: string,
  limit: number,
): Promise<ShippedListDTO> {
  try {
    return await BoardService.ListShipped(repoPrefix, sinceDays, sinceTs, limit);
  } catch (err) {
    throw normalize(err);
  }
}

// countShippedIssues (BACI-221) is the lean count-only sibling polled
// on the Pipeline Shipping-column pill's 10s cadence so the "Shipped · N" label reflects
// the active Today / Last Week / Forever scope even when the popover
// is closed. `sinceDays` / `sinceTs` mirror listShippedIssues — sinceDays
// 0 means "Forever"; a non-empty sinceTs is the absolute "Today" cutoff.
export async function countShippedIssues(
  repoPrefix: string,
  sinceDays: number,
  sinceTs: string,
): Promise<number> {
  try {
    return await BoardService.CountShipped(repoPrefix, sinceDays, sinceTs);
  } catch (err) {
    throw normalize(err);
  }
}
