// Shared option constants for the FeaturesView decomposition (BACI-363).
// Lifted verbatim from the pre-split components/FeaturesView.tsx so the
// segmented controls render identically — the comments document the same
// invariants the inline arrays did.

// BACI-236: Overview vs Graph tab on the right pane. The id matches
// the `viewMode` useState so the segmented control mirrors the active
// tab. Overview is the historical default; switching tabs is a local
// state flip (no refetch / no URL change in v1).
export const VIEW_MODE_OPTIONS = [
  { id: 'overview', label: 'Overview' },
  { id: 'graph', label: 'Graph' },
];

// BACI-177: per-feature "Show on board" toggle. The id is the value
// the toggle should set on the feature — true = show on board (the
// default), false = hide. Matches the two-button mk-segmented shape
// used elsewhere (theme, show-archived, hide-empty-columns).
export const SHOW_ON_BOARD_OPTIONS = [
  { id: true, label: 'Show' },
  { id: false, label: 'Hide' },
];

// BACI-250: per-feature auto-close toggle. The id is the `enabled`
// value the API expects — true = auto-close ON (default; the sweep
// may promote the feature once every child is terminal), false =
// auto-close OFF (long-lived catch-all stays `active`). Same shape as
// SHOW_ON_BOARD_OPTIONS.
export const AUTO_CLOSE_OPTIONS = [
  { id: true, label: 'On' },
  { id: false, label: 'Off' },
];

// BACI-333: per-feature collect-handoffs options. The id is the boolean
// the API expects — true = collect worker close-out handoff comments
// (the default), false = silence a standing bucket like `bugs` /
// `maintenance`. Same shape as AUTO_CLOSE_OPTIONS.
export const COLLECT_HANDOFFS_OPTIONS = [
  { id: true, label: 'On' },
  { id: false, label: 'Off' },
];

// BACI-199: per-feature state options. The id is the canonical
// state string ParseFeatureState accepts; label is the visible button
// caption. Stays in lockstep with model.FeatureState — adding a
// fourth value would mean adding a row here.
export const FEATURE_STATE_OPTIONS = [
  { id: 'active', label: 'Active' },
  { id: 'done', label: 'Done' },
  { id: 'cancelled', label: 'Cancelled' },
];

// Filter strip choices — `all` is the default; the canonical states
// match the segmented control's ids so the same `state` value works
// against either surface.
export const FILTERS = [
  { id: 'all', label: 'All' },
  { id: 'active', label: 'Active' },
  { id: 'done', label: 'Done' },
  { id: 'cancelled', label: 'Cancelled' },
];
