// Feature-domain Wails calls (BACI-359): feature reads, the property
// toggles, the dependency-graph plan, and feature comments.
import { FeatureService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type { FeatureSummary, FeatureDetail, FeaturePlan } from './contract';
import { normalize } from './normalize';

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  try {
    return await FeatureService.ListFeatures(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getFeature(repoPrefix: string, slug: string): Promise<FeatureDetail> {
  try {
    return await FeatureService.GetFeature(repoPrefix, slug);
  } catch (err) {
    throw normalize(err);
  }
}

// getFeaturePlan (BACI-236) returns the topo-sorted dependency-graph
// payload for a feature. includeClosed=false matches the historical
// open-only `bacio feature plan` shape (used today by the CLI text
// render); true widens to every issue in the feature plus every
// `blocks` edge whose endpoints are both in the feature, with
// closed=true on terminal entries so the graph view can mute them.
export async function getFeaturePlan(
  repoPrefix: string,
  slug: string,
  includeClosed: boolean,
): Promise<FeaturePlan> {
  try {
    return await FeatureService.GetFeaturePlan(repoPrefix, slug, includeClosed);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureEmoji (BACI-172) updates the per-feature emoji glyph
// rendered on every kanban card under the feature. Empty clears.
export async function setFeatureEmoji(
  repoPrefix: string,
  slug: string,
  emoji: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureEmoji(repoPrefix, slug, emoji);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureBranchName (BACI-231) updates the per-feature integration
// branch. Empty clears the branch (the feature ships straight to
// main again). Validated by the store-side ValidateBranchName so
// malformed input surfaces as an error string from the Wails binding.
export async function setFeatureBranchName(
  repoPrefix: string,
  slug: string,
  branchName: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureBranchName(repoPrefix, slug, branchName);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureDescription (BACI-341) updates the per-feature description
// from the Features detail pane. Description is free text; empty clears
// it. Mirrors setFeatureBranchName.
export async function setFeatureDescription(
  repoPrefix: string,
  slug: string,
  description: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureDescription(repoPrefix, slug, description);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureHiddenOnBoard (BACI-177) flips the per-feature "Show on
// board" toggle and returns the refreshed FeatureDetail. true hides
// every kanban card belonging to this feature on this machine; false
// makes them visible again. Idempotent — flipping to the same state
// is a no-op write.
export async function setFeatureHiddenOnBoard(
  repoPrefix: string,
  slug: string,
  hidden: boolean,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetHiddenOnBoard(repoPrefix, slug, hidden);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. BACI-250 decoupled this from
// the auto-close pin — call setFeatureAutoClose to flip
// `state_manual` independently.
export async function setFeatureState(
  repoPrefix: string,
  slug: string,
  state: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureState(repoPrefix, slug, state);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 archive-sweep's auto-completion pass — and returns the
// refreshed FeatureDetail. enabled=true clears the bit (the sweep may
// promote this feature once every child is terminal); enabled=false
// sets the bit (long-lived catch-alls stay `active` indefinitely).
export async function setFeatureAutoClose(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureAutoClose(repoPrefix, slug, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// setFeatureCollectHandoffs (BACI-333) flips the per-feature
// collect-handoffs toggle that gates whether worker close-outs append
// handoff comments to this feature, and returns the refreshed
// FeatureDetail. enabled=true collects handoffs; enabled=false silences a
// standing bucket like `bugs`/`maintenance`.
export async function setFeatureCollectHandoffs(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.SetFeatureCollectHandoffs(repoPrefix, slug, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// addFeatureComment posts a chronological handoff comment to a feature
// (BACI-124) and returns the refreshed feature detail.
export async function addFeatureComment(
  repoPrefix: string,
  slug: string,
  author: string,
  body: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.AddFeatureComment(repoPrefix, slug, author, body);
  } catch (err) {
    throw normalize(err);
  }
}

// deleteFeatureComment removes a feature comment by uuid (BACI-124) and
// returns the refreshed feature detail.
export async function deleteFeatureComment(
  repoPrefix: string,
  slug: string,
  commentUUID: string,
): Promise<FeatureDetail> {
  try {
    return await FeatureService.DeleteFeatureComment(repoPrefix, slug, commentUUID);
  } catch (err) {
    throw normalize(err);
  }
}
