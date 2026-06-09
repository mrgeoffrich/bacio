// Issue-domain wire types + reshapers (BACI-358).
//
// The snake_case `Api*` shapes mirror the JSON the `bacio api` server
// serialises (model.Issue / internal/api/views.go) and the pure
// functions that reshape them into the camelCase desktop DTOs the React
// tree consumes. Moved out of api.http.ts so the reshapes are unit
// testable; api.http.ts imports the types (for its `call<T>` generics)
// and the reshapers (for the fetch wrappers). Phase 2b (BACI-359) folded
// the camelCase DTO shapes into the shared, runtime-free ../contract
// module — imported as types only, so both transports compile against
// one definition.

import type {
  BoardCard,
  CommentDTO,
  LatestPlan,
  IssueDetail,
  IssueBriefDTO,
  IssueMetaDTO,
  FeatureRefDTO,
  RelationDTO,
  WaitingState,
} from '../contract';
import { stateLabel, assigneeList } from './common';

export interface ApiIssue {
  key: string;
  title: string;
  description?: string;
  state: string;
  assignee?: string;
  // BACI-349: optional one-line customer impact. Snake-case on the wire
  // (model.Issue's json tag); cardFromIssue / the meta reshape copy it
  // onto the camelCase customerImpact field.
  customer_impact?: string;
  tags?: string[];
  taken?: boolean;
  // BACI-172: per-feature glyph denormalised from the join in
  // store.issueSelect. Snake-case on the wire; the cardFromIssue
  // reshape below copies it onto the camelCase BoardCard.featureEmoji.
  feature_emoji?: string;
  // BACI-231: per-feature integration branch from the same join.
  // Snake-case on the wire; cardFromIssue reshapes onto
  // BoardCard.featureBranchName so per-issue endpoints (e.g.
  // setIssueState's drag-refresh) keep the branch chip until the
  // next listCards() rebuild.
  feature_branch_name?: string;
}

export interface ApiCommentEnvelope {
  uuid: string;
  author: string;
  body: string;
  created_at: string;
  // BACI-131 — wire-shape mirror of model.Comment. eval is always
  // present on the wire (the server omits no field), agent_name is
  // populated only by the JOIN-aware list endpoint. The four context
  // fields stay omitted on normal comments via the model's omitempty.
  eval?: boolean;
  agent_session_id?: string;
  dispatch_id?: number;
  mode?: string;
  agent_name?: string;
}

export interface ApiPR { url: string; }

export interface ApiDocLink {
  document_filename: string;
  document_type: string;
  description?: string;
}

export interface ApiClaimant {
  session_id: string;
  agent_name: string;
  prompt: string;
  claimed_at: string;
  released_at: string | null;
}

export interface ApiIssueView {
  issue: ApiIssue;
  comments: ApiCommentEnvelope[];
  pull_requests: ApiPR[];
  documents: ApiDocLink[];
  claimants: ApiClaimant[];
  taken: boolean;
  // BACI-216: snake-cased wire shape — see ApiLatestPlan below.
  latest_plan?: ApiLatestPlan | null;
}

export interface ApiBriefDoc {
  filename: string;
  type: string;
  description?: string;
  source_path?: string;
  linked_via?: string[];
  size_bytes?: number;
  content?: string;
}

// ApiLatestPlan is the snake_case wire shape of model.LatestPlan — the
// per-issue plan projection BACI-216 attaches to the issue show / brief
// payloads. Reshaped into the camelCase LatestPlan the UI consumes by
// mapApiLatestPlan below. The cards endpoint already serves camelCase
// via the Go json tag so the listCards() path doesn't go through this
// reshape — only the snake-cased show / brief handlers do.
export interface ApiLatestPlan {
  document_id: number;
  uuid: string;
  filename: string;
  updated_at: string;
}

export interface ApiRelation {
  // ListIssueRelations emits model.Relation, which serialises as
  // {from_issue, to_issue, type, id, created_at}. The brief reshape
  // resolves these into RelationDTO entries with the "other end" key.
  from_issue: string;
  to_issue: string;
  type: string;
}

export interface ApiIssueRelations {
  outgoing?: ApiRelation[] | null;
  incoming?: ApiRelation[] | null;
}

export interface ApiFeatureRef {
  slug: string;
  title: string;
}

export interface ApiIssueBrief {
  issue: ApiIssue;
  feature?: ApiFeatureRef | null;
  relations?: ApiIssueRelations | null;
  pull_requests?: ApiPR[] | null;
  documents?: ApiBriefDoc[] | null;
  comments?: ApiCommentEnvelope[] | null;
  claimants?: ApiClaimant[] | null;
  taken?: boolean;
  // BACI-145: snake-case wire shape mirroring internal/api/views.go
  // IssueBrief.WaitingState. WaitingState's own field names stay
  // camelCase server-side (the boardcards JSON tags are camel) so no
  // per-field rename here — only the outer wrapper is snake.
  waiting_state?: WaitingState | null;
  // BACI-216: snake-cased wire shape — see ApiLatestPlan above.
  latest_plan?: ApiLatestPlan | null;
  warnings?: string[] | null;
}

export interface ApiBoardCard {
  // setIssueState returns the model.Issue; same reshape as listCards.
  // Post-BACI-255 the wire model.Issue no longer carries a denormalised
  // waiting_for_claim flag — drag-state for waiting cards is gated by
  // the kanban's own waitingState lookup, not a per-row boolean.
  key: string;
  state: string;
  title: string;
  assignee?: string;
  tags?: string[];
  taken?: boolean;
}

