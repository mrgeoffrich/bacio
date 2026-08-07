// Settings-domain HTTP transport (BACI-359). Fetch wrappers + reshapers over
// the `bacio api` REST surface; the public ./api surface is the same as the
// Wails seam's. See ./client.http for the shared plumbing.
import { call, preferencePair } from './client.http';
import { reshapeTemplate } from './wire/template';
import type { ApiPromptTemplate, ApiRestoreResponse } from './wire/template';
import type { LeaderStatusDTO, PromptTemplateDTO, DisplayPreferencesDTO, ArchivePreferencesDTO, AudioPreferencesDTO, TimezonePreferencesDTO, DefaultFeatureDTO, BoardHiddenStatesDTO } from './contract';

type defaultFeatureResp = { feature: { slug: string; title?: string; emoji?: string } | null };

function dtoFromResp(res: defaultFeatureResp): DefaultFeatureDTO {
  if (!res.feature) return { slug: '' };
  return { slug: res.feature.slug, title: res.feature.title, emoji: res.feature.emoji };
}

type boardHiddenStatesResp = { states: string[] | null };

// Refetch every template and return the one DTO the caller updated —
// SavePromptTemplate only returns the persisted row's body, not the
// full DTO; fetching once after the write keeps the caller's
// `templates` state consistent.
async function refreshOneTemplate(slug: string): Promise<PromptTemplateDTO> {
  const all = await listPromptTemplates();
  const found = all.find(t => t.slug === slug);
  if (!found) throw new Error(`template ${slug} not found after save`);
  return found;
}

// ---------- Global preference pairs (BACI-68 / 162 / 240 / 312) ----------
// Collapsed behind preferencePair (BACI-359): GET + PUT + snake->camel reshape.

const displayPrefs = preferencePair<{ show_archived: boolean }, DisplayPreferencesDTO>(
  '/settings/display-preferences', (w) => ({ showArchived: w.show_archived }));
export const getDisplayPreferences = (): Promise<DisplayPreferencesDTO> => displayPrefs.get();
export const setDisplayPreferences = (showArchived: boolean): Promise<DisplayPreferencesDTO> =>
  displayPrefs.set({ show_archived: showArchived });

const archivePrefs = preferencePair<{ auto_enabled: boolean; retention_days: number }, ArchivePreferencesDTO>(
  '/settings/archive-preferences', (w) => ({ autoEnabled: w.auto_enabled, retentionDays: w.retention_days }));
export const getArchivePreferences = (): Promise<ArchivePreferencesDTO> => archivePrefs.get();
export const setArchivePreferences = (autoEnabled: boolean, retentionDays: number): Promise<ArchivePreferencesDTO> =>
  archivePrefs.set({ auto_enabled: autoEnabled, retention_days: retentionDays });

const audioPrefs = preferencePair<{ shipped_sfx: boolean }, AudioPreferencesDTO>(
  '/settings/audio-preferences', (w) => ({ shippedSfx: w.shipped_sfx }));
export const getAudioPreferences = (): Promise<AudioPreferencesDTO> => audioPrefs.get();
export const setAudioPreferences = (shippedSfx: boolean): Promise<AudioPreferencesDTO> =>
  audioPrefs.set({ shipped_sfx: shippedSfx });

const timezonePrefs = preferencePair<{ timezone: string }, TimezonePreferencesDTO>(
  '/settings/timezone-preferences', (w) => ({ timezone: w.timezone }));
export const getTimezonePreferences = (): Promise<TimezonePreferencesDTO> => timezonePrefs.get();
export const setTimezonePreferences = (timezone: string): Promise<TimezonePreferencesDTO> =>
  timezonePrefs.set({ timezone });

export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  // BACI-50 added the composite /settings/templates/full endpoint that
  // returns the rich DTO — labels, defaults, is_builtin, and the
  // BACI-51 concurrency fields all flow through in one round-trip,
  // no client-side label derivation or placeholder fields needed.
  const rows = await call<ApiPromptTemplate[]>('/settings/templates/full');
  return (rows ?? []).map(reshapeTemplate);
}

export async function savePromptTemplate(
  mode: string,
  body: string,
): Promise<PromptTemplateDTO> {
  // Empty body = reset to default. BACI-36's PUT rejects an empty body
  // explicitly; the documented contract is to DELETE for reset.
  if (body === '') {
    await call<unknown>(`/settings/templates/${mode}`, { method: 'DELETE' });
  } else {
    await call<unknown>(`/settings/templates/${mode}`, {
      method: 'PUT',
      body: { body },
    });
  }
  return refreshOneTemplate(mode);
}

// savePromptConcurrency (BACI-51) PUTs the per-(repo, slug) in-flight
// dispatch cap. 0 = unlimited; positive integers cap. No DELETE route
// (a PUT 0 reverts to "unlimited").
export async function savePromptConcurrency(
  mode: string,
  concurrencyLimit: number,
): Promise<PromptTemplateDTO> {
  await call<unknown>(`/settings/templates/${mode}/concurrency`, {
    method: 'PUT',
    body: { concurrency_limit: concurrencyLimit },
  });
  return refreshOneTemplate(mode);
}

