import type { Board } from '../../api';
import { formatSyncTime } from '../../lib/syncBadge';
import './workspace.css';

// WorkspaceSyncNote is what the Sync settings pane says when the active
// project is a workspace.
//
// A workspace has no working tree, so it has nowhere to keep a
// `.bacio/config.yaml` and cannot drive a sync tick of its own — the
// server refuses a sync setup on it outright, and the "Unsynced projects"
// list below deliberately leaves it out (that list is a call to action
// whose action doesn't exist here).
//
// But it IS mirrored. `Engine.Export` walks every repo in the database
// with no filter, so the moment any git repo drives a tick, this
// workspace's issues, documents and folders ride along into every sync
// repo on this machine. That is the whole story, and the pane's job is to
// tell it rather than leave a workspace looking unconfigured: this is the
// same "mirroring is what the user is actually asking about" reading that
// BACI-376 landed on the topbar badge, which reports actual mirroring
// (`syncMirroredBy`) rather than per-repo config.
//
// There is deliberately no control here. Per-workspace sync configuration
// (`repo_settings.sync_remote_url`) is a documented follow-on, not this
// pass — so an affordance would be a lie either way.
type WorkspaceSyncNoteProps = {
  board: Board;
};

export default function WorkspaceSyncNote({ board }: WorkspaceSyncNoteProps) {
  const mirroredBy = board.syncMirroredBy || '';
  const when = board.syncLastAt ? formatSyncTime(board.syncLastAt) : '';
  return (
    <div className="mk-workspace-sync-note">
      <div className="mk-workspace-sync-note-head">
        <span className="mk-pill mk-status-workspace">Workspace</span>
        <span>{board.prefix} is mirrored automatically</span>
      </div>
      <div className="mk-workspace-sync-note-body">
        A workspace has no working tree, so it has nowhere to keep a sync
        config of its own and can&apos;t drive a sync run. It has no sync
        settings to configure — which is why it isn&apos;t in the unsynced
        list below.
      </div>
      <div className="mk-workspace-sync-note-body">
        {mirroredBy ? (
          <>
            Its issues, documents and folders are mirrored into{' '}
            <strong>{mirroredBy}</strong> anyway: the export is whole-DB, so
            every run a git repo drives carries this workspace with it
            {when ? <> — last synced {when}</> : <> — no run has completed yet</>}.
            {!board.syncBackgroundEnabled && (
              <> Background sync is currently off for every repo, so nothing is running.</>
            )}
          </>
        ) : (
          <>
            Nothing on this machine mirrors it yet. The export is whole-DB —
            set up sync on any git repo below and this workspace is carried
            along with it, with nothing to configure here.
          </>
        )}
      </div>
    </div>
  );
}