export function cardFromIssue(iss: ApiIssue): BoardCard {
  const assignee = iss.assignee ?? '';
  return {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    // BACI-349: thread the customer impact through so a card refreshed
    // via setIssueState() (drag-to-move) keeps it until the next
    // listCards() rebuild.
    customerImpact: iss.customer_impact,
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    taken: !!iss.taken,
    // BACI-172: thread the joined feature emoji through so a card
    // refreshed via setIssueState() (drag-to-move) keeps its glyph
    // until the next listCards() rebuilds the array.
    featureEmoji: iss.feature_emoji,
    // BACI-231: thread the joined feature branch through so the
    // drag-refresh keeps the kanban branch chip until the next
    // listCards() rebuild.
    featureBranchName: iss.feature_branch_name,
    // BACI-145: setIssueState (a drag-to-move) is blocked for waiting
    // cards by the UI, so the wire shape produced here can't be
    // observably waiting. Leave waitingState undefined; the next 10s
    // poll re-runs listCards and re-populates it from the server.
  };
}

// mapApiComment normalises the wire-shape envelope (snake_case,
// always-defined-but-maybe-zero context fields) to the CommentDTO
// the React tree consumes (camelCase, optional-when-zero context).
// Shared by reshapeIssueView() and reshapeApiBrief() so the BACI-131
// fields stay in lock-step across both read paths.
export function mapApiComment(c: ApiCommentEnvelope): CommentDTO {
  return {
    uuid: c.uuid,
    author: c.author,
    body: c.body,
    createdAt: c.created_at,
    eval: !!c.eval,
    agentSessionId: c.agent_session_id ?? '',
    dispatchId: c.dispatch_id,
    mode: c.mode ?? '',
    agentName: c.agent_name ?? '',
  };
}

export function mapApiLatestPlan(p: ApiLatestPlan | null | undefined): LatestPlan | null {
  if (!p) return null;
  return {
    documentId: p.document_id,
    uuid: p.uuid,
    filename: p.filename,
    updatedAt: p.updated_at,
  };
}

// reshapeIssueView maps the GET /repos/{prefix}/issues/{key} payload into
// the IssueDetail the issue pane consumes.
export function reshapeIssueView(view: ApiIssueView): IssueDetail {
  const iss = view.issue;
  const assignee = iss.assignee ?? '';
  return {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    customerImpact: iss.customer_impact,
    description: iss.description ?? '',
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    comments: (view.comments ?? []).map(mapApiComment),
    pullRequests: (view.pull_requests ?? []).map(p => ({ url: p.url })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.document_filename,
      type: d.document_type,
      description: d.description ?? '',
    })),
    claimants: (view.claimants ?? []).map(c => ({
      sessionId: c.session_id,
      agentName: c.agent_name,
      prompt: c.prompt,
      claimedAt: c.claimed_at,
      releasedAt: c.released_at,
      open: c.released_at == null,
    })),
    taken: !!view.taken,
    latestPlan: mapApiLatestPlan(view.latest_plan),
  };
}

export function reshapeApiBrief(view: ApiIssueBrief): IssueBriefDTO {
  const iss = view.issue;
  const assignee = iss.assignee ?? '';
  // BACI-216: resolve the latest plan once; both the meta and the
  // envelope-level field carry the same shape.
  const latestPlan = mapApiLatestPlan(view.latest_plan);
  const meta: IssueMetaDTO = {
    key: iss.key,
    column: iss.state,
    columnLabel: stateLabel(iss.state),
    title: iss.title,
    customerImpact: iss.customer_impact,
    description: iss.description ?? '',
    tags: iss.tags ?? [],
    assignees: assigneeList(assignee),
    claude: assignee === 'claude',
    taken: !!view.taken,
    latestPlan,
  };
  const feat: FeatureRefDTO | null = view.feature
    ? { slug: view.feature.slug, title: view.feature.title }
    : null;
  const outgoing: RelationDTO[] = (view.relations?.outgoing ?? []).map(r => ({
    type: r.type,
    otherKey: r.to_issue,
  }));
  const incoming: RelationDTO[] = (view.relations?.incoming ?? []).map(r => ({
    type: r.type,
    otherKey: r.from_issue,
  }));
  return {
    issue: meta,
    feature: feat,
    relations: { outgoing, incoming },
    pullRequests: (view.pull_requests ?? []).map(p => ({ url: p.url })),
    documents: (view.documents ?? []).map(d => ({
      filename: d.filename,
      type: d.type,
      description: d.description ?? '',
      sourcePath: d.source_path ?? '',
      linkedVia: d.linked_via ?? [],
      sizeBytes: d.size_bytes ?? 0,
      content: d.content ?? '',
    })),
    comments: (view.comments ?? []).map(mapApiComment),
    claimants: (view.claimants ?? []).map(c => ({
      sessionId: c.session_id,
      agentName: c.agent_name,
      prompt: c.prompt,
      claimedAt: c.claimed_at,
      releasedAt: c.released_at,
      open: c.released_at == null,
    })),
    taken: !!view.taken,
    waitingState: view.waiting_state ?? null,
    latestPlan,
    warnings: view.warnings ?? [],
  };
}
