import { describe, it, expect } from 'vitest';
import {
  cardFromIssue,
  mapApiComment,
  mapApiLatestPlan,
  reshapeIssueView,
  reshapeApiBrief,
} from '../issue';

// BACI-358: the issue-domain reshapers became unit-testable the moment
// they moved out of api.http.ts. These cover the snake→camel rename, the
// optional-field defaults, and the relation "other end" resolution.

describe('cardFromIssue', () => {
  it('renames snake_case joins and derives the column label', () => {
    const card = cardFromIssue({
      key: 'BACI-1',
      title: 'Hi',
      state: 'in_pipeline',
      assignee: 'claude',
      customer_impact: 'big',
      tags: ['a'],
      taken: true,
      feature_emoji: '🔧',
      feature_branch_name: 'feat/x',
    });
    expect(card.key).toBe('BACI-1');
    expect(card.columnLabel).toBe('In Pipeline');
    expect(card.customerImpact).toBe('big');
    expect(card.assignees).toEqual(['claude']);
    expect(card.claude).toBe(true);
    expect(card.taken).toBe(true);
    expect(card.featureEmoji).toBe('🔧');
    expect(card.featureBranchName).toBe('feat/x');
  });

  it('defaults the unset assignee / tags and falls back the column label', () => {
    const card = cardFromIssue({ key: 'BACI-2', title: 'T', state: 'weird' });
    expect(card.columnLabel).toBe('weird');
    expect(card.assignees).toEqual([]);
    expect(card.claude).toBe(false);
    expect(card.tags).toEqual([]);
  });
});

describe('mapApiComment', () => {
  it('zero-fills the optional BACI-131 context fields', () => {
    const c = mapApiComment({ uuid: 'u', author: 'me', body: 'b', created_at: 'now' });
    expect(c).toEqual({
      uuid: 'u',
      author: 'me',
      body: 'b',
      createdAt: 'now',
      eval: false,
      agentSessionId: '',
      dispatchId: undefined,
      mode: '',
      agentName: '',
    });
  });

  it('threads through the eval + agent context when present', () => {
    const c = mapApiComment({
      uuid: 'u', author: 'a', body: 'b', created_at: 'now',
      eval: true, agent_session_id: 's', dispatch_id: 7, mode: 'review', agent_name: 'bot',
    });
    expect(c.eval).toBe(true);
    expect(c.agentSessionId).toBe('s');
    expect(c.dispatchId).toBe(7);
    expect(c.mode).toBe('review');
    expect(c.agentName).toBe('bot');
  });
});

describe('mapApiLatestPlan', () => {
  it('returns null for absent plans', () => {
    expect(mapApiLatestPlan(null)).toBeNull();
    expect(mapApiLatestPlan(undefined)).toBeNull();
  });

  it('renames the document_id / updated_at fields', () => {
    expect(mapApiLatestPlan({ document_id: 3, uuid: 'u', filename: 'p.md', updated_at: 't' }))
      .toEqual({ documentId: 3, uuid: 'u', filename: 'p.md', updatedAt: 't' });
  });
});

describe('reshapeIssueView', () => {
  it('reshapes the nested issue view into IssueDetail', () => {
    const detail = reshapeIssueView({
      issue: { key: 'BACI-9', title: 'T', state: 'todo', description: 'd', tags: ['x'] },
      comments: [{ uuid: 'c', author: 'a', body: 'b', created_at: 'now' }],
      pull_requests: [{ url: 'http://pr' }],
      documents: [{ document_filename: 'f.md', document_type: 'plan' }],
      claimants: [{ session_id: 's', agent_name: 'bot', prompt: 'plan', claimed_at: 't', released_at: null }],
      taken: true,
    });
    expect(detail.columnLabel).toBe('Todo');
    expect(detail.description).toBe('d');
    expect(detail.comments).toHaveLength(1);
    expect(detail.pullRequests).toEqual([{ url: 'http://pr' }]);
    expect(detail.documents[0]).toEqual({ filename: 'f.md', type: 'plan', description: '' });
    expect(detail.claimants[0].open).toBe(true);
    expect(detail.latestPlan).toBeNull();
  });
});

describe('reshapeApiBrief', () => {
  it('resolves the relation other-end key and defaults empty collections', () => {
    const brief = reshapeApiBrief({
      issue: { key: 'BACI-5', title: 'T', state: 'in_review' },
      feature: { slug: 'feat', title: 'Feature' },
      relations: {
        outgoing: [{ from_issue: 'BACI-5', to_issue: 'BACI-6', type: 'blocks' }],
        incoming: [{ from_issue: 'BACI-4', to_issue: 'BACI-5', type: 'blocks' }],
      },
    });
    expect(brief.issue.columnLabel).toBe('In Review');
    expect(brief.feature).toEqual({ slug: 'feat', title: 'Feature' });
    expect(brief.relations.outgoing).toEqual([{ type: 'blocks', otherKey: 'BACI-6' }]);
    expect(brief.relations.incoming).toEqual([{ type: 'blocks', otherKey: 'BACI-4' }]);
    expect(brief.pullRequests).toEqual([]);
    expect(brief.comments).toEqual([]);
    expect(brief.waitingState).toBeNull();
    expect(brief.warnings).toEqual([]);
  });
});
