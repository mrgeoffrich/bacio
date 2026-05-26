// BACI-222: pure filter helper for the kanban dispatch menus (follow-on
// + zap). Lives in its own module so the matching logic can be smoke-
// tested with the plain-Node `node:assert` runner the rest of the
// frontend uses — no React, no DOM, no Vitest dependency.
//
// The two callers (DispatchMenuContent today, anything else later) ask
// the same thing: "given a typed query, which prompt-config rows are
// visible?". `actionLabel`, `label`, and `mode` are all matched (case-
// insensitive substring) so the user can type either the imperative
// verb ("plan"), the gerund ("planning"), or the raw mode slug
// ("fix_review"). An empty query returns the input list unchanged so
// the menu renders identically to its pre-filter shape on first open.

export interface PromptForFilter {
  mode: string;
  label?: string;
  actionLabel?: string;
}

export function filterPrompts<T extends PromptForFilter>(
  prompts: readonly T[],
  query: string,
): T[] {
  const list = Array.isArray(prompts) ? prompts : [];
  const q = (query || '').trim().toLowerCase();
  if (!q) return list.slice();
  return list.filter(p => {
    if (!p) return false;
    const haystack = [p.actionLabel, p.label, p.mode]
      .filter(Boolean)
      .map(s => String(s).toLowerCase());
    return haystack.some(s => s.includes(q));
  });
}
