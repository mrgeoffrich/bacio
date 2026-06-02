import React from 'react';

// BACI-248: vertical three-entry rail for the sectioned Settings page.
// Same active/hover treatment as the .mk-segmented family the rest of
// the app uses for two/three-choice controls, stacked into a column
// rail that DocsView / FeaturesView's left-rail layout already
// rhymes with.
//
// `sections` is the rail's data — one entry per section with an id,
// label, and optional brief description rendered below the label.
// `activeId` is the section currently mounted in the main pane;
// onSelect fires when the user clicks an entry.

export default function SectionRail({ sections, activeId, onSelect }) {
  return (
    <nav className="mk-settings-rail" aria-label="Settings sections">
      {sections.map(s => {
        const isActive = s.id === activeId;
        return (
          <button
            key={s.id}
            type="button"
            className={`mk-settings-rail-btn ${isActive ? 'is-active' : ''}`}
            aria-pressed={isActive}
            onClick={() => onSelect(s.id)}
          >
            <span className="mk-settings-rail-btn-label">{s.label}</span>
            {s.hint && (
              <span className="mk-settings-rail-btn-hint">{s.hint}</span>
            )}
          </button>
        );
      })}
    </nav>
  );
}
