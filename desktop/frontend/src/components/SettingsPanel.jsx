import React from 'react';
import Icon from './Icon.jsx';

const THEME_OPTIONS = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
];

export default function SettingsPanel({ open, theme, onChangeTheme, onClose }) {
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
        </div>
      </div>
    </>
  );
}
