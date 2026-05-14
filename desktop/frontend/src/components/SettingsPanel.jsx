import React, { useState, useEffect, useCallback } from 'react';
import Icon from './Icon.jsx';
import * as api from '../api';

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

export default function SettingsPanel({ open, theme, onChangeTheme, onClose }) {
  // Prompt templates load lazily when the panel opens. `templates` holds
  // the persisted state from the backend; `drafts` holds the in-flight
  // textarea edits, keyed by mode, so a save only fires on blur/reset.
  const [templates, setTemplates] = useState([]);
  const [placeholders, setPlaceholders] = useState([]);
  const [drafts, setDrafts] = useState({});
  const [savingMode, setSavingMode] = useState(null);
  const [tmplError, setTmplError] = useState(null);

  useEffect(() => {
    if (!open) return undefined;
    let cancelled = false;
    Promise.all([api.listPromptTemplates(), api.promptPlaceholders()])
      .then(([tpls, ph]) => {
        if (cancelled) return;
        setTemplates(tpls);
        setPlaceholders(ph);
        setDrafts(Object.fromEntries(tpls.map(t => [t.mode, t.body])));
        setTmplError(null);
      })
      .catch(err => { if (!cancelled) setTmplError(err.message); });
    return () => { cancelled = true; };
  }, [open]);

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

  if (!open) return null;

  return (
    <>
      <div className="mk-scrim" onClick={onClose} />
      <div className="mk-settings" role="dialog" aria-label="Settings">
        <header className="mk-settings-head">
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

          <section className="mk-settings-section">
            <div className="mk-settings-row-text">
              <div className="mk-settings-label">Prompt templates</div>
              <div className="mk-settings-hint">
                The instruction sent to an agent when you dispatch a job at each stage.
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
                </div>
              );
            })}
          </section>
        </div>
      </div>
    </>
  );
}
