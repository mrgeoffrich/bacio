import { describe, it, expect } from 'vitest';
import { reshapeDispatch } from '../dispatch';

// BACI-358: the dispatch reshaper, testable now it lives outside
// api.http.ts. Covers the snake→camel rename and the empty-string
// defaults for the optional wire fields.

describe('reshapeDispatch', () => {
  it('renames target_agent_name / issue_key and zero-fills optionals', () => {
    expect(reshapeDispatch({ id: 4, status: 'queued', created_at: 'now' })).toEqual({
      id: 4,
      issueKey: '',
      targetAgent: '',
      mode: '',
      status: 'queued',
      payload: '',
      createdBy: '',
      createdAt: 'now',
    });
  });

  it('threads through the populated fields', () => {
    const d = reshapeDispatch({
      id: 9, issue_key: 'BACI-1', target_agent_name: 'bot', mode: 'implement',
      status: 'delivered', payload: 'p', created_by: 'me', created_at: 'now',
    });
    expect(d.issueKey).toBe('BACI-1');
    expect(d.targetAgent).toBe('bot');
    expect(d.mode).toBe('implement');
    expect(d.payload).toBe('p');
    expect(d.createdBy).toBe('me');
  });
});
