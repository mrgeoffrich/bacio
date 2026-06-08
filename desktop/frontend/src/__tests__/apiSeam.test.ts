import { describe, it, expect } from 'vitest';
// Importing `../api` proves the Vitest harness resolves the transport
// seam end-to-end: the alias swaps `./api` → api.http.ts (the HTTP
// transport) exactly as the web build does, and the whole module graph
// (wire reshapers, lib helpers) loads cleanly under jsdom without the
// Wails runtime. If the alias or jsdom env regressed, this import throws
// and every component test below would fail for the same reason — so it
// earns its keep as the cheapest canary.
import * as api from '../api';

describe('api seam', () => {
  it('resolves ./api to the HTTP transport and exposes its runtime functions', () => {
    expect(typeof api.listBoards).toBe('function');
    expect(typeof api.listCards).toBe('function');
    expect(typeof api.getIssue).toBe('function');
    expect(typeof api.getIssueBrief).toBe('function');
  });
});
