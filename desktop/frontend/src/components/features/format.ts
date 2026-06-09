import { FEATURE_STATE_OPTIONS } from './constants';

// Display helpers shared across the FeaturesView pieces (BACI-363).
// Extracted verbatim from the pre-split components/FeaturesView.tsx.

// stateLabelFor maps a feature state string to the visible label,
// falling back to the raw value so a forward-compat new state still
// renders something. Mirrors stateLabel() for issue states.
export function stateLabelFor(state: string): string {
  const opt = FEATURE_STATE_OPTIONS.find((o) => o.id === state);
  return opt ? opt.label : state || 'Active';
}

// Short date for the feature-list rows and detail metadata line.
export function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString();
}

// relTime renders a coarse "time since" for the list-row updated stamp.
export function relTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return 'in the future';
  const m = Math.floor(ms / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return shortDate(iso);
}

// commentTimestamp formats a feature comment's createdAt for display
// next to its author — matches the issue drawer's comment metadata.
export function commentTimestamp(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}
