// BACI-247: pure data helper for the kanban zap-button popover body.
//
// `computeZapMenuTree` consumes the filter-narrowed `visible` slice
// the `DispatchMenuContent` shell hands its `renderRows` callback,
// buckets it via `promotePrompts`, and emits the structured shape the
// React renderer in `dispatchMenuRows.jsx` walks. Splitting the data
// shape out of the JSX lets the Node smoketest pin the bucketing +
// compound-matrix shape without needing a JSX loader.
//
// Source of truth for the bucketing rule lives in `promotePrompts.ts`
// (BACI-245). The compound-matrix rule mirrors BACI-209: the
// follow-on for each primary is every *other* mode in the visible
// slice (so the dispatch shell's filter input narrows compound rows
// as the user types). `hasOnDispatchChain` is the caller's
// `Boolean(onDispatchChain)` — when the chain handler is absent the
// compound rows are suppressed entirely.

import { promotePrompts } from './promotePrompts.ts';
import type { PromoteStateGraph, PromotePromptInput } from './promotePrompts.ts';

export interface ZapMenuPrompt extends PromotePromptInput {
  mode: string;
  label?: string;
  actionLabel?: string;
}

export interface ZapMenuPrimary {
  prompt: ZapMenuPrompt;
  compounds: ZapMenuPrompt[];
}

export interface ZapMenuTree {
  primaries: ZapMenuPrimary[];
  unusual: ZapMenuPrompt[];
}

export function computeZapMenuTree(args: {
  visible: readonly ZapMenuPrompt[] | null | undefined;
  promptConfig: readonly ZapMenuPrompt[] | null | undefined;
  cardColumn: string;
  stateGraph?: PromoteStateGraph | null;
  hasOnDispatchChain?: boolean;
}): ZapMenuTree {
  const { visible, promptConfig, cardColumn, stateGraph, hasOnDispatchChain } = args;
  const buckets = promotePrompts(visible || [], cardColumn, stateGraph ?? null);
  const visibleModes = new Set((visible || []).map(p => p.mode));

  const primaries: ZapMenuPrimary[] = buckets.primary.map(p => {
    // The compound-matrix follow-on set: every *other* mode the user
    // can currently see in the filter slice. When onDispatchChain is
    // absent (caller didn't wire chain dispatch), the matrix
    // collapses to an empty list — the primary stays clickable, the
    // expander just has nothing to reveal.
    const compounds = hasOnDispatchChain && promptConfig
      ? promptConfig.filter(q => q.mode !== p.mode && visibleModes.has(q.mode))
      : [];
    return { prompt: p, compounds };
  });

  return {
    primaries,
    unusual: buckets.unusual.slice(),
  };
}
