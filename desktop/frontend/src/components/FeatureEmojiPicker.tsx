import React, { useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';

// BACI-172: per-feature emoji picker.
//
// Implementation note: the plan considered the npm `frimousse` package
// for a full Unicode emoji grid, but the worktree-isolated agent run
// can't safely add a new npm dep without breaking the desktop build
// (the Wails frontend bundles into the CLI binary via `./build.sh`,
// so a fresh dep needs the lockfile + the bundled assets to land
// together). Keeping the dep surface stable is more important than
// shipping the full grid in v1.
//
// The compromise: a curated grid of common "feature brand" emojis
// (workflows, areas of code, status concepts) inside an existing
// Radix `DropdownMenu`, plus a free-form text input for anything not
// in the grid. The store-side validator still enforces "exactly one
// grapheme cluster or empty", so the text input can't slip nonsense
// past the server.
//
// Future v2: swap the grid for `frimousse` or `emoji-picker-element`
// once we're ready to take that dep — the seam is this component.

// CURATED is the v1 grid of common feature-brand emojis, grouped
// loosely by intent. Order is curated rather than alphabetical — the
// most-likely-picked icons sit in the first row.
const CURATED = [
  '🔐', '🪲', '🚀', '⚡', '🔧', '🛠️', '🧪', '🎨',
  '📦', '📚', '📝', '📊', '🔍', '🔔', '🧭', '🗂️',
  '⚙️', '🧱', '🪛', '🔩', '🪜', '🪤', '🪝', '🛡️',
  '🌱', '🌿', '🌳', '🍃', '🌸', '🌼', '🌟', '✨',
  '🐛', '🐞', '🦋', '🦄', '🦊', '🐙', '🐳', '🐢',
  '💡', '💬', '💭', '📣', '📢', '🪄', '🎯', '🏁',
];

// FeatureEmojiPicker renders a clickable button showing the current
// emoji (or a placeholder when none is set). Clicking opens a Radix
// DropdownMenu with the curated grid + a text input for free-form
// entry. onSelect fires for both grid clicks and the input commit
// (Enter), receiving the new emoji string (possibly empty to clear).
export default function FeatureEmojiPicker({ value, onSelect, disabled }) {
  const [draft, setDraft] = useState('');
  const [open, setOpen] = useState(false);
  const commit = (next) => {
    onSelect(next);
    setDraft('');
    setOpen(false);
  };
  return (
    <DropdownMenu.Root open={open} onOpenChange={setOpen}>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="mk-features-emoji-btn"
          aria-label={value ? `Feature emoji: ${value}. Click to change.` : 'Set feature emoji'}
          title={value ? 'Click to change the feature emoji' : 'Click to set a feature emoji'}
          disabled={disabled}
        >
          {value || <span className="mk-features-emoji-placeholder">＋</span>}
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="mk-features-emoji-menu"
          align="start"
          side="bottom"
          sideOffset={4}
          collisionPadding={8}
        >
          <div className="mk-features-emoji-grid">
            {CURATED.map(e => (
              <button
                key={e}
                type="button"
                className={`mk-features-emoji-cell ${e === value ? 'is-active' : ''}`}
                aria-label={`Pick ${e}`}
                onClick={() => commit(e)}
              >
                {e}
              </button>
            ))}
          </div>
          <div className="mk-features-emoji-custom">
            <input
              type="text"
              className="mk-features-emoji-input"
              placeholder="Custom emoji…"
              value={draft}
              maxLength={16}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && draft) {
                  e.preventDefault();
                  commit(draft);
                }
              }}
            />
            <button
              type="button"
              className="mk-features-emoji-clear"
              onClick={() => commit('')}
              title="Clear the feature emoji"
            >
              Clear
            </button>
          </div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
