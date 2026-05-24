// tokenSummary walks the parsed event stream and sums per-turn
// `message.usage` token counters. Feeds the header strip
// ("18.2k in / 4.1k out · 0.42 cache hit") and the per-event tooltip
// on assistant cards (BACI-125).

import type { TokenUsage, TranscriptEvent } from './types';

export type TokenSummary = {
  totalInput: number;
  totalOutput: number;
  totalCacheCreate: number;
  totalCacheRead: number;
  // cacheRead / (cacheRead + cacheCreate + input). 0 when nothing was
  // billed yet (avoids NaN in the badge text).
  cacheHitRatio: number;
};

function add(sum: number, v: number | undefined): number {
  return sum + (typeof v === 'number' && Number.isFinite(v) ? v : 0);
}

export function summariseTokens(events: TranscriptEvent[]): TokenSummary {
  let totalInput = 0;
  let totalOutput = 0;
  let totalCacheCreate = 0;
  let totalCacheRead = 0;
  for (const ev of events) {
    if (ev.kind !== 'assistant' || !ev.usage) continue;
    const u: TokenUsage = ev.usage;
    totalInput = add(totalInput, u.input_tokens);
    totalOutput = add(totalOutput, u.output_tokens);
    totalCacheCreate = add(totalCacheCreate, u.cache_creation_input_tokens);
    totalCacheRead = add(totalCacheRead, u.cache_read_input_tokens);
  }
  const denom = totalCacheRead + totalCacheCreate + totalInput;
  const cacheHitRatio = denom > 0 ? totalCacheRead / denom : 0;
  return {
    totalInput,
    totalOutput,
    totalCacheCreate,
    totalCacheRead,
    cacheHitRatio,
  };
}

// formatKilo renders a token count compactly — "18.2k" / "412".
export function formatKilo(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (n < 1000) return String(Math.round(n));
  return `${(n / 1000).toFixed(1)}k`;
}
