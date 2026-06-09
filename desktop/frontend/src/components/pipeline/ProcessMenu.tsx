import React, { useState } from 'react';
import { stageLabel, stageGlyph } from '../../lib/pipelineProcesses';
import type { ProcessSelection, BoardCardJob } from '../../api';

// SEG_ORDER is the fixed canonical order the stage toggles render and
// assemble in, regardless of click order (Design → Plan → Implement →
// Ship/Shelve/Done). Ship, Shelve, and Mark-done are the three terminal
// sentinels — all final-only and mutually exclusive (TERMINAL_SEGS), so at
// most one lights.
const SEG_ORDER = ['design', 'plan', 'implement', 'ship', 'shelve', 'mark_done'];

// TERMINAL_SEGS are the mutually-exclusive terminal sentinels in the
// segmented bar: Ship hands the card off to Shipping; Shelve (BACI-332)
// returns it to Backlog; Mark-done (BACI-352) closes it out as done.
// Turning one on clears the others.
const TERMINAL_SEGS = ['ship', 'shelve', 'mark_done'];

// STANDALONE_OPTIONS are the no-chain single-agent rows offered below the
// segmented stage toggles — each is a distinct agent that runs one pass
// and finishes in place, mutually exclusive with the four toggles and
// with each other. Large Plan is a big planning pass; Scope and Research
// are the BACI-300 triage stages that replaced the retired manual-triage
// dispatch path.
const STANDALONE_OPTIONS = ['plan_large', 'scope', 'research'];

// ProcessMenu — the "pick a process" overlay shown over a freshly-entered
// card (no chain yet) or when editing (BACI-299, Option C: the segmented
// checklist). Four independent stage toggles (Design / Plan / Implement /
// Ship) light up when on and assemble into a chain in canonical order; the
// lit segments ARE the preview, with a muted caption beneath. Below an "or"
// divider, a set of standalone rows (Large Plan / Scope / Research) are each
// mutually exclusive with the four toggles and with each other. The issue
// card sits dimmed under the scrim.
// StageFlags is the on/off state of the five segmented stage toggles,
// keyed by stage slug. Indexed by plain strings (SEG_ORDER / mode), so a
// string-keyed Record rather than a fixed-key shape.
type StageFlags = Record<string, boolean>;

type ProcessMenuProps = {
  dimmedHasProcess?: boolean;
  jobs?: BoardCardJob[];
  onPick: (selection: ProcessSelection) => void;
  onPickAuto: (selection: ProcessSelection) => void;
  onCancel?: (() => void) | null;
};

