import { useState, useEffect, useCallback } from 'react';
import { reportError } from '../../errors';
import * as api from '../../api';
import type { Board, BoardColumn, FeatureSummary } from '../../api';

// BACI-248: Per-repository Settings section — fenced-off pane behind
// its own repo dropdown for the per-repo settings that used to share
// the flat scroll with the global System rows. Three sub-bands today:
//
//   1. Default feature (BACI-235) — lifted down from System; the
//      auto-applied feature for `bacio issue add` with no --feature-slug.
//   2. Board visibility — hidden columns / states (`tui_settings[board.hidden_states]`,
//      surfaced from TUI-only to desktop / web via the new
//      /repos/{prefix}/board/hidden-states endpoint pair).
//   3. Board visibility — hidden features (`tui_settings[board.hidden_features]`,
//      already exposed on the Features screen via the BACI-177
//      features/hidden endpoint; same flag, second surface.)
//
// The repo dropdown at the head defaults to the App-owned activeBoard.
// Switching repos re-runs every effect under it, scoped by repoPrefix.
//
// When the dropdown is unset / "all" the section renders an empty
// state — mirrors how FeaturesView empty-states a cross-repo view.

type PerRepoSettingsSectionProps = {
  activeBoard: string;
  boards: Board[];
  columns: BoardColumn[];
};

