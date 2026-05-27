// featureGraphLayout (BACI-236) is the pure layered-layout helper the
// FeatureDependencyGraph view consumes. The /features/{slug}/plan
// endpoint already hands us issues in topological order; we walk that
// order to assign a rank (max rank of its blockers + 1) so the graph
// renders as columns of independent work.
//
// Kept dependency-free (no React, no @xyflow/react) so it can ship a
// plain Node smoke test in __tests__/featureDependencyLayout.smoketest.mjs
// — the layout maths is the bit worth pinning without jsdom.
//
// Coordinates are in raw pixels — Reactflow's fitView rescales to fit
// the canvas at render time. COL_WIDTH and ROW_HEIGHT are conservative
// defaults that keep the typical 3-30 node feature legible without
// pan-zoom; the consumer can override either when building its
// <ReactFlow> instance.

export const COL_WIDTH = 240;
export const ROW_HEIGHT = 96;

export interface PlanEntryInput {
  key: string;
  title: string;
  state: string;
  blockedBy?: string[];
  closed?: boolean;
}

// LayoutNode is shaped to drop straight into @xyflow/react's <ReactFlow>
// `nodes` prop. We bake the per-node payload into `data` so the
// custom node component can read everything it needs (title, key,
// state, muted flag, ready flag) without a second lookup.
export interface LayoutNode {
  id: string;
  type: 'blocker';
  position: { x: number; y: number };
  data: {
    issueKey: string;
    title: string;
    state: string;
    closed: boolean;
    ready: boolean;
    rank: number;
  };
}

// LayoutEdge mirrors @xyflow/react's edge shape. Source → target
// represents "source blocks target" — the arrow points from the
// blocker to the blocked.
export interface LayoutEdge {
  id: string;
  source: string;
  target: string;
}

export interface LayoutResult {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
}

// computeLayout walks the topo-ordered plan entries and assigns each
// a rank (column). Ranks start at 0 for entries with no in-feature
// blockers; everything else gets max(rank(blocker)) + 1. Nodes within
// the same rank stack vertically in plan-order, which is stable across
// reloads.
//
// `closed` entries contribute their rank as normal — the graph view
// wants delivered work to sit on the left so the live-work columns
// flow rightward as expected. The renderer can mute closed nodes
// separately via the per-node `closed` flag.
//
// Cross-feature blockers (keys in `blockedBy` that don't appear in the
// plan) are tolerated silently — the plan endpoint never emits them
// after the BACI-236 widening (the in-feature gate stays in place),
// but a defensive check costs nothing and keeps the helper safe to
// re-use from a fixture-driven test.
export function computeLayout(entries: PlanEntryInput[]): LayoutResult {
  if (!entries || entries.length === 0) {
    return { nodes: [], edges: [] };
  }

  const inFeature = new Set<string>();
  for (const e of entries) inFeature.add(e.key);

  // Walk topo-order to assign ranks. Because the plan endpoint sorts
  // blockers before blocked, each entry's in-feature blockers already
  // have a rank when we get to them.
  const rank = new Map<string, number>();
  for (const e of entries) {
    let r = 0;
    for (const b of e.blockedBy ?? []) {
      if (!inFeature.has(b)) continue;
      const br = rank.get(b);
      if (br !== undefined && br + 1 > r) r = br + 1;
    }
    rank.set(e.key, r);
  }

  // Bucket by rank for the y-coordinate within a column.
  const byRank = new Map<number, string[]>();
  for (const e of entries) {
    const r = rank.get(e.key) ?? 0;
    let bucket = byRank.get(r);
    if (!bucket) {
      bucket = [];
      byRank.set(r, bucket);
    }
    bucket.push(e.key);
  }

  const nodes: LayoutNode[] = [];
  for (const e of entries) {
    const r = rank.get(e.key) ?? 0;
    const bucket = byRank.get(r) ?? [e.key];
    const indexInRank = bucket.indexOf(e.key);
    const closed = !!e.closed;
    // A node is "ready" only when it's live AND has no in-feature
    // blockers (open or closed — a delivered blocker no longer
    // gates the work, but the BACI-236 graph still draws the edge
    // for visual context, so we ignore closed blockers here).
    let ready = !closed;
    if (ready) {
      for (const b of e.blockedBy ?? []) {
        if (!inFeature.has(b)) continue;
        const blocker = entries.find(x => x.key === b);
        if (blocker && !blocker.closed) {
          ready = false;
          break;
        }
      }
    }
    nodes.push({
      id: e.key,
      type: 'blocker',
      position: { x: r * COL_WIDTH, y: indexInRank * ROW_HEIGHT },
      data: {
        issueKey: e.key,
        title: e.title,
        state: e.state,
        closed,
        ready,
        rank: r,
      },
    });
  }

  const edges: LayoutEdge[] = [];
  for (const e of entries) {
    for (const b of e.blockedBy ?? []) {
      if (!inFeature.has(b)) continue;
      edges.push({
        id: `${b}__${e.key}`,
        source: b,
        target: e.key,
      });
    }
  }

  return { nodes, edges };
}
