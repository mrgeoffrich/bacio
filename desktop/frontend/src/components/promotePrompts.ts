// BACI-241 / BACI-245: categorise a prompt list into buckets the
// kanban follow-on popup (and future surfaces) can promote / tuck
// away by prominence.
//
// The rule: a prompt's `allowedStates` describes the issue states
// the prompt is valid to run *from* (the Go source of truth lives
// in `internal/model/agent.go` `builtinPromptStates` — e.g. plan /
// implement / scope / design / research = [todo], review / ship /
// fix_review = [in_review]). We bucket against that:
//
//   primary  — `allowedStates` includes the card's current state.
//              These are the modes that make sense to dispatch (or
//              queue) right now.
//   unusual  — every other prompt. Kept reachable behind a
//              `<details>Show all</details>` block so the user can
//              still escape the categorisation (e.g. dispatch
//              `implement` from a card in `in_review` if they know
//              what they're doing).
//   secondary — always empty under the new semantic. The field stays
//              in `PromotedPrompts<T>` so consumers don't break; the
//              caller's render path already handles
//              `secondary.length === 0` gracefully (the divider only
//              draws when both primary and secondary are non-empty).
//
// The previous BACI-241 implementation intersected `allowedStates`
// against the state-graph's reachable next-states. That comparison
// crossed domains: `allowedStates` is run-from, the graph's edges
// are transition-to. For a card in `todo`, no built-in prompt's
// `allowedStates` contained `in_progress` (the primary next-state),
// so every prompt fell through to the unusual catch-all and got
// hidden behind `Show all` — the BACI-245 bug.
//
// The `graph` parameter stays in the signature for API stability
// (callers already pass `stateGraph` through), but is unused. A
// follow-up can either drop the plumbing or repurpose it (e.g.
// order within `primary` by destination distance).
//
// Pure helper, no React, no DOM. Tested by promotePrompts.smoketest.mjs
// against the standard built-in prompt set.

export interface PromoteStateEdge {
  from: string;
  to: string;
  category: string; // 'primary' | 'secondary' | 'unusual'
}

export interface PromoteStateGraph {
  states: string[];
  edges: PromoteStateEdge[];
}

export interface PromotePromptInput {
  mode: string;
  allowedStates?: string[];
  // Anything else the caller carries on the prompt rows passes through
  // verbatim — we never copy fields, only group references.
  [k: string]: unknown;
}

export interface PromotedPrompts<T extends PromotePromptInput> {
  primary: T[];
  secondary: T[];
  unusual: T[];
}

// promotePrompts buckets the input prompt list against the card's
// current state. Primary gets the prompts whose `allowedStates`
// admits `currentState`; everything else goes into `unusual` so the
// user can still pick it from the `Show all` disclosure. Secondary
// is always empty under the BACI-245 semantic — see the header
// comment for why.
//
// Graceful fallback: missing `currentState` (brand-new repo, corrupt
// state column) returns every prompt under `unusual` so nothing
// disappears. The `graph` argument is unused; kept for plumbing
// stability — see the header comment.
export function promotePrompts<T extends PromotePromptInput>(
  prompts: readonly T[] | null | undefined,
  currentState: string,
  // graph: kept for plumbing stability — currently unused, see header
  // comment / BACI-245 plan.
  _graph: PromoteStateGraph | null | undefined,
): PromotedPrompts<T> {
  const list = Array.isArray(prompts) ? prompts : [];

  // No current state → fall back to "everything under unusual" so the
  // caller can render the flat list without dropping prompts.
  if (!currentState) {
    return { primary: [], secondary: [], unusual: list.slice() };
  }

  const out: PromotedPrompts<T> = { primary: [], secondary: [], unusual: [] };
  for (const p of list) {
    if (!p) continue;
    const allowed = Array.isArray(p.allowedStates) ? p.allowedStates : [];
    if (allowed.includes(currentState)) {
      out.primary.push(p);
    } else {
      // Not valid from this state — surface under unusual so the user
      // can still escape the categorisation (e.g. shipping straight
      // from in_progress). `allowedStates` is a hint, not enforcement.
      out.unusual.push(p);
    }
  }
  return out;
}
