import React, { useEffect, useMemo, useState, useCallback } from 'react';
import { useNavigate } from 'react-router';
import {
  ReactFlow,
  Background,
  Controls,
  Handle,
  Position,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

import * as api from '../api';
import { reportError } from '../errors';
import { issuePath } from '../lib/routes';
import { computeLayout } from './featureGraphLayout';

// FeatureDependencyGraph (BACI-236, dagre layout under BACI-243)
// renders the feature's open + closed issues as a left-to-right
// layered graph, with directed edges along `blocks` relations.
// Positions come from @dagrejs/dagre via `computeLayout`. Lives in
// its own module so the heavy @xyflow/react import only lands when
// the user opens the Graph tab — FeaturesView lazy-loads this file.
//
// The component fetches `getFeaturePlan(repo, slug, true)` on mount to
// pull every issue (open + closed) plus every in-feature blocker
// edge. Closed nodes render muted; open nodes that have no remaining
// open blockers show a "Ready" badge. Clicking a node navigates to the
// issue workspace.
export default function FeatureDependencyGraph({ repoPrefix, slug }) {
  const navigate = useNavigate();
  const [plan, setPlan] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .getFeaturePlan(repoPrefix, slug, true)
      .then((p) => {
        if (cancelled) return;
        setPlan(p);
        setLoading(false);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err?.message || String(err));
        setLoading(false);
        reportError(err, { headline: "Couldn't load dependency graph" });
      });
    return () => {
      cancelled = true;
    };
  }, [repoPrefix, slug]);

  const layout = useMemo(() => {
    if (!plan) return { nodes: [], edges: [] };
    return computeLayout(plan.order ?? []);
  }, [plan]);

  const onNodeClick = useCallback(
    (_evt, node) => {
      const key = node?.data?.issueKey;
      if (!key) return;
      navigate(issuePath(repoPrefix, key));
    },
    [navigate, repoPrefix],
  );

  // BACI-236: keep the custom node type stable across renders so
  // ReactFlow doesn't warn about re-registering. The map references
  // the BlockerNode component declared below this function — wrapped
  // in useMemo so it's the same object reference on every render.
  const nodeTypes = useMemo(() => ({ blocker: BlockerNode }), []);

  if (loading) {
    return <div className="mk-features-empty">Loading graph…</div>;
  }
  if (error) {
    return (
      <div className="mk-features-empty">
        Couldn't load graph: {error}
      </div>
    );
  }
  if (!layout.nodes.length) {
    return (
      <div className="mk-features-empty">
        No issues in this feature yet.
      </div>
    );
  }

  return (
    <div className="mk-features-graph">
      <ReactFlow
        nodes={layout.nodes}
        edges={layout.edges}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={true}
      >
        <Background gap={24} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}

// BlockerNode renders a single issue card in the graph. Three visual
// states (driven by data.closed / data.ready / default-blocked) match
// the CSS rules in app.css so the renderer stays the only source of
// truth for the look-and-feel.
function BlockerNode({ data }) {
  const variant = data.closed ? 'closed' : data.ready ? 'ready' : 'blocked';
  return (
    <div className={`mk-graph-node mk-graph-node--${variant}`}>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <div className="mk-graph-node-head">
        <span className="mk-graph-node-key">{data.issueKey}</span>
        {data.ready && (
          <span className="mk-graph-node-badge" title="No remaining open blockers">
            Ready
          </span>
        )}
      </div>
      <div className="mk-graph-node-title" title={data.title}>
        {data.title}
      </div>
      <div className={`mk-graph-node-state mk-status-${data.state || 'unknown'}`}>
        {data.state || 'unknown'}
      </div>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}
