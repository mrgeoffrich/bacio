// ControlsBar — the top-of-page strip on the transcript viewer
// (BACI-125). Filter chips per tool actually used; toggles for
// "hide envelope attachments" (default on) and "show per-event
// usage"; a jump-to-next-error button that walks the item list
// forward from the current cursor.

import React from 'react';

type ControlsBarProps = {
  toolNamesUsed: string[];
  toolFilter: Set<string>;
  onToggleTool: (name: string) => void;
  onClearFilter: () => void;
  hideEnvelope: boolean;
  onToggleHideEnvelope: (next: boolean) => void;
  showPerEventUsage: boolean;
  onToggleUsage: (next: boolean) => void;
  hasErrors: boolean;
  onJumpToNextError: () => void;
  warnings: string[];
};

export default function ControlsBar({
  toolNamesUsed,
  toolFilter,
  onToggleTool,
  onClearFilter,
  hideEnvelope,
  onToggleHideEnvelope,
  showPerEventUsage,
  onToggleUsage,
  hasErrors,
  onJumpToNextError,
  warnings,
}: ControlsBarProps): React.ReactElement {
  return (
    <div className="mk-transcript-controls">
      <div className="mk-transcript-control-group">
        <button
          type="button"
          className="mk-btn-secondary"
          onClick={onJumpToNextError}
          disabled={!hasErrors}
          title={
            hasErrors
              ? 'Scroll to the next tool result that errored'
              : 'No errors in this transcript'
          }
        >
          Jump to next error
        </button>
      </div>
      <div className="mk-transcript-control-group">
        <label className="mk-transcript-control-toggle">
          <input
            type="checkbox"
            checked={hideEnvelope}
            onChange={e => onToggleHideEnvelope(e.target.checked)}
          />{' '}
          Hide envelope attachments
        </label>
        <label className="mk-transcript-control-toggle">
          <input
            type="checkbox"
            checked={showPerEventUsage}
            onChange={e => onToggleUsage(e.target.checked)}
          />{' '}
          Show per-event usage
        </label>
      </div>
      {toolNamesUsed.length > 0 && (
        <div className="mk-transcript-control-group mk-transcript-filter-row">
          <span className="mk-transcript-filter-label">Filter tools:</span>
          {toolNamesUsed.map(name => (
            <button
              key={name}
              type="button"
              className={`mk-transcript-filter-chip ${
                toolFilter.has(name) ? 'is-active' : ''
              }`}
              onClick={() => onToggleTool(name)}
              title={
                toolFilter.has(name)
                  ? `Hide ${name} calls`
                  : `Filter to ${name} calls (and others already on)`
              }
            >
              {name}
            </button>
          ))}
          {toolFilter.size > 0 && (
            <button
              type="button"
              className="mk-transcript-filter-clear"
              onClick={onClearFilter}
            >
              Clear
            </button>
          )}
        </div>
      )}
      {warnings.length > 0 && (
        <div className="mk-transcript-warnings" role="alert">
          <strong>{warnings.length}</strong> malformed line
          {warnings.length === 1 ? '' : 's'} skipped:&nbsp;
          <span className="mk-transcript-warnings-text">
            {warnings.slice(0, 3).join(' · ')}
            {warnings.length > 3 && ` (+${warnings.length - 3} more)`}
          </span>
        </div>
      )}
    </div>
  );
}