export default function ProcessMenu({ dimmedHasProcess, jobs, onPick, onPickAuto, onCancel }: ProcessMenuProps) {
  // Lazy initialiser: on Edit Process pre-fill from the card's existing job
  // modes (a standalone option wins if present); on a fresh card default to
  // Plan + Implement + Ship on — the plan-implement-ship chain is the most
  // common end-to-end flow, so it's pre-selected and a user can Confirm with
  // no extra clicks (BACI-331).
  const existingStandalone = () =>
    STANDALONE_OPTIONS.find(slug => (jobs || []).some(j => j.mode === slug)) || null;
  const emptyStages = (): StageFlags => ({ design: false, plan: false, implement: false, ship: false, shelve: false, mark_done: false });
  const [stages, setStages] = useState<StageFlags>(() => {
    if (dimmedHasProcess && existingStandalone()) {
      return emptyStages();
    }
    if (dimmedHasProcess) {
      const modes = new Set((jobs || []).map(j => j.mode));
      return {
        design: modes.has('design'),
        plan: modes.has('plan'),
        implement: modes.has('implement'),
        ship: modes.has('ship'),
        shelve: modes.has('shelve'),
        mark_done: modes.has('mark_done'),
      };
    }
    return { design: false, plan: true, implement: true, ship: true, shelve: false, mark_done: false };
  });
  // `standalone` is the selected standalone slug (or null when the
  // segmented chain is active).
  const [standalone, setStandalone] = useState<string | null>(() =>
    dimmedHasProcess ? existingStandalone() : null
  );

  // toggleStage flips one segment and clears any standalone selection
  // (the two are mutually exclusive). Turning a terminal sentinel
  // (Ship / Shelve) on clears the other — at most one terminal at a time.
  const toggleStage = (mode: string) => {
    setStandalone(null);
    setStages(s => {
      const next = { ...s, [mode]: !s[mode] };
      if (!s[mode] && TERMINAL_SEGS.includes(mode)) {
        for (const t of TERMINAL_SEGS) {
          if (t !== mode) next[t] = false;
        }
      }
      return next;
    });
  };
  // selectStandalone toggles a standalone row; turning one on clears all
  // segments and supersedes any other standalone, turning the active
  // one off leaves the segbar empty.
  const selectStandalone = (slug: string) => {
    setStandalone(cur => {
      const next = cur === slug ? null : slug;
      if (next) setStages(emptyStages());
      return next;
    });
  };

  // `selected` is the canonical-order chain assembled from the toggles (or
  // the chosen standalone) — the single source of truth for both the
  // caption and the Confirm payload, so the lit bar and the sent list never
  // drift.
  const selected = standalone
    ? [standalone]
    : SEG_ORDER.filter(mode => stages[mode]);
  const canConfirm = selected.length > 0;

  // A connector lights only when BOTH flanking segments are on, so a gap
  // (e.g. Plan skipped between Design and Implement) reads honestly.
  const segConnActive = (i: number) => stages[SEG_ORDER[i]] && stages[SEG_ORDER[i + 1]];

  return (
    <div className="mk-pl-procmenu">
      <div className="mk-pl-procmenu-title">
        {dimmedHasProcess ? 'Replace the process' : 'Pick a process'}
      </div>

      <div className={`mk-pl-segbar${standalone ? ' is-dim' : ''}`}>
        {SEG_ORDER.map((mode, i) => {
          const on = stages[mode];
          return (
            <React.Fragment key={mode}>
              {i > 0 && (
                <span className={`mk-pl-step-conn${segConnActive(i - 1) ? ' is-active' : ''}`} />
              )}
              <button
                type="button"
                className={`mk-pl-seg is-${mode}${on ? ' is-on' : ''}`}
                disabled={!!standalone}
                onClick={() => toggleStage(mode)}
                title={on ? `${stageLabel(mode)} on — click to drop it` : `Add ${stageLabel(mode)}`}
              >
                {on && <span className="mk-pl-seg-tick">✓</span>}
                <span className="mk-pl-seg-glyph">{stageGlyph(mode)}</span>
                <span className="mk-pl-seg-lbl">{stageLabel(mode)}</span>
              </button>
            </React.Fragment>
          );
        })}
      </div>

      <div className={`mk-pl-segcap${!standalone && !canConfirm ? ' is-empty' : ''}`}>
        {standalone
          ? ' '
          : canConfirm
            ? selected.map(stageLabel).join(' → ')
            : 'pick at least one stage'}
      </div>

      <div className="mk-pl-or"><span>or</span></div>

      {STANDALONE_OPTIONS.map((slug) => {
        const on = standalone === slug;
        return (
          <button
            key={slug}
            type="button"
            className={`mk-pl-largeplan${on ? ' is-selected' : ''}`}
            onClick={() => selectStandalone(slug)}
            title={`Run the standalone ${stageLabel(slug)} agent (can't be chained)`}
          >
            <span className="mk-pl-largeplan-glyph">{stageGlyph(slug)}</span>
            <span className="mk-pl-largeplan-lbl">{stageLabel(slug)}</span>
            <span className="mk-pl-largeplan-note">{on ? 'selected' : 'standalone'}</span>
          </button>
        );
      })}

      {/* BACI-334: twin confirm buttons. Confirm sets the process (Auto
          stays off — unchanged); Confirm + Auto sets the process AND turns
          Auto on in one click. Both share the same canConfirm gate. */}
      <div className="mk-pl-procbtns">
        <button
          type="button"
          className="mk-pl-procopt is-confirm"
          disabled={!canConfirm}
          onClick={() => onPick({ stages: selected })}
        >
          Confirm
        </button>
        <button
          type="button"
          className="mk-pl-procopt is-confirm is-confirm-auto"
          disabled={!canConfirm}
          onClick={() => onPickAuto({ stages: selected })}
          title="Set the process and run it automatically (Auto)"
        >
          Confirm + Auto
        </button>
      </div>

      {onCancel && (
        <button type="button" className="mk-pl-procopt is-cancel" onClick={onCancel}>
          Cancel
        </button>
      )}
    </div>
  );
}
