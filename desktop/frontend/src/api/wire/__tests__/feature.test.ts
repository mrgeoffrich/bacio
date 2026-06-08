import { describe, it, expect } from 'vitest';
import {
  reshapeFeatureSummary,
  reshapeFeatureView,
  reshapePlanView,
} from '../feature';

// BACI-358: feature-domain reshapers, testable now they live outside
// api.http.ts. Cover the snake→camel rename, the `?? 'active'` / `?? true`
// defaults for servers that predate a column, and the plan blocked_by →
// blockedBy rename.

describe('reshapeFeatureSummary', () => {
  it('renames branch_name and defaults the unset emoji / state', () => {
    expect(reshapeFeatureSummary({
      slug: 's', title: 'T', created_at: 'c', updated_at: 'u',
    })).toEqual({
      slug: 's',
      title: 'T',
      emoji: '',
      state: 'active',
      branchName: '',
      updatedAt: 'u',
      hiddenOnBoard: false,
    });
  });

  it('threads through the populated fields', () => {
    const s = reshapeFeatureSummary({
      slug: 's', title: 'T', emoji: '🔧', state: 'done', branch_name: 'feat/x',
      created_at: 'c', updated_at: 'u', hidden_on_board: true,
    });
    expect(s.emoji).toBe('🔧');
    expect(s.state).toBe('done');
    expect(s.branchName).toBe('feat/x');
    expect(s.hiddenOnBoard).toBe(true);
  });
});

describe('reshapeFeatureView', () => {
  it('defaults collectHandoffs ON and labels nested issue states', () => {
    const d = reshapeFeatureView({
      feature: { slug: 's', title: 'T', created_at: 'c', updated_at: 'u' },
      issues: [{ key: 'BACI-1', title: 'I', state: 'in_review' }],
      comments: [{ uuid: 'x', author: 'a', body: 'b', created_at: 'n' }],
      documents: [{ document_filename: 'f.md', document_type: 'plan', description: 'why' }],
    });
    expect(d.collectHandoffs).toBe(true);
    expect(d.state).toBe('active');
    expect(d.issues[0].stateLabel).toBe('In Review');
    expect(d.comments[0].createdAt).toBe('n');
    expect(d.documents[0]).toEqual({ filename: 'f.md', type: 'plan', description: 'why', sourcePath: '' });
  });

  it('honours an explicit collect_handoffs=false', () => {
    const d = reshapeFeatureView({
      feature: { slug: 's', title: 'T', created_at: 'c', updated_at: 'u', collect_handoffs: false, state_manual: true },
      issues: [],
    });
    expect(d.collectHandoffs).toBe(false);
    expect(d.stateManual).toBe(true);
  });
});

describe('reshapePlanView', () => {
  it('renames blocked_by → blockedBy and defaults empties', () => {
    const p = reshapePlanView({
      feature: 'feat',
      order: [{ key: 'BACI-1', title: 'I', state: 'todo', blocked_by: ['BACI-2'], closed: false }],
    });
    expect(p.slug).toBe('feat');
    expect(p.order[0].blockedBy).toEqual(['BACI-2']);
    expect(p.order[0].assignee).toBe('');
    expect(p.order[0].closed).toBe(false);
  });

  it('tolerates an absent order list', () => {
    expect(reshapePlanView({ feature: 'feat' }).order).toEqual([]);
  });
});
