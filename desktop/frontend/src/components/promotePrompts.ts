// BACI-241: categorise a prompt list against the canonical state-
// transition graph so the kanban follow-on popup (and future surfaces)
// can promote / demote / tuck-away modes by prominence.
//
// The rule: for each prompt, walk the categories in display-priority
// order (primary → secondary → unusual). The first bucket whose set of
// next-states from the card's current column intersects the prompt's
// `allowedStates` wins. A prompt with no overlap falls into `unusual` —
// we never drop a prompt entirely, because the user might know better
// than the graph (e.g. a custom mode whose intended use sits outside
// the categorisation). Within each bucket, the prompt's original order
// in the input list is preserved so the user's mental model of "where
// Implement lives" stays stable across cards.
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

// Walk the graph edges from `currentState`, returning a `Set` of `to`
// states for the given category. Cheap (≤16 edges, ≤3 callsites per
// render); the resulting set is consumed by `intersects()` below.
function nextStatesFrom(
  graph: PromoteStateGraph | null | undefined,
  from: string,
  category: string,
): Set<string> {
  const out = new Set<string>();
  if (!graph || !Array.isArray(graph.edges)) return out;
  for (const e of graph.edges) {
    if (e && e.from === from && e.category === category) {
      out.add(e.to);
    }
  }
  return out;
}

function intersects(allowed: string[] | undefined, nexts: Set<string>): boolean {
  if (!Array.isArray(allowed) || allowed.length === 0) return false;
  if (nexts.size === 0) return false;
  for (const s of allowed) {
    if (nexts.has(s)) return true;
  }
  return false;
}

// promotePrompts returns three buckets (primary / secondary / unusual)
// of the input prompt list, with each prompt placed in the highest-
// priority bucket whose state set intersects its `allowedStates`. A
// prompt with no overlap lands in `unusual` so the user always has the
// option to escape the graph's opinion. Returns empty buckets when the
// graph is missing — caller renders the input as a flat unusual list,
// which is the graceful-fallback shape.
export function promotePrompts<T extends PromotePromptInput>(
  prompts: readonly T[] | null | undefined,
  currentState: string,
  graph: PromoteStateGraph | null | undefined,
): PromotedPrompts<T> {
  const list = Array.isArray(prompts) ? prompts : [];

  // Graph absent / unknown current state → every prompt falls under
  // `unusual` so nothing is hidden. The wire scheme treats `unusual`
  // as the "tucked away" bucket but the caller can render it flat in
  // the fallback case.
  if (!graph || !currentState) {
    return { primary: [], secondary: [], unusual: list.slice() };
  }

  const primaryNexts = nextStatesFrom(graph, currentState, 'primary');
  const secondaryNexts = nextStatesFrom(graph, currentState, 'secondary');
  const unusualNexts = nextStatesFrom(graph, currentState, 'unusual');

  const out: PromotedPrompts<T> = { primary: [], secondary: [], unusual: [] };
  for (const p of list) {
    if (!p) continue;
    const allowed = p.allowedStates;
    if (intersects(allowed, primaryNexts)) {
      out.primary.push(p);
    } else if (intersects(allowed, secondaryNexts)) {
      out.secondary.push(p);
    } else if (intersects(allowed, unusualNexts)) {
      out.unusual.push(p);
    } else {
      // No overlap with any bucket — still surface under unusual so
      // the user can pick it; the graph is a hint, not enforcement.
      out.unusual.push(p);
    }
  }
  return out;
}
