// Feature-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror model.Feature / api.PlanEntry as the
// `bacio api` server serialises them, plus the pure functions that reshape
// them into the camelCase FeatureSummary / FeatureDetail / FeaturePlan the
// React tree consumes. Moved out of api.http.ts so the reshapes are unit
// testable. See ./issue.ts for the same pattern + the Phase 2b note.

import type {
  FeatureSummary,
  FeatureDetail,
  FeaturePlan,
} from '../../api.http';
import { stateLabel } from './common';

export interface ApiFeature {
  slug: string;
  title: string;
  description?: string;
  // BACI-172: per-feature emoji glyph from the wire. Optional /
  // absent on pre-BACI-172 features (or features with no glyph
  // set); model.Feature serialises with omitempty.
  emoji?: string;
  // BACI-225: per-feature integration branch from the wire. Optional
  // / absent when the feature ships straight to main (the legacy
  // default); model.Feature serialises BranchName with omitempty
  // when the column is NULL.
  branch_name?: string;
  // BACI-199: three-state column + sticky bit. Always present
  // in JSON (no omitempty on model.Feature). Pre-BACI-199 servers
  // (no column wired yet) leave these absent — `state ?? 'active'`
  // is the safe default.
  state?: string;
  state_manual?: boolean;
  // BACI-333: per-feature collect-handoffs toggle. Always present in
  // JSON (no omitempty) so the React component reads it directly. A
  // server that hasn't shipped BACI-333 leaves it absent — `?? true`
  // keeps the opt-out default ON.
  collect_handoffs?: boolean;
  created_at: string;
  updated_at: string;
  // BACI-177: per-feature "Show on board" toggle state. Always
  // present in JSON (no omitempty) so the React component reads it
  // without an `?? false`. Defaults to false on a server that hasn't
  // shipped BACI-177 yet — the field will simply be absent.
  hidden_on_board?: boolean;
}

export interface ApiFeatureComment {
  uuid: string;
  author: string;
  body: string;
  created_at: string;
}

// ApiFeatureLink mirrors model.DocumentLink (BACI-214) — the snake_case
// wire shape served by `GET /repos/{prefix}/features/{slug}` for each
// linked document. Description is the link-time `--why`. Other
// model.DocumentLink fields (ids, issue/feature foreign keys, created_at)
// aren't surfaced on the feature pane.
export interface ApiFeatureLink {
  document_filename: string;
  document_type: string;
  description?: string;
}

export interface ApiFeatureView {
  feature: ApiFeature;
  issues: Array<{ key: string; title: string; state: string }>;
  comments?: ApiFeatureComment[];
  documents?: ApiFeatureLink[];
}

// ApiPlanEntry is the snake_case wire shape served by GET
// /repos/{prefix}/features/{slug}/plan — mirrors api.PlanEntry. We
// reshape blocked_by → blockedBy on the client so the FeaturePlan
// camelCase shape matches the Wails binding (BACI-236).
export interface ApiPlanEntry {
  key: string;
  title: string;
  state: string;
  assignee?: string;
  blocked_by?: string[];
  closed?: boolean;
}

export interface ApiPlanView {
  feature: string;
  order?: ApiPlanEntry[];
}

// reshapeFeatureSummary maps one row of GET /repos/{prefix}/features into
// the FeatureSummary the Features list consumes.
export function reshapeFeatureSummary(f: ApiFeature): FeatureSummary {
  return {
    slug: f.slug,
    title: f.title,
    emoji: f.emoji ?? '',
    state: f.state ?? 'active',
    branchName: f.branch_name ?? '',
    updatedAt: f.updated_at,
    hiddenOnBoard: !!f.hidden_on_board,
  };
}

// reshapeFeatureView maps GET /repos/{prefix}/features/{slug} into the
// FeatureDetail the feature pane consumes.
export function reshapeFeatureView(view: ApiFeatureView): FeatureDetail {
  const f = view.feature;
  return {
    slug: f.slug,
    title: f.title,
    description: f.description ?? '',
    emoji: f.emoji ?? '',
    branchName: f.branch_name ?? '',
    state: f.state ?? 'active',
    stateManual: !!f.state_manual,
    collectHandoffs: f.collect_handoffs ?? true,
    createdAt: f.created_at,
    updatedAt: f.updated_at,
    issues: (view.issues ?? []).map(iss => ({
      key: iss.key,
      title: iss.title,
      state: iss.state,
      stateLabel: stateLabel(iss.state),
    })),
    comments: (view.comments ?? []).map(c => ({
      uuid: c.uuid,
      author: c.author,
      body: c.body,
      createdAt: c.created_at,
    })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.document_filename,
      type: d.document_type,
      description: d.description ?? '',
      sourcePath: '',
    })),
    hiddenOnBoard: !!f.hidden_on_board,
  };
}

// reshapePlanView maps GET /repos/{prefix}/features/{slug}/plan into the
// FeaturePlan the dependency graph consumes (blocked_by → blockedBy).
export function reshapePlanView(view: ApiPlanView): FeaturePlan {
  return {
    slug: view.feature,
    order: (view.order ?? []).map(e => ({
      key: e.key,
      title: e.title,
      state: e.state,
      assignee: e.assignee ?? '',
      blockedBy: e.blocked_by ?? [],
      closed: !!e.closed,
    })),
  };
}
