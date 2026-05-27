// BACI-252: pure data helper for the kanban zap-button popover body.
//
// `computeZapMenuTree` consumes the filter-narrowed `visible` slice the
// `DispatchMenuContent` shell hands its `renderRows` callback, and emits
// a flat list of dispatchable primaries paired with their BACI-209
// compound-chain follow-ons. There is no per-state bucketing — every
// visible mode is a primary. Reserved/internal slugs (the leading-
// underscore convention; `_dispatch_preamble` today) are expected to be
// filtered upstream by the caller before they reach `visible`.
//
// The compound-matrix rule mirrors BACI-209: the follow-on for each
// primary is every *other* mode in the visible slice (so the dispatch
// shell's filter input narrows compound rows as the user types).
// `hasOnDispatchChain` is the caller's `Boolean(onDispatchChain)` —
// when the chain handler is absent the compound rows are suppressed
// entirely.

export interface ZapMenuPrompt {
  mode: string;
  label?: string;
  actionLabel?: string;
  // Anything else the caller carries on the prompt rows passes through
  // verbatim — we never copy fields, only group references.
  [k: string]: unknown;
}

export interface ZapMenuPrimary {
  prompt: ZapMenuPrompt;
  compounds: ZapMenuPrompt[];
}

export interface ZapMenuTree {
  primaries: ZapMenuPrimary[];
}

export function computeZapMenuTree(args: {
  visible: readonly ZapMenuPrompt[] | null | undefined;
  hasOnDispatchChain?: boolean;
}): ZapMenuTree {
  const { visible, hasOnDispatchChain } = args;
  const list = Array.isArray(visible) ? visible : [];

  const primaries: ZapMenuPrimary[] = list.map(p => {
    // The compound-matrix follow-on set: every *other* mode the user
    // can currently see in the filter slice. When onDispatchChain is
    // absent (caller didn't wire chain dispatch), the matrix
    // collapses to an empty list — the primary stays clickable, the
    // expander just has nothing to reveal.
    const compounds = hasOnDispatchChain
      ? list.filter(q => q.mode !== p.mode)
      : [];
    return { prompt: p, compounds };
  });

  return { primaries };
}
