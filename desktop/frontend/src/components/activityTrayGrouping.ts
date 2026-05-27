// Pure grouping helper for the BACI-231 ActivityTray auto-grouping
// pass. Extracted from ActivityTray.jsx so it's loadable from a
// plain-Node smoketest (the .jsx parent pulls React + Icon and won't
// import cleanly). The component imports this back in for the render
// path.
//
// Behaviour:
//
//   - Pinned rows (kind: 'pinned') stay at the top in their own
//     headerless block — they're sticky, not in-flight transitions.
//   - Entry rows (kind: 'entry') group by their card's
//     `featureBranchName` denorm (empty string = on `main` / no
//     branch). If every entry resolves to the same branch (or no
//     branch), we return the legacy flat shape — render byte-
//     identically to today, no headers.
//   - Otherwise we return one group per branch, feature branches
//     first (alphabetical), then the empty-key / `main` bucket last,
//     so feature-branch activity floats to the top of the tray.
//   - Within each group, the original LIFO ordering of `rows` is
//     preserved by inserting into the per-branch bucket in iteration
//     order.
//
// Pure function — no React state — testable in isolation.

export type TrayPinnedRow = { kind: 'pinned'; key: string; card: { key: string } & Record<string, unknown> };
export type TrayEntryRow = { kind: 'entry'; key: string; entry: Record<string, unknown> };
export type TrayRow = TrayPinnedRow | TrayEntryRow;

export type CardLike = { featureBranchName?: string } & Record<string, unknown>;

// `branch === null` flags the pinned-rows-only leading group so the
// renderer skips drawing a header above it (the pinned block has no
// branch semantics).
export type TrayGroup = { branch: string | null; rows: TrayRow[] };

export type TrayGrouping =
  | { multiBranch: false; groups: [TrayGroup] }
  | { multiBranch: true; groups: TrayGroup[] };

export function groupRowsByBranch(
  rows: TrayRow[],
  cardsByKey: Map<string, CardLike> | { get: (k: string) => CardLike | undefined } | null | undefined,
): TrayGrouping {
  // Split off the pinned rows; they don't participate in grouping.
  // They render first as their own block.
  const pinned: TrayRow[] = [];
  const entryRows: TrayRow[] = [];
  for (const r of rows) {
    if (r.kind === 'pinned') pinned.push(r);
    else entryRows.push(r);
  }

  // Build the branch-keyed map for the entry rows in insertion order
  // so each group preserves the existing LIFO ordering of its members.
  const byBranch = new Map<string, TrayRow[]>();
  for (const r of entryRows) {
    const card = cardsByKey && typeof cardsByKey.get === 'function' ? cardsByKey.get(r.key) : null;
    const branch = (card && card.featureBranchName) || '';
    let bucket = byBranch.get(branch);
    if (!bucket) {
      bucket = [];
      byBranch.set(branch, bucket);
    }
    bucket.push(r);
  }

  // Single branch (or no entries) — flat render, no headers. Pinned
  // rows stay where they were.
  if (byBranch.size <= 1) {
    return { multiBranch: false, groups: [{ branch: '', rows: [...pinned, ...entryRows] }] };
  }

  // Multi-branch — sort feature-branch buckets alphabetically, then
  // the empty-key "main" bucket last.
  const branches = [...byBranch.keys()].filter((b) => b !== '').sort();
  const ordered: TrayGroup[] = branches.map((b) => ({ branch: b, rows: byBranch.get(b)! }));
  if (byBranch.has('')) {
    ordered.push({ branch: '', rows: byBranch.get('')! });
  }

  // Pinned rows prepend as their own headerless block (branch=null)
  // so they stay sticky at the top of the tray.
  const groups: TrayGroup[] = [];
  if (pinned.length > 0) {
    groups.push({ branch: null, rows: pinned });
  }
  groups.push(...ordered);
  return { multiBranch: true, groups };
}