// savePromptActionLabel (BACI-67) sets or clears the imperative
// override rendered on the dispatch action menus. An empty actionLabel
// DELETEs the override, mirroring the body endpoint's reset shape; the
// UI then derives a default from the gerund display name. A non-empty
// value PUTs the override.
export async function savePromptActionLabel(
  mode: string,
  actionLabel: string,
): Promise<PromptTemplateDTO> {
  if (actionLabel === '') {
    await call<unknown>(`/settings/templates/${mode}/action-label`, { method: 'DELETE' });
  } else {
    await call<unknown>(`/settings/templates/${mode}/action-label`, {
      method: 'PUT',
      body: { action_label: actionLabel },
    });
  }
  return refreshOneTemplate(mode);
}

export async function addPromptTemplate(
  slug: string,
  name: string,
  body: string,
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  // BACI-67: forward actionLabel verbatim — an empty string is the
  // "no override, derive from name" sentinel that the Go side honours.
  const raw = await call<ApiPromptTemplate>('/settings/templates', {
    method: 'POST',
    body: { slug, name, body, action_label: actionLabel },
  });
  return reshapeTemplate(raw);
}

export async function renamePromptTemplate(
  slug: string,
  newSlug: string,
  newName: string,
): Promise<PromptTemplateDTO> {
  const raw = await call<ApiPromptTemplate>(`/settings/templates/${slug}/rename`, {
    method: 'POST',
    body: { new_slug: newSlug, new_name: newName },
  });
  return reshapeTemplate(raw);
}

export async function deletePromptTemplate(
  slug: string,
): Promise<PromptTemplateDTO> {
  const raw = await call<ApiPromptTemplate>(`/settings/templates/${slug}/row`, {
    method: 'DELETE',
  });
  return reshapeTemplate(raw);
}

export async function restoreBuiltinPromptTemplates(): Promise<PromptTemplateDTO[]> {
  const raw = await call<ApiRestoreResponse>('/settings/templates/restore-builtins', {
    method: 'POST',
  });
  return (raw.templates ?? []).map(reshapeTemplate);
}

export async function promptPlaceholders(): Promise<string[]> {
  // Mirror internal/model/prompt.go:PromptTemplateTokens. The REST
  // surface doesn't return the token list separately, but it's a
  // small fixed set — render the canonical names client-side.
  return ['issue_id', 'issue_title', 'repo_prefix'];
}

export async function bacioVersion(): Promise<string> {
  const res = await call<{ version: string }>('/version');
  return res.version;
}

export async function getDefaultFeature(repoPrefix: string): Promise<DefaultFeatureDTO> {
  const res = await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`);
  return dtoFromResp(res);
}

export async function setDefaultFeature(repoPrefix: string, slug: string): Promise<DefaultFeatureDTO> {
  const res = await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`, {
    method: 'PUT',
    body: { slug },
  });
  return dtoFromResp(res);
}

export async function clearDefaultFeature(repoPrefix: string): Promise<DefaultFeatureDTO> {
  await call<defaultFeatureResp>(`/repos/${encodeURIComponent(repoPrefix)}/settings/default-feature`, {
    method: 'DELETE',
  });
  return { slug: '' };
}

export async function getBoardHiddenStates(repoPrefix: string): Promise<BoardHiddenStatesDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to view its board-hide settings');
  }
  const res = await call<boardHiddenStatesResp>(
    `/repos/${encodeURIComponent(repoPrefix)}/board/hidden-states`,
  );
  return { states: res.states ?? [] };
}

// setBoardHiddenStates replaces the persisted set (replace-not-merge).
// Pass the full new array — unknown state names are silently dropped
// at the store boundary so a future state rename doesn't break old
// saved settings.
export async function setBoardHiddenStates(
  repoPrefix: string,
  states: string[],
): Promise<BoardHiddenStatesDTO> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit its board-hide settings');
  }
  const res = await call<boardHiddenStatesResp>(
    `/repos/${encodeURIComponent(repoPrefix)}/board/hidden-states`,
    { method: 'PUT', body: { states } },
  );
  return { states: res.states ?? [] };
}

// setShowAgentSurfaces / setShowKanban persist the per-space nav-surface
// gates — which top-nav tabs the space exposes. PUT only; the resolved
// values already ride every repo row from GET /repos, so there is no
// getter pair to drift from it. Mirrors the Wails seam in settings.ts —
// there is no `satisfies` parity check between the two transports, so
// `npm run build:web` is what catches a drift here.
export async function setShowAgentSurfaces(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit its nav settings');
  }
  const res = await call<{ show_agent_surfaces: boolean }>(
    `/repos/${encodeURIComponent(repoPrefix)}/show-agent-surfaces`,
    { method: 'PUT', body: { enabled } },
  );
  return res.show_agent_surfaces;
}

export async function setShowKanban(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  if (!repoPrefix || repoPrefix === 'all') {
    throw new Error('select a repository to edit its nav settings');
  }
  const res = await call<{ show_kanban: boolean }>(
    `/repos/${encodeURIComponent(repoPrefix)}/show-kanban`,
    { method: 'PUT', body: { enabled } },
  );
  return res.show_kanban;
}

// The bacio api server runs the elector itself, sharing the ui_leader
// table with the desktop and TUI. The browser can never be the leader,
// but its connected api server can — so the "Controlling" chip reflects
// the server's lease, not this page's. App.tsx polls this on a 10s
// cadence (matching POLL_INTERVAL_MS, which mirrors UILeaderHeartbeatInterval).
export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  const res = await call<{ amLeader: boolean; holderLabel: string }>('/leader');
  return { amLeader: !!res.amLeader, holderLabel: res.holderLabel ?? '' };
}
