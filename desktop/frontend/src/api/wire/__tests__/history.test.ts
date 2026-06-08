import { describe, it, expect } from 'vitest';
import { reshapeHistoryEntry } from '../history';

// BACI-358: the history reshaper, testable now it lives outside
// api.http.ts. Covers the target_label / created_at rename and the
// empty-string defaults for the optional fields.

describe('reshapeHistoryEntry', () => {
  it('renames target_label / created_at and zero-fills optionals', () => {
    expect(reshapeHistoryEntry({ id: 1, actor: 'me', op: 'issue.add', created_at: 'now' })).toEqual({
      id: 1,
      actor: 'me',
      op: 'issue.add',
      kind: '',
      targetLabel: '',
      details: '',
      createdAt: 'now',
    });
  });

  it('threads through the populated fields', () => {
    const e = reshapeHistoryEntry({
      id: 2, actor: 'bot', op: 'agent.bind', kind: 'issue', target_label: 'BACI-1', details: 'd', created_at: 'now',
    });
    expect(e.kind).toBe('issue');
    expect(e.targetLabel).toBe('BACI-1');
    expect(e.details).toBe('d');
  });
});
