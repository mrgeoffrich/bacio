import React, { useState, useEffect, useCallback } from 'react';
import Icon from './Icon.jsx';
import * as api from '../api';

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

const HIDE_EMPTY_OPTIONS = [
  { id: false, label: 'Off' },
  { id: true, label: 'On' },
];

// SettingsView is the desktop Settings screen — a full-screen view (not
// a modal) covering the content area below the topbar. It owns theme
// selection plus the per-stage dispatch prompt config: the template
// body and the state-gate (which issue states the prompt is valid to
// run from). `columns` is the bacio state vocabulary, used to render
// the state-gate toggles.
export default function SettingsView({ theme, onChangeTheme, hideEmptyColumns, onChangeHideEmptyColumns, columns, onClose }) {
  // `templates` holds the persisted per-stage config from the backend;
  // `drafts` holds in-flight body textarea edits, keyed by mode, so a
  // body save only fires on blur. State-gate edits save immediately.
  const [templates, setTemplates] = useState([]);
  const [placeholders, setPlaceholders] = useState([]);
  const [drafts, setDrafts] = useState({});
  const [savingMode, setSavingMode] = useState(null);
  const [tmplError, setTmplError] = useState(null);
  const [bacioVer, setBacioVer] = useState('');

  useEffect(() => {
    let cancelled = false;
    Promise.all([api.listPromptTemplates(), api.promptPlaceholders(), api.bacioVersion()])
      .then(([tpls, ph, ver]) => {
        if (cancelled) return;
        setTemplates(tpls);
        setPlaceholders(ph);
        setDrafts(Object.fromEntries(tpls.map(t => [t.mode, t.body])));
        setBacioVer(ver);
        setTmplError(null);
      })
      .catch(err => { if (!cancelled) setTmplError(err.message); });
    return () => { cancelled = true; };
  }, []);

  const saveTemplate = useCallback(async (mode, body) => {
    setSavingMode(mode);
    try {
      const updated = await api.savePromptTemplate(mode, body);
      setTemplates(prev => prev.map(t => (t.mode === mode ? updated : t)));
      setDrafts(prev => ({ ...prev, [mode]: updated.body }));
      setTmplError(null);
    } catch (err) {
      setTmplError(err.message);
    } finally {
      setSavingMode(null);
    }
  }, []);

  const saveStates = useCallback(async (mode, states) => {
    setSavingMode(mode);
    try {
      const updated = await api.savePromptStates(mode, states);
      setTemplates(prev => prev.map(t => (t.mode === mode ? updated : t)));
      setTmplError(null);
    } catch (err) {
      setTmplError(err.message);
    } finally {
      setSavingMode(null);
    }
  }, []);

  // toggleState flips one state in a stage's gate, rebuilding the list
  // in canonical `columns` order so the stored set stays stable.
  const toggleState = useCallback((t, state) => {
    const on = new Set(t.allowedStates || []);
    if (on.has(state)) on.delete(state);
    else on.add(state);
    const next = columns.map(c => c.state).filter(s => on.has(s));
    saveStates(t.mode, next);
  }, [columns, saveStates]);

  return (
    <div className="mk-settings-view">
      <header className="mk-settings-bar">
        <h2 className="mk-settings-title">Settings</h2>
        <button className="mk-icbtn" aria-label="Close" onClick={onClose}>
          <Icon name="x" />
        </button>
      </header>

      <div className="mk-settings-body">
        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Theme</div>
            <div className="mk-settings-hint">System follows your OS appearance.</div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Theme">
            {THEME_OPTIONS.map(opt => (
              <button
                key={opt.id}
                className={`mk-segmented-btn ${theme === opt.id ? 'is-active' : ''}`}
                aria-pressed={theme === opt.id}
                onClick={() => onChangeTheme(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Hide empty board columns</div>
            <div className="mk-settings-hint">Columns with no cards are hidden from the board.</div>
          </div>
          <div className="mk-segmented" role="group" aria-label="Hide empty board columns">
            {HIDE_EMPTY_OPTIONS.map(opt => (
              <button
                key={String(opt.id)}
                className={`mk-segmented-btn ${hideEmptyColumns === opt.id ? 'is-active' : ''}`}
                aria-pressed={hideEmptyColumns === opt.id}
                onClick={() => onChangeHideEmptyColumns(opt.id)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </section>

        <section className="mk-settings-section">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Prompt templates</div>
            <div className="mk-settings-hint">
              The instruction sent to an agent when you dispatch a job at each stage,
              and the issue states each stage's prompt can be launched from.
            </div>
          </div>
          {placeholders.length > 0 && (
            <div className="mk-tmpl-tokens">
              Placeholders:{' '}
              {placeholders.map((p, i) => (
                <React.Fragment key={p}>
                  {i > 0 && ' '}
                  <code>{`{{${p}}}`}</code>
                </React.Fragment>
              ))}
            </div>
          )}
          {tmplError && <div className="mk-settings-error">{tmplError}</div>}
          {templates.map(t => {
            const draft = drafts[t.mode] ?? t.body;
            const dirty = draft !== t.body;
            const busy = savingMode === t.mode;
            const allowed = new Set(t.allowedStates || []);
            return (
              <div className="mk-tmpl" key={t.mode}>
                <div className="mk-tmpl-head">
                  <span className="mk-tmpl-label">{t.label}</span>
                  <button
                    className="mk-tmpl-reset"
                    disabled={busy || (t.isDefault && !dirty)}
                    onClick={() => saveTemplate(t.mode, '')}
                  >
                    Reset to default
                  </button>
                </div>
                <textarea
                  className="mk-tmpl-input"
                  value={draft}
                  rows={3}
                  disabled={busy}
                  spellCheck={false}
                  onChange={e => setDrafts(prev => ({ ...prev, [t.mode]: e.target.value }))}
                  onBlur={() => { if (dirty) saveTemplate(t.mode, draft); }}
                />
                <div className="mk-tmpl-states">
                  <div className="mk-tmpl-states-head">
                    <span className="mk-tmpl-states-label">Valid from</span>
                    <button
                      className="mk-tmpl-reset"
                      disabled={busy || t.statesAreDefault}
                      onClick={() => saveStates(t.mode, [])}
                    >
                      Reset to default
                    </button>
                  </div>
                  <div className="mk-tmpl-states-chips">
                    {columns.map(col => (
                      <button
                        key={col.state}
                        className={`mk-state-chip ${allowed.has(col.state) ? 'is-on' : ''}`}
                        aria-pressed={allowed.has(col.state)}
                        disabled={busy}
                        onClick={() => toggleState(t, col.state)}
                      >
                        {col.label}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            );
          })}
        </section>

        <section className="mk-settings-row">
          <div className="mk-settings-row-text">
            <div className="mk-settings-label">Bacio version</div>
            <div className="mk-settings-hint">
              The version of the bacio binary this desktop app is running.
              Cross-check against the "Bacio version" on each agent in the
              Agents panel to spot agents talking to outdated channels.
            </div>
          </div>
          <div className="mk-settings-value"><code>{bacioVer || '—'}</code></div>
        </section>
      </div>
    </div>
  );
}
