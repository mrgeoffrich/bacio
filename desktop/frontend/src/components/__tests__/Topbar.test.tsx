import { describe, it, expect } from 'vitest';
import { NAV } from '../Topbar';

// NAV is the contract App.tsx maps the digit hotkeys (1..6) onto, in
// order, so its shape and ordering are load-bearing beyond the topbar
// render. Importing Topbar also pulls its whole component graph (which
// reaches the `./api` seam) through the test harness, so a transport
// regression surfaces here too.

describe('Topbar NAV', () => {
  it('lists the top-nav views in the order the digit hotkeys map onto', () => {
    expect(NAV.map(n => n.view)).toEqual([
      'pipeline',
      'features',
      'docs',
      'agents',
      'history',
      'monitor',
    ]);
  });

  it('pairs every view with a non-empty label', () => {
    for (const item of NAV) {
      expect(item.view).toBeTruthy();
      expect(item.label).toBeTruthy();
    }
  });
});
