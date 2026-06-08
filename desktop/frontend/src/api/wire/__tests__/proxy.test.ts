import { describe, it, expect } from 'vitest';
import { reshapeProxyStat, reshapeProxyCapture, reshapeJobTranscript } from '../proxy';

// BACI-358: proxy reshapers, testable now they live outside api.http.ts.
// Cover the snake→camel rename, the raw_log_path → hasRaw flag, and the
// nested usage rename + zero-fill.

describe('reshapeProxyStat', () => {
  it('renames the request_count / p50_ms style fields', () => {
    expect(reshapeProxyStat({
      host: 'api.anthropic.com',
      request_count: 5,
      bytes_in: 100,
      bytes_out: 200,
      error_count: 1,
      error_rate: 0.2,
      p50_ms: 10,
      p95_ms: 50,
      first_seen: 'f',
      last_seen: 'l',
    })).toEqual({
      host: 'api.anthropic.com',
      requestCount: 5,
      bytesIn: 100,
      bytesOut: 200,
      errorCount: 1,
      errorRate: 0.2,
      p50Ms: 10,
      p95Ms: 50,
      firstSeen: 'f',
      lastSeen: 'l',
    });
  });
});

describe('reshapeProxyCapture', () => {
  it('derives hasRaw from raw_log_path and coerces the boolean flags', () => {
    const r = reshapeProxyCapture({
      id: 1, method: 'POST', host: 'h', path: '/v1', status: 200,
      bytes_in: 10, bytes_out: 20, duration_ms: 30,
      raw_log_path: '/tmp/x.http', is_stream: true, is_anthropic: true,
      dispatch_id: 7, issue_key: 'BACI-1', mode: 'implement', started_at: 's',
    });
    expect(r.hasRaw).toBe(true);
    expect(r.isStream).toBe(true);
    expect(r.isAnthropic).toBe(true);
    expect(r.durationMs).toBe(30);
    expect(r.dispatchId).toBe(7);
  });

  it('hasRaw is false when raw_log_path is absent', () => {
    const r = reshapeProxyCapture({
      id: 2, method: 'GET', host: 'h', path: '/p', status: 200,
      bytes_in: 0, bytes_out: 0, duration_ms: 0, started_at: 's',
    });
    expect(r.hasRaw).toBe(false);
    expect(r.isStream).toBe(false);
  });
});

describe('reshapeJobTranscript', () => {
  it('renames the nested usage tokens and zero-fills the missing ones', () => {
    const r = reshapeJobTranscript({
      dispatch_id: 9, turn_count: 3, last_seen: 'l',
      usage: { input_tokens: 100, cache_read_input_tokens: 5 },
    });
    expect(r.dispatchId).toBe(9);
    expect(r.turnCount).toBe(3);
    expect(r.usage).toEqual({
      inputTokens: 100,
      outputTokens: 0,
      cacheCreationTokens: 0,
      cacheReadTokens: 5,
      thinkingTokens: 0,
    });
  });
});
