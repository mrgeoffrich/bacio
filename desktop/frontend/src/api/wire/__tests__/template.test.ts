import { describe, it, expect } from 'vitest';
import { reshapeTemplate } from '../template';

// BACI-358: the prompt-template reshaper, testable now it lives outside
// api.http.ts. Covers the is_builtin / concurrency / action_label renames
// and the `?? true` defaults for the *_is_default flags.

describe('reshapeTemplate', () => {
  it('renames the snake_case fields and defaults the is_default flags ON', () => {
    expect(reshapeTemplate({
      slug: 'implement', mode: 'implement', label: 'Implement', body: 'b',
      default: 'd', is_builtin: true, is_default: false,
    })).toEqual({
      slug: 'implement',
      mode: 'implement',
      label: 'Implement',
      body: 'b',
      default: 'd',
      isBuiltin: true,
      isDefault: false,
      concurrencyLimit: 0,
      defaultConcurrencyLimit: 0,
      concurrencyIsDefault: true,
      actionLabel: '',
      defaultActionLabel: '',
      actionLabelIsDefault: true,
    });
  });

  it('threads through the concurrency + action-label overrides', () => {
    const t = reshapeTemplate({
      slug: 's', mode: 'm', label: 'L', body: 'b', default: 'd',
      is_builtin: false, is_default: true,
      concurrency_limit: 3, default_concurrency_limit: 1, concurrency_is_default: false,
      action_label: 'Ship it', default_action_label: 'Ship', action_label_is_default: false,
    });
    expect(t.concurrencyLimit).toBe(3);
    expect(t.concurrencyIsDefault).toBe(false);
    expect(t.actionLabel).toBe('Ship it');
    expect(t.actionLabelIsDefault).toBe(false);
  });
});