export default function PerRepoSettingsSection({
  activeBoard,
  boards,
  columns,
}: PerRepoSettingsSectionProps) {
  // The pane's local repo selection. Defaults to the App-owned active
  // board on mount + when the App switches repos; the user can pick a
  // different repo without changing the global selection.
  const [repoPrefix, setRepoPrefix] = useState(activeBoard || '');
  useEffect(() => {
    if (activeBoard) setRepoPrefix(activeBoard);
  }, [activeBoard]);

  // Resolve the row for the dropdown's display (name + prefix). Fallback
  // shape keeps the rest of the pane resilient to an out-of-sync board
  // list (e.g. when the user just added a repo and the dropdown hasn't
  // refreshed yet).
  const selectableBoards = Array.isArray(boards)
    ? boards.filter(b => b && b.prefix && b.prefix !== 'all')
    : [];

  // BACI-235: per-repo default-feature. defaultFeatureSlug is the
  // empty-string sentinel when unset; featureChoices is the list
  // backing the dropdown. Re-runs when repoPrefix changes.
  const [defaultFeatureSlug, setDefaultFeatureSlug] = useState('');
  const [featureChoices, setFeatureChoices] = useState<FeatureSummary[]>([]);
  const [savingDefaultFeature, setSavingDefaultFeature] = useState(false);

  // BACI-248: hidden kanban states (the set, as a Set<string> of
  // canonical state names). Loaded on mount + on repoPrefix change.
  // Saved on each toggle — same blur-commit shape as the rest of
  // SettingsView (toggle === implicit blur).
  const [hiddenStates, setHiddenStates] = useState<Set<string>>(new Set());
  const [savingHiddenStates, setSavingHiddenStates] = useState(false);

  // BACI-177: hidden features (set of slugs). Mirror of the per-feature
  // toggle on the Features screen — flipping it here is visible there
  // on the next refresh because both surfaces talk to the same KV.
  // featureSummaries doubles as the source of feature labels for the
  // chip-list; featureChoices above is the slim shape backing the
  // default-feature dropdown — same data, same fetch, but kept under
  // separate names because they're consumed by different controls.
  const [featureSummaries, setFeatureSummaries] = useState<FeatureSummary[]>([]);
  const [savingHiddenFeatureSlug, setSavingHiddenFeatureSlug] = useState<string | null>(null);

  // Mount + repoPrefix change: parallel-load every per-repo setting.
  // A failure on any one path surfaces once via reportError but doesn't
  // tank the others — the section degrades to per-row empty states.
  useEffect(() => {
    if (!repoPrefix || repoPrefix === 'all') {
      setDefaultFeatureSlug('');
      setFeatureChoices([]);
      setHiddenStates(new Set());
      setFeatureSummaries([]);
      return;
    }
    let cancelled = false;
    Promise.all([
      api.getDefaultFeature(repoPrefix),
      api.listFeatures(repoPrefix),
      api.getBoardHiddenStates(repoPrefix),
    ])
      .then(([def, feats, hidden]) => {
        if (cancelled) return;
        setDefaultFeatureSlug(def?.slug ?? '');
        setFeatureChoices(feats || []);
        setFeatureSummaries(feats || []);
        setHiddenStates(new Set((hidden?.states) || []));
      })
      .catch(err => { if (!cancelled) reportError(err, { headline: "Couldn't load per-repo settings" }); });
    return () => { cancelled = true; };
  }, [repoPrefix]);

  // BACI-235: persist the per-repo default feature. Empty string is
  // the "clear" sentinel — routed through clearDefaultFeature so the
  // audit log records it as a clear, not as a "set to no feature".
  const changeDefaultFeature = useCallback(async (nextSlug: string) => {
    if (!repoPrefix) return;
    setSavingDefaultFeature(true);
    try {
      if (nextSlug === '') {
        await api.clearDefaultFeature(repoPrefix);
        setDefaultFeatureSlug('');
      } else {
        const out = await api.setDefaultFeature(repoPrefix, nextSlug);
        setDefaultFeatureSlug(out?.slug ?? '');
      }
    } catch (err) {
      reportError(err, { headline: "Couldn't save default epic" });
    } finally {
      setSavingDefaultFeature(false);
    }
  }, [repoPrefix]);

  // BACI-248: toggle a single state's hidden flag. The set + the
  // server are both replace-not-merge — we build the new set
  // client-side, fire it as the full new array, and the server returns
  // the canonical sorted slice we re-seed our local set from.
  const toggleHiddenState = useCallback(async (state: string) => {
    if (!repoPrefix || savingHiddenStates) return;
    const next = new Set(hiddenStates);
    if (next.has(state)) next.delete(state);
    else next.add(state);
    setSavingHiddenStates(true);
    // Optimistic — flip immediately, then confirm with the server response.
    setHiddenStates(next);
    try {
      const out = await api.setBoardHiddenStates(repoPrefix, Array.from(next));
      setHiddenStates(new Set(out.states || []));
    } catch (err) {
      reportError(err, { headline: "Couldn't save board-hidden states" });
      // Re-pull on failure so the chip group snaps back to the
      // persisted truth.
      api.getBoardHiddenStates(repoPrefix)
        .then(out => setHiddenStates(new Set(out.states || [])))
        .catch(() => {});
    } finally {
      setSavingHiddenStates(false);
    }
  }, [repoPrefix, hiddenStates, savingHiddenStates]);

  // BACI-177: flip the per-feature "Show on board" toggle. Updates the
  // local featureSummaries row in place so the chip flips immediately,
  // then awaits the server response. On failure the chip snaps back to
  // the server's persisted truth (the response is the refreshed
  // FeatureDetail, mapped onto the slim summary shape).
  const toggleFeatureHidden = useCallback(async (slug: string, nextHidden: boolean) => {
    if (!repoPrefix) return;
    setSavingHiddenFeatureSlug(slug);
    setFeatureSummaries(prev => prev.map(f => (
      f.slug === slug ? { ...f, hiddenOnBoard: nextHidden } : f
    )));
    try {
      await api.setFeatureHiddenOnBoard(repoPrefix, slug, nextHidden);
    } catch (err) {
      reportError(err, { headline: "Couldn't save epic visibility" });
      // Snap back to the persisted truth.
      try {
        const feats = await api.listFeatures(repoPrefix);
        setFeatureSummaries(feats || []);
      } catch { /* swallow — the toast already surfaced */ }
    } finally {
      setSavingHiddenFeatureSlug(null);
    }
  }, [repoPrefix]);

  const repoChosen = !!repoPrefix && repoPrefix !== 'all';

  return (
    <div className="mk-settings-section-pane">
      <header className="mk-settings-section-head">
        <h3 className="mk-settings-section-title">Per-repository</h3>
        <p className="mk-settings-section-subtitle">
          Settings that apply to one repo. Defaults to the repository
          you have selected in the topbar; switch from the dropdown to
          edit another.
        </p>
      </header>

      <section className="mk-settings-row mk-settings-repo-row">
        <div className="mk-settings-row-text">
          <div className="mk-settings-label">Repository</div>
          <div className="mk-settings-hint">
            The repo every setting in this section applies to.
          </div>
        </div>
        <select
          className="mk-tmpl-input"
          value={repoChosen ? repoPrefix : ''}
          aria-label="Repository for per-repo settings"
          onChange={e => setRepoPrefix(e.target.value)}
        >
          {!repoChosen && <option value="">(none selected)</option>}
          {selectableBoards.map(b => (
            <option key={b.prefix} value={b.prefix}>
              {b.name} · {b.prefix}
            </option>
          ))}
        </select>
      </section>

      {!repoChosen ? (
        <div className="mk-settings-section-empty">
          Select a repository above (or from the topbar) to edit its
          per-repo settings.
        </div>
      ) : (
        <>
          <section className="mk-settings-row">
            <div className="mk-settings-row-text">
              <div className="mk-settings-label">
                Default epic ({repoPrefix})
              </div>
              <div className="mk-settings-hint">
                BACI-235: when set, issues created without an explicit
                epic auto-apply to this epic. An explicit epic on
                the new-issue form always wins; deleting the referenced
                epic clears the setting automatically.
              </div>
            </div>
            <select
              className="mk-tmpl-input"
              value={defaultFeatureSlug}
              disabled={savingDefaultFeature}
              aria-label="Default epic for new issues"
              onChange={e => changeDefaultFeature(e.target.value)}
            >
              <option value="">(none)</option>
              {featureChoices.map(f => (
                <option key={f.slug} value={f.slug}>
                  {f.emoji ? `${f.emoji} ` : ''}{f.title}
                </option>
              ))}
            </select>
          </section>

          {/* Board visibility — hidden columns / states. The chip group
              mirrors the TUI's hidden-states picker; flipping a chip
              persists immediately. */}
          <section className="mk-settings-section">
            <div className="mk-settings-row-text">
              <div className="mk-settings-label">Hidden columns</div>
              <div className="mk-settings-hint">
                Click a column to hide it on this repo&apos;s kanban board.
                Hidden columns stay editable everywhere else (issue
                workspace, command palette, CLI); they&apos;re just absent
                from the board view. Visible to the TUI on its next reload.
              </div>
            </div>
            <div className="mk-tmpl-states-chips">
              {columns.map(col => {
                const on = hiddenStates.has(col.state);
                return (
                  <button
                    key={col.state}
                    className={`mk-state-chip ${on ? 'is-on' : ''}`}
                    aria-pressed={on}
                    disabled={savingHiddenStates}
                    onClick={() => toggleHiddenState(col.state)}
                  >
                    {col.label}
                  </button>
                );
              })}
            </div>
          </section>

          {/* Board visibility — hidden features. Mirrors the
              BACI-177 per-feature toggle on the Features screen; both
              surfaces talk to the same per-repo KV. */}
          <section className="mk-settings-section">
            <div className="mk-settings-row-text">
              <div className="mk-settings-label">Hidden epics</div>
              <div className="mk-settings-hint">
                Click an epic to hide every kanban card belonging to
                it on this repo&apos;s board. Same flag as the &quot;Show on board&quot;
                toggle on the Epics screen; the surfaces stay in
                lockstep.
              </div>
            </div>
            {featureSummaries.length === 0 ? (
              // `bacio feature add` is the literal CLI verb — the Epics
              // rename is display-only and stops at the shell prompt.
              <div className="mk-settings-section-empty">
                This repo has no epics yet. Add one with{' '}
                <code>bacio feature add</code> or from the Epics panel.
              </div>
            ) : (
              <div className="mk-tmpl-states-chips">
                {featureSummaries.map(f => {
                  const on = !!f.hiddenOnBoard;
                  const busy = savingHiddenFeatureSlug === f.slug;
                  return (
                    <button
                      key={f.slug}
                      className={`mk-state-chip ${on ? 'is-on' : ''}`}
                      aria-pressed={on}
                      disabled={busy}
                      onClick={() => toggleFeatureHidden(f.slug, !on)}
                    >
                      {f.emoji ? `${f.emoji} ` : ''}{f.title}
                    </button>
                  );
                })}
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}
