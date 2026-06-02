import { useState, useMemo, useRef } from 'react';
import { useNavigate, useParams } from 'react-router';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import {
  stageLabel,
  stageGlyph,
  isShipStage,
  isShelveStage,
  EDITABLE_JOB_MODES,
} from '../lib/pipelineProcesses';
import { viewPath } from '../lib/routes';
import type { BoardCard } from '../api';

// A single persisted job in the card's process chain ({sequence, mode,
// status}), derived from the BoardCard wire type so it stays in lockstep
// with the api seam rather than being redefined here.
type BoardCardJob = NonNullable<BoardCard['jobs']>[number];

// One pending-tail row in local editor state: a stable uid (so reorder /
// remove survives re-renders even when modes repeat) plus the job mode.
type TailRow = { uid: string; mode: string };

// The error banner surfaced on a failed Save.
type Banner = { kind: string; text: string };

type ProcessEditorProps = {
  cards: BoardCard[];
  activeBoard: string;
  onSave: (key: string, stages: string[]) => Promise<void>;
  onSaveAuto: (key: string, stages: string[]) => Promise<void>;
};

// ProcessEditor (BACI-294) — the full-screen Edit Process screen. It edits
// an in_pipeline card's job chain as a re-orderable list, respecting work
// already done: the completed / running / cancelled jobs are a LOCKED
// prefix (greyed, no controls), and the pending jobs are an editable tail
// the user can re-order (native drag + ↑/↓ fallback), remove (✕), and grow
// (+ Add job over the full dispatchable palette). Save persists the new
// tail via onSave (the App-level editCardProcessTail wrapper) — the client
// sends only the tail; the server reads the locked prefix from the store
// as the source of truth, so a stale client can never corrupt history.
// Cancel discards and returns to the Pipeline.
//
// The screen reads the card's live chain off the cached `cards` array App
// already threads into the Pipeline subtree (each card carries
// `jobs: [{sequence, mode, status}]`). A deep-link before cards have
// loaded renders a loading state, then redirects to the Pipeline if the
// key isn't found.
export default function ProcessEditor({ cards, activeBoard, onSave, onSaveAuto }: ProcessEditorProps) {
  const { key } = useParams();
  const navigate = useNavigate();
  const card = useMemo(
    () => (cards || []).find((c) => c.key === key) || null,
    [cards, key],
  );

  const backToPipeline = () => navigate(viewPath(activeBoard, 'pipeline'));

  // Locked prefix = every job that has advanced past pending; editable tail
  // = the pending jobs, seeded into local state as {uid, mode} rows so a
  // reorder/remove is stable across renders even when modes repeat.
  const jobs: BoardCardJob[] = card?.jobs || [];
  const locked = useMemo(() => jobs.filter((j) => j.status !== 'pending'), [jobs]);
  const seedTail = useMemo<TailRow[]>(
    () =>
      jobs
        .filter((j) => j.status === 'pending')
        .map((j, i) => ({ uid: `seed-${i}-${j.mode}`, mode: j.mode })),
    [jobs],
  );

  const [tail, setTail] = useState<TailRow[]>(seedTail);
  const [saving, setSaving] = useState(false);
  const [banner, setBanner] = useState<Banner | null>(null);
  const uidRef = useRef(0);
  const nextUid = () => `add-${uidRef.current++}`;
  // Track the dragged tail row index for native HTML5 reorder.
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [dragOverIdx, setDragOverIdx] = useState<number | null>(null);

  // The whole projected chain (locked modes + edited tail) drives the
  // client-side ship-last validation that gates Save.
  const chainModes = useMemo(
    () => [...locked.map((j) => j.mode), ...tail.map((t) => t.mode)],
    [locked, tail],
  );
  // A terminal sentinel (Ship hand-off / Shelve demote, BACI-332) may only
  // be the final stage — mirrors the server's ProcessFromStagesWithPrefix
  // gate, so the client refuses Save before the round-trip rather than
  // surfacing a server error.
  const terminalNotLast = chainModes.some(
    (m, i) => (isShipStage(m) || isShelveStage(m)) && i !== chainModes.length - 1,
  );
  // Disable Save when nothing would change, when the chain is empty, or
  // when a terminal sentinel isn't the final stage.
  const tailUnchanged =
    tail.length === seedTail.length && tail.every((t, i) => t.mode === seedTail[i]?.mode);
  const canSave = !saving && chainModes.length > 0 && !terminalNotLast && !tailUnchanged;

  const moveTail = (from: number, to: number) => {
    if (to < 0 || to >= tail.length) return;
    setTail((rows) => {
      const next = rows.slice();
      const [row] = next.splice(from, 1);
      next.splice(to, 0, row);
      return next;
    });
  };
  const removeTail = (uid: string) => setTail((rows) => rows.filter((r) => r.uid !== uid));
  const addJob = (mode: string) => setTail((rows) => [...rows, { uid: nextUid(), mode }]);

  const onDrop = (to: number) => {
    setDragOverIdx(null);
    if (dragIdx == null || dragIdx === to) {
      setDragIdx(null);
      return;
    }
    moveTail(dragIdx, to);
    setDragIdx(null);
  };

  // runSave drives both footer buttons: `save` persists the tail (Auto
  // untouched), `saveAuto` (BACI-334) persists the tail and turns Auto on.
  // They differ only in the App callback passed, so the saving lock and the
  // error-banner lifecycle live here once.
  const runSave = (fn: (key: string, stages: string[]) => Promise<void>) => {
    if (!canSave || !key) return;
    setSaving(true);
    setBanner(null);
    Promise.resolve(fn(key, tail.map((t) => t.mode)))
      .then(() => backToPipeline())
      .catch((err) => {
        setSaving(false);
        setBanner({ kind: 'error', text: err?.message || 'Could not save the process.' });
      });
  };
  const save = () => runSave(onSave);
  const saveAuto = () => runSave(onSaveAuto);

  // Loading / not-found. App threads the cached cards; on a deep-link the
  // array may be empty for a tick. Once cards have loaded but the key is
  // absent, the card was shipped / cancelled under the editor — bounce back.
  if (!card) {
    if ((cards || []).length === 0) {
      return (
        <div className="mk-pe">
          <div className="mk-pe-empty">Loading…</div>
        </div>
      );
    }
    return (
      <div className="mk-pe">
        <div className="mk-pe-empty">
          <p>That card isn’t in the Pipeline any more.</p>
          <button type="button" className="mk-pl-btn is-primary is-sm" onClick={backToPipeline}>
            Back to Pipeline
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="mk-pe">
      <header className="mk-pe-head">
        <div className="mk-pe-title">
          <span className="mk-pe-key">{card.key}</span>
          <span className="mk-pe-name">{card.title}</span>
        </div>
        <p className="mk-pe-sub">
          Re-order, remove, or add the upcoming jobs. Completed and running jobs are locked.
        </p>
      </header>

      <ol className="mk-pe-list">
        {locked.map((j) => {
          const sentinel = isShipStage(j.mode) || isShelveStage(j.mode);
          const shelve = isShelveStage(j.mode);
          let dot = stageGlyph(j.mode);
          if (j.status === 'complete') dot = '✓';
          else if (j.status === 'running') dot = '●';
          else if (j.status === 'cancelled') dot = '✕';
          return (
            <li key={`locked-${j.sequence}`} className={`mk-pe-row is-locked is-${j.status}`}>
              <span className="mk-pe-handle is-disabled" aria-hidden="true">⋮⋮</span>
              <span className={`mk-pe-dot is-${sentinel ? 'handoff' : j.status}${shelve ? ' is-shelve' : ''}`}>{dot}</span>
              <span className="mk-pe-lbl">{stageLabel(j.mode)}</span>
              <span className="mk-pe-status">{j.status}</span>
            </li>
          );
        })}

        {tail.map((row, i) => (
          <li
            key={row.uid}
            className={`mk-pe-row is-pending${dragOverIdx === i ? ' is-drop' : ''}`}
            draggable
            onDragStart={() => setDragIdx(i)}
            onDragOver={(e) => { e.preventDefault(); setDragOverIdx(i); }}
            onDragLeave={(e) => { if (e.currentTarget === e.target) setDragOverIdx(null); }}
            onDrop={(e) => { e.preventDefault(); onDrop(i); }}
            onDragEnd={() => { setDragIdx(null); setDragOverIdx(null); }}
          >
            <span className="mk-pe-handle" title="Drag to re-order">⋮⋮</span>
            <span className={`mk-pe-dot is-${isShipStage(row.mode) || isShelveStage(row.mode) ? 'handoff' : 'pending'}${isShelveStage(row.mode) ? ' is-shelve' : ''}`}>
              {stageGlyph(row.mode)}
            </span>
            <span className="mk-pe-lbl">{stageLabel(row.mode)}</span>
            <span className="mk-pe-spacer" />
            <div className="mk-pe-rowbtns">
              <button
                type="button"
                className="mk-pe-iconbtn"
                title="Move up"
                disabled={i === 0}
                onClick={() => moveTail(i, i - 1)}
              >
                ↑
              </button>
              <button
                type="button"
                className="mk-pe-iconbtn"
                title="Move down"
                disabled={i === tail.length - 1}
                onClick={() => moveTail(i, i + 1)}
              >
                ↓
              </button>
              <button
                type="button"
                className="mk-pe-iconbtn is-remove"
                title="Remove"
                onClick={() => removeTail(row.uid)}
              >
                ✕
              </button>
            </div>
          </li>
        ))}

        {tail.length === 0 && (
          <li className="mk-pe-row is-empty">No upcoming jobs — add one below.</li>
        )}
      </ol>

      <div className="mk-pe-add">
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button type="button" className="mk-pl-btn is-ghost is-sm">+ Add job</button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content className="mk-pe-menu" align="start" side="bottom" sideOffset={4} collisionPadding={8}>
              {EDITABLE_JOB_MODES.map((mode) => (
                <DropdownMenu.Item key={mode} className="mk-pe-menu-item" onSelect={() => addJob(mode)}>
                  <span className="mk-pe-dot is-add">{stageGlyph(mode)}</span>
                  {stageLabel(mode)}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>

      {(banner || terminalNotLast) && (
        <div className="mk-pe-banner is-error" role="alert">
          {banner?.text || 'Ship or Shelve must be the final job in the chain.'}
          {banner && (
            <button type="button" className="mk-pe-banner-x" onClick={() => setBanner(null)} aria-label="Dismiss">
              ✕
            </button>
          )}
        </div>
      )}

      <footer className="mk-pe-foot">
        <button type="button" className="mk-pl-btn is-ghost" onClick={backToPipeline} disabled={saving}>
          Cancel
        </button>
        <button type="button" className="mk-pl-btn is-primary" onClick={save} disabled={!canSave}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button
          type="button"
          className="mk-pl-btn is-primary is-confirm-auto"
          onClick={saveAuto}
          disabled={!canSave}
          title="Save the process and run it automatically (Auto)"
        >
          {saving ? 'Saving…' : 'Save + Auto'}
        </button>
      </footer>
    </div>
  );
}
