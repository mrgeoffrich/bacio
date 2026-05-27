// Pure helpers for the BACI-231 per-feature integration branch
// (`features.branch_name`). Three concerns:
//
//   - `isValidBranchName` mirrors the Go `ValidateBranchName` at
//     `internal/store/validate.go:228` so the FeaturesView Properties
//     row can reject malformed input client-side, before the PATCH
//     round-trip surfaces the same error from the server. Empty is
//     the "no branch" sentinel and passes — clearing the field is
//     how the user routes a feature back to main.
//   - `displayBranchName` is the "what to show when the value is
//     empty" picker — the detail pane wants a human "main (default)"
//     hint, but the kanban chip / FeatureRow chip should render
//     nothing at all in that case (they condition on truthiness
//     upstream of this helper).
//   - `shortBranchLabel` truncates long refnames (think
//     `feat/users/alice/very-long-experiment`) so they don't blow
//     the kanban card's `.mk-card-top` width budget. The callsite
//     puts the full ref in a `title` attribute so the truncation
//     never loses information.
//
// Mirrors the validation rules in lockstep with the Go validator:
// any change here without a matching change on the Go side will
// leave the two layers disagreeing about what's acceptable.

const MAX_BRANCH_NAME_BYTES = 256;

// Encoder for the byte-length check. UTF-8 multi-byte runes need
// to count their bytes (not their JS string units), to match the Go
// `len(s)` against the same cap.
const utf8Bytes = (s: string): number => new TextEncoder().encode(s).length;

export type BranchNameValidation =
  | { ok: true }
  | { ok: false; reason: string };

// isValidBranchName mirrors internal/store/validate.go:ValidateBranchName.
// Empty is OK (the "no branch" sentinel; clears the column to NULL).
// Non-empty input is held to git's refname rules so a downstream
// `git checkout <branch>` doesn't choke.
export function isValidBranchName(s: string): BranchNameValidation {
  if (s === '') return { ok: true };
  if (utf8Bytes(s) > MAX_BRANCH_NAME_BYTES) {
    return { ok: false, reason: `too long (max ${MAX_BRANCH_NAME_BYTES} bytes)` };
  }
  if (s.startsWith('-')) {
    return { ok: false, reason: "must not start with '-'" };
  }
  if (s.startsWith('/') || s.endsWith('/')) {
    return { ok: false, reason: "must not start or end with '/'" };
  }
  if (s.startsWith('.') || s.endsWith('.')) {
    return { ok: false, reason: "must not start or end with '.'" };
  }
  if (s.includes('..')) {
    return { ok: false, reason: "must not contain '..'" };
  }
  if (s.includes('@{')) {
    return { ok: false, reason: "must not contain '@{'" };
  }
  // Whitespace, control chars, and disallowed punctuation in one pass.
  for (const ch of s) {
    const code = ch.codePointAt(0)!;
    // C0 controls (U+0000..U+001F) and DEL (U+007F).
    if ((code >= 0x00 && code <= 0x1f) || code === 0x7f) {
      return { ok: false, reason: 'must not contain control characters' };
    }
    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      return { ok: false, reason: 'must not contain whitespace' };
    }
    if (ch === '~' || ch === '^' || ch === ':' || ch === '?' || ch === '*' || ch === '[' || ch === '\\') {
      return { ok: false, reason: `must not contain '${ch}'` };
    }
  }
  return { ok: true };
}

// displayBranchName is the human-facing string for surfaces that
// want to render a placeholder when no branch is set (the detail
// pane's "Integration branch" Properties row in particular). Surfaces
// that render nothing when empty (the FeatureRow chip, the kanban
// card chip) condition on truthiness before reaching this helper —
// don't route them through here.
export function displayBranchName(s: string): string {
  return s === '' ? 'main (default)' : s;
}

// shortBranchLabel truncates a long refname for the chip surfaces
// where `.mk-card-top` runs out of room. Refs ≤ 14 chars pass through
// unchanged; longer refs collapse to a leading `…` plus the
// 13-character tail (which is usually the discriminating suffix —
// `feat/users/alice/wip` → `…ce/wip` etc.). Always render the full
// ref in a `title` attribute / tooltip on the chip so the truncation
// is recoverable.
export function shortBranchLabel(s: string): string {
  if (s.length <= 14) return s;
  return '…' + s.slice(-13);
}
