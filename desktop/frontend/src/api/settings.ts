// Settings-domain Wails calls (BACI-359): prompt-template CRUD, the global
// preference pairs (display / archive / audio / timezone), the per-repo
// default-feature + board-hidden-states settings, the bacio version probe,
// and the leader-election status seed.
import { BoardService, SettingsService, LeaderService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type {
  PromptTemplateDTO,
  DisplayPreferencesDTO,
  ArchivePreferencesDTO,
  AudioPreferencesDTO,
  TimezonePreferencesDTO,
  DefaultFeatureDTO,
  BoardHiddenStatesDTO,
  LeaderStatusDTO,
} from './contract';
import { normalize } from './normalize';

// listPromptTemplates returns every registered dispatch prompt template
// (built-ins + user-created), in store iteration order.
export async function listPromptTemplates(): Promise<PromptTemplateDTO[]> {
  try {
    return await SettingsService.ListPromptTemplates();
  } catch (err) {
    throw normalize(err);
  }
}

// addPromptTemplate creates a new dispatch prompt template. The trailing
// actionLabel (BACI-67) is the imperative override rendered on the
// dispatch action menus; pass "" to skip it and have the UI derive
// from the display name via the gerund→imperative rule.
export async function addPromptTemplate(
  slug: string,
  name: string,
  body: string,
  actionLabel: string = '',
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.AddPromptTemplate(slug, name, body, actionLabel);
  } catch (err) {
    throw normalize(err);
  }
}

// renamePromptTemplate renames an existing template — slug, name, or
// both. Pass an empty string for newSlug to keep the slug; pass an
// empty string for newName to keep the display name.
export async function renamePromptTemplate(
  slug: string,
  newSlug: string,
  newName: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.RenamePromptTemplate(slug, newSlug, newName);
  } catch (err) {
    throw normalize(err);
  }
}

// deletePromptTemplate removes a template by slug. Historical dispatch
// rows that reference the slug are left intact (a dispatch is a
// snapshot, not a foreign key).
export async function deletePromptTemplate(slug: string): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.DeletePromptTemplate(slug);
  } catch (err) {
    throw normalize(err);
  }
}

// restoreBuiltinPromptTemplates re-seeds any built-in slug that's been
// deleted, then returns the refreshed full template list.
export async function restoreBuiltinPromptTemplates(): Promise<PromptTemplateDTO[]> {
  try {
    return await SettingsService.RestoreBuiltinPromptTemplates();
  } catch (err) {
    throw normalize(err);
  }
}

// promptPlaceholders returns the placeholder token names a template body
// can interpolate (without the surrounding {{ }}).
export async function promptPlaceholders(): Promise<string[]> {
  try {
    return await SettingsService.PromptPlaceholders();
  } catch (err) {
    throw normalize(err);
  }
}

// bacioVersion returns the version string of the bacio binary this
// desktop client is running, so the Settings panel can surface it for
// cross-checking against per-session "Bacio version" on the Agents panel.
export async function bacioVersion(): Promise<string> {
  try {
    return await SettingsService.BacioVersion();
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptTemplate stores a custom body for one dispatch stage. Passing
// an empty body resets that stage to its built-in default.
export async function savePromptTemplate(
  mode: string,
  body: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptTemplate(mode, body);
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptConcurrency (BACI-51) updates a template's per-(repo, slug)
// in-flight dispatch cap the matcher enforces. 0 = unlimited; positive
// integers cap. Returns the refreshed template DTO.
export async function savePromptConcurrency(
  mode: string,
  concurrencyLimit: number,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptConcurrency(mode, concurrencyLimit);
  } catch (err) {
    throw normalize(err);
  }
}

// savePromptActionLabel (BACI-67) sets or clears a template's
// imperative override — the verb the dispatch action menus render.
// An empty actionLabel clears the override; the UI then derives one
// from the gerund display name. Returns the refreshed template DTO.
export async function savePromptActionLabel(
  mode: string,
  actionLabel: string,
): Promise<PromptTemplateDTO> {
  try {
    return await SettingsService.SavePromptActionLabel(mode, actionLabel);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getDisplayPreferences(): Promise<DisplayPreferencesDTO> {
  try {
    return await SettingsService.GetDisplayPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setDisplayPreferences(
  showArchived: boolean,
): Promise<DisplayPreferencesDTO> {
  try {
    return await SettingsService.SetDisplayPreferences(showArchived);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getArchivePreferences(): Promise<ArchivePreferencesDTO> {
  try {
    return await SettingsService.GetArchivePreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setArchivePreferences(
  autoEnabled: boolean,
  retentionDays: number,
): Promise<ArchivePreferencesDTO> {
  try {
    return await SettingsService.SetArchivePreferences(autoEnabled, retentionDays);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getAudioPreferences(): Promise<AudioPreferencesDTO> {
  try {
    return await SettingsService.GetAudioPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setAudioPreferences(
  shippedSfx: boolean,
): Promise<AudioPreferencesDTO> {
  try {
    return await SettingsService.SetAudioPreferences(shippedSfx);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getTimezonePreferences(): Promise<TimezonePreferencesDTO> {
  try {
    return await SettingsService.GetTimezonePreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setTimezonePreferences(
  timezone: string,
): Promise<TimezonePreferencesDTO> {
  try {
    return await SettingsService.SetTimezonePreferences(timezone);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getDefaultFeature(
  repoPrefix: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.GetDefaultFeature(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function setDefaultFeature(
  repoPrefix: string,
  slug: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.SetDefaultFeature(repoPrefix, slug);
  } catch (err) {
    throw normalize(err);
  }
}

export async function clearDefaultFeature(
  repoPrefix: string,
): Promise<DefaultFeatureDTO> {
  try {
    return await BoardService.ClearDefaultFeature(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

export async function getBoardHiddenStates(
  repoPrefix: string,
): Promise<BoardHiddenStatesDTO> {
  try {
    return await BoardService.GetBoardHiddenStates(repoPrefix);
  } catch (err) {
    throw normalize(err);
  }
}

// setBoardHiddenStates replaces the persisted set (replace-not-merge).
// Pass the full new array — unknown state names are silently dropped
// at the store boundary so a future state rename doesn't break old
// saved settings.
export async function setBoardHiddenStates(
  repoPrefix: string,
  states: string[],
): Promise<BoardHiddenStatesDTO> {
  try {
    return await BoardService.SetBoardHiddenStates(repoPrefix, states);
  } catch (err) {
    throw normalize(err);
  }
}

// setShowAgentSurfaces / setShowKanban persist the per-space
// nav-surface gates — which top-nav tabs the space exposes. Both return
// the persisted value.
//
// There is deliberately no getter pair: the resolved values already ride
// every Board from listBoards, so a second read path could drift from
// the one the nav actually renders off. Callers patch the provider's
// board list optimistically instead (RepoProvider.patchBoard).
export async function setShowAgentSurfaces(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetShowAgentSurfaces(repoPrefix, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

export async function setShowKanban(
  repoPrefix: string,
  enabled: boolean,
): Promise<boolean> {
  try {
    return await BoardService.SetShowKanban(repoPrefix, enabled);
  } catch (err) {
    throw normalize(err);
  }
}

// getLeaderStatus returns the current UI leader-election state synchronously.
// Used on mount to seed the UI before the first "leaderStatus" event arrives.
export async function getLeaderStatus(): Promise<LeaderStatusDTO> {
  try {
    return await LeaderService.GetLeaderStatus();
  } catch (err) {
    throw normalize(err);
  }
}
