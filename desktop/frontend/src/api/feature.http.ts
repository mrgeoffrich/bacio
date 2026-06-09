// Feature-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, readActor } from './client.http';
import { reshapeFeatureSummary, reshapeFeatureView, reshapePlanView } from './wire/feature';
import type { ApiFeature, ApiFeatureView, ApiPlanView } from './wire/feature';
import type { FeatureSummary, FeaturePlan, FeatureDetail } from './contract';

export async function listFeatures(repoPrefix: string): Promise<FeatureSummary[]> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const feats = await call<ApiFeature[]>(`/repos/${repoPrefix}/features`);
  return feats.map(reshapeFeatureSummary);
}

export async function getFeature(repoPrefix: string, slug: string): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const view = await call<ApiFeatureView>(`/repos/${repoPrefix}/features/${slug}`);
  return reshapeFeatureView(view);
}

// getFeaturePlan (BACI-236) returns the topo-sorted dependency-graph
// payload for a feature. includeClosed=false matches today's open-only
// shape; true widens to include done/cancelled issues plus every
// `blocks` edge whose endpoints are both in the feature. The web
// bundle reshapes blocked_by → blockedBy so callers can share a single
// camelCase shape with the Wails binding.
export async function getFeaturePlan(
  repoPrefix: string,
  slug: string,
  includeClosed: boolean,
): Promise<FeaturePlan> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its features');
  }
  const view = await call<ApiPlanView>(
    `/repos/${repoPrefix}/features/${slug}/plan`,
    { query: includeClosed ? { include_closed: '1' } : undefined },
  );
  return reshapePlanView(view);
}

// setFeatureEmoji (BACI-172) updates the per-feature emoji glyph
// surfaced on every kanban card under this feature. Empty string
// clears the emoji. The store-side validator enforces exactly one
// grapheme cluster (or empty) so malformed input surfaces as a
// {error} envelope. Returns the refreshed FeatureDetail so the
// caller can drop it straight into its panel state.
export async function setFeatureEmoji(
  repoPrefix: string,
  slug: string,
  emoji: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, emoji },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureBranchName (BACI-231) updates the per-feature integration
// branch every kanban card under this feature is decorated with and
// every dispatch is routed against. Empty string clears the branch
// (the feature ships straight to main again). The store-side
// ValidateBranchName enforces git's refname rules so a malformed
// input surfaces as a {error} envelope. Returns the refreshed
// FeatureDetail so the caller can drop it straight into its panel
// state. Parallel to setFeatureEmoji.
export async function setFeatureBranchName(
  repoPrefix: string,
  slug: string,
  branchName: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, branch_name: branchName },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureDescription (BACI-341) updates the per-feature description
// edited inline from the Features detail pane. Description is free text;
// empty clears it. Returns the refreshed FeatureDetail so the caller can
// drop it straight into its panel state. Parallel to setFeatureBranchName.
export async function setFeatureDescription(
  repoPrefix: string,
  slug: string,
  description: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(`/repos/${repoPrefix}/features/${slug}`, {
    method: 'PATCH',
    body: { slug, description },
  });
  return getFeature(repoPrefix, slug);
}

// setFeatureState (BACI-199) flips the feature's three-state column
// and returns the refreshed FeatureDetail. BACI-250 decoupled this
// from the auto-close pin — call setFeatureAutoClose to flip
// `state_manual` independently.
export async function setFeatureState(
  repoPrefix: string,
  slug: string,
  state: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/state`,
    { method: 'PUT', body: { slug, state } },
  );
  return getFeature(repoPrefix, slug);
}

// setFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 archive-sweep's auto-completion pass — and returns the
// refreshed FeatureDetail. enabled=true clears the bit; enabled=false
// sets it (pin long-lived catch-alls so they stay `active`).
export async function setFeatureAutoClose(
  repoPrefix: string,
  slug: string,
  enabled: boolean,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/auto-close`,
    { method: 'PUT', body: { enabled } },
  );
  return getFeature(repoPrefix, slug);
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
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<ApiFeature>(
    `/repos/${repoPrefix}/features/${slug}/handoffs`,
    { method: 'PUT', body: { enabled } },
  );
  return getFeature(repoPrefix, slug);
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
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit a feature');
  }
  await call<{ slug: string; hidden: boolean }>(
    `/repos/${repoPrefix}/features/${slug}/hide`,
    { method: 'PUT', body: { hidden } },
  );
  return getFeature(repoPrefix, slug);
}

// addFeatureComment posts a chronological handoff comment to a feature
// (BACI-124). Returns the refreshed FeatureDetail so the caller can
// drop the new row straight into its panel state.
export async function addFeatureComment(
  repoPrefix: string,
  slug: string,
  author: string,
  body: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to comment on a feature');
  }
  const effectiveAuthor = author?.trim() || readActor() || 'web';
  await call<unknown>(`/repos/${repoPrefix}/features/${slug}/comments`, {
    method: 'POST',
    body: { author: effectiveAuthor, body },
  });
  return getFeature(repoPrefix, slug);
}

// deleteFeatureComment removes a feature comment by uuid (BACI-124).
export async function deleteFeatureComment(
  repoPrefix: string,
  slug: string,
  commentUUID: string,
): Promise<FeatureDetail> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to comment on a feature');
  }
  await call<unknown>(`/repos/${repoPrefix}/features/${slug}/comments/${commentUUID}`, {
    method: 'DELETE',
  });
  return getFeature(repoPrefix, slug);
}

export async function archiveFeature(prefix: string, slug: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/features/${encodeURIComponent(slug)}/archive`, { method: 'POST' });
}

export async function unarchiveFeature(prefix: string, slug: string): Promise<unknown> {
  return call(`/repos/${encodeURIComponent(prefix)}/features/${encodeURIComponent(slug)}/unarchive`, { method: 'POST' });
}
