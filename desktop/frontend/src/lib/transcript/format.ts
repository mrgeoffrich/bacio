// Small formatting helpers shared by the transcript viewer (BACI-125).

import type { ToolResultContent } from './types';

// formatTimeDelta produces the "+1.4s" / "+12s" / "+1m 4s" rendering
// the ticket asks for. `prev` may be undefined (first card) — then
// we return an empty string so the slot stays blank rather than
// showing "+NaN".
export function formatTimeDelta(ts?: string, prev?: string): string {
  if (!ts || !prev) return '';
  const a = Date.parse(ts);
  const b = Date.parse(prev);
  if (!Number.isFinite(a) || !Number.isFinite(b)) return '';
  const ms = a - b;
  if (ms < 0) return '';
  if (ms < 1000) return `+${ms}ms`;
  if (ms < 60_000) return `+${(ms / 1000).toFixed(1)}s`;
  const mins = Math.floor(ms / 60_000);
  const secs = Math.round((ms % 60_000) / 1000);
  return `+${mins}m ${secs}s`;
}

// formatTimestamp lifts an ISO timestamp into HH:MM:SS local for the
// first card / per-event tooltips. Falls back to the raw string if it
// can't parse — never crashes a render.
export function formatTimestamp(ts?: string): string {
  if (!ts) return '';
  const t = Date.parse(ts);
  if (!Number.isFinite(t)) return ts;
  const d = new Date(t);
  return d.toLocaleTimeString();
}

// countLines counts the line breaks in a string (line count = breaks
// + 1, or 0 for empty input). Used by CollapsibleBody for the
// "▼ result · N lines" header — never a `…` ellipsis.
export function countLines(s: string): number {
  if (!s) return 0;
  let n = 1;
  for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) === 10) n++;
  return n;
}

// flattenResultContent reduces a tool_result content value (string OR
// list of blocks) to a single plain string for the generic renderer.
// `text` blocks contribute their text; `image` blocks render as a
// placeholder; any other block falls through as compact JSON so the
// renderer degrades to readable JSON rather than dropping data.
export function flattenResultContent(content: ToolResultContent): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  const parts: string[] = [];
  for (const b of content) {
    if (!b || typeof b !== 'object') continue;
    const t = String((b as { type?: unknown }).type || '');
    if (t === 'text' && typeof (b as { text?: unknown }).text === 'string') {
      parts.push((b as { text: string }).text);
    } else if (t === 'image') {
      parts.push('[image]');
    } else {
      try {
        parts.push(JSON.stringify(b));
      } catch {
        parts.push(String(b));
      }
    }
  }
  return parts.join('\n');
}

// prettyJSON stringifies a value with 2-space indentation. Falls back
// to String(v) if the value isn't JSON-safe (cyclic, etc.).
export function prettyJSON(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

// formatBytes renders a byte count compactly. Same shape as the
// LinkedDocPanel helper but local to the transcript module so the
// viewer is self-contained.
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}
