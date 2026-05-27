// featureGraphLayout (BACI-236, dagre rewrite under BACI-243) is the
// pure layered-layout helper the FeatureDependencyGraph view consumes.
// The /features/{slug}/plan endpoint already hands us issues in
// topological order; we walk that order to assign a per-node `rank`
// (max rank of its blockers + 1) so the smoke test has a stable
// column index regardless of how dagre shuffles the layout for edge
// crossings. Absolute positions come from @dagrejs/dagre.
//
// Kept React-free (no @xyflow/react import) so it can ship a plain
// Node smoke test in __tests__/featureDependencyLayout.smoketest.mjs
// — the layout maths + rank/ready bits are the bit worth pinning
// without jsdom.
//
// Coordinates are in raw pixels — Reactflow's fitView rescales to fit
// the canvas at render time. NODE_WIDTH and NODE_HEIGHT mirror the
// `.mk-graph-node` CSS so dagre's edge routing avoids overlap.

import dagre from '@dagrejs/dagre';

export const NODE_WIDTH = 220;
export const NODE_HEIGHT = 100;

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

// computeLayout walks the topo-ordered plan entries to assign each
// node a stable `rank` (column index — kept for the smoke test) and
// then hands the graph to dagre for absolute positions. Ranks start
// at 0 for entries with no in-feature blockers; everything else gets
// max(rank(blocker)) + 1. Cross-feature blockers (keys in `blockedBy`
// that don't appear in the plan) are tolerated silently — the plan
// endpoint never emits them after the BACI-236 widening, but a
// defensive check costs nothing.
//
// `closed` entries flow through the layout normally — the renderer
// mutes them via the per-node `closed` flag. The `ready` bit reflects
// "this is live AND none of its in-feature blockers are still open" —
// a delivered blocker no longer gates the work but the edge is still
// drawn for visual context.
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

  // Build the dagre graph. rankdir LR places ranks left-to-right; the
  // sep / margin values keep the typical 3-30 node feature legible
  // without pan-zoom.
  const g = new dagre.graphlib.Graph();
  g.setGraph({
    rankdir: 'LR',
    ranksep: 64,
    nodesep: 24,
    marginx: 16,
    marginy: 16,
  });
  g.setDefaultEdgeLabel(() => ({}));

  for (const e of entries) {
    g.setNode(e.key, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const e of entries) {
    for (const b of e.blockedBy ?? []) {
      if (!inFeature.has(b)) continue;
      g.setEdge(b, e.key);
    }
  }

  dagre.layout(g);

  // Dagre returns center coords; ReactFlow expects top-left.
  const nodes: LayoutNode[] = [];
  for (const e of entries) {
    const r = rank.get(e.key) ?? 0;
    const closed = !!e.closed;
    // A node is "ready" only when it's live AND has no in-feature
    // open blockers. A delivered blocker no longer gates the work,
    // but the BACI-236 graph still draws the edge for visual context,
    // so we ignore closed blockers here.
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
    const dn = g.node(e.key);
    nodes.push({
      id: e.key,
      type: 'blocker',
      position: {
        x: (dn?.x ?? 0) - NODE_WIDTH / 2,
        y: (dn?.y ?? 0) - NODE_HEIGHT / 2,
      },
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
