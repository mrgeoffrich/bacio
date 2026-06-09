// Sync-domain Wails calls (BACI-359, BACI-108/111/112): the sync registry,
// the background-sync preference pair, the SyncSetupModal bootstrap, and the
// phantom-repo link. setupSync throws the typed SyncSetupCollisionError on a
// renumber-collision refusal so the modal can branch into its confirm step.
import { SettingsService } from '../../bindings/github.com/mrgeoffrich/bacio/desktop';
import type {
  SyncRegistryDTO,
  SyncPreferencesDTO,
  SyncSetupPayload,
  SyncSetupResult,
  SyncSetupDTO,
  CollisionPreviewDTO,
  RepoLinkResult,
} from './contract';
import { normalize } from './normalize';

// SyncSetupCollisionError carries the typed CollisionPreviewDTO so the
// modal can render the renumber / rename preview verbatim. Throws are
// the natural shape for the wire-level seam — a 409-equivalent turns into
// one of these in both transports — but the underlying SyncSetupResult is
// preserved on the .result property when the caller wants the full shape.
export class SyncSetupCollisionError extends Error {
  previewCollisions: CollisionPreviewDTO;
  result: SyncSetupDTO;
  constructor(result: SyncSetupDTO) {
    super('sync setup: renumber collision');
    this.name = 'SyncSetupCollisionError';
    this.result = result;
    // Non-null assert: caller only constructs this when the server
    // populated previewCollisions; the Go side guarantees it.
    this.previewCollisions = result.previewCollisions!;
  }
}

// getSyncRegistry returns the BACI-108 sync-repo registry payload —
// one entry per `sync_remotes` row with the projects each carries,
// plus the residual tracked project repos that don't yet have a
// sync.remote in their .bacio/config.yaml. Backs the standalone Sync
// view.
export async function getSyncRegistry(): Promise<SyncRegistryDTO> {
  try {
    return await SettingsService.GetSyncRegistry();
  } catch (err) {
    throw normalize(err);
  }
}

export async function getSyncPreferences(): Promise<SyncPreferencesDTO> {
  try {
    return await SettingsService.GetSyncPreferences();
  } catch (err) {
    throw normalize(err);
  }
}

export async function setSyncPreferences(
  backgroundEnabled: boolean,
): Promise<SyncPreferencesDTO> {
  try {
    return await SettingsService.SetSyncPreferences(backgroundEnabled);
  } catch (err) {
    throw normalize(err);
  }
}

// setupSync (BACI-111) drives the SyncSetupModal — three modes
// (init / clone / attach) map 1:1 onto the engine's bootstrap paths.
// On a renumber-collision refusal the Wails service returns the
// SyncSetupDTO with `previewCollisions` populated and a nil Go-side
// error; the JS-side seam detects the populated preview and throws
// SyncSetupCollisionError so the modal can `instanceof`-branch into
// the step-2 confirm. Any other failure (validation / engine error /
// lock timeout) surfaces as a plain Error via normalize().
export async function setupSync(
  prefix: string,
  payload: SyncSetupPayload,
): Promise<SyncSetupResult> {
  let result: SyncSetupDTO;
  try {
    result = await SettingsService.SetupSync(prefix, payload);
  } catch (err) {
    throw normalize(err);
  }
  if (result.previewCollisions) {
    throw new SyncSetupCollisionError(result);
  }
  return result;
}

// linkPhantomRepo (BACI-112) binds a phantom repo (a sync_clone-
// imported row with no local path) to a local working tree. Drives
// the PhantomLinkModal on the desktop / web SyncView. Errors come
// back as plain Error here; the caller renders the message inline.
export async function linkPhantomRepo(
  prefix: string,
  path: string,
): Promise<RepoLinkResult> {
  try {
    return await SettingsService.LinkPhantomRepo(prefix, path);
  } catch (err) {
    throw normalize(err);
  }
}
