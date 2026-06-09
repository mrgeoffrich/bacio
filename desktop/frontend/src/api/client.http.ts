// Shared HTTP plumbing for the web transport (BACI-359).
//
// The per-domain api/*.http.ts modules build on `call` / `callText` here
// instead of each re-deriving the fetch + auth + error-envelope dance. Only
// the web bundle ever loads this file — Vite swaps `./api` to api.http.ts in
// web mode, and these modules hang off that barrel.

// API base URL. Empty string => same-origin (the recommended deployment,
// where `bacio api` serves both /ui/ and /repos/...). Set VITE_BACIO_API
// at build time to point cross-origin at a different host.
export const API_BASE = (import.meta.env.VITE_BACIO_API ?? '').replace(/\/+$/, '');

// Read the bearer token from localStorage so the user can paste it in
// from a Settings field (or a dev console) without rebuilding.
export function readToken(): string {
  try { return localStorage.getItem('bacio.token') || ''; }
  catch { return ''; }
}

// Actor identifier stamped onto the X-Actor header. Defaults to "web"
// so audit rows are at least distinguishable from CLI / API direct
// usage; the user can override via localStorage['bacio.actor'].
export function readActor(): string {
  try { return localStorage.getItem('bacio.actor') || 'web'; }
  catch { return 'web'; }
}

interface FetchOpts {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined>;
}

export async function call<T>(path: string, opts: FetchOpts = {}): Promise<T> {
  const method = opts.method ?? 'GET';
  const url = new URL(API_BASE + path, window.location.origin);
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === '') continue;
      url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = { 'X-Actor': readActor() };
  const token = readToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  let body: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  const res = await fetch(url.toString(), { method, headers, body });
  // 204 No Content paths (e.g. DELETE) come back without a body.
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!res.ok) {
    // The server uses a {error, code, details} envelope; surface the
    // human message preferentially, falling back to status text.
    let msg = `${res.status} ${res.statusText}`;
    if (text) {
      try {
        const parsed = JSON.parse(text);
        if (parsed?.error) msg = parsed.error;
      } catch { msg = text; }
    }
    throw new Error(msg);
  }
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// callText is the text/plain twin of call<T> for endpoints that return a
// non-JSON body (BACI-308's raw .http capture passthrough). Same auth /
// error-envelope handling, but the 2xx body is returned verbatim rather than
// JSON-parsed.
export async function callText(path: string, opts: FetchOpts = {}): Promise<string> {
  const method = opts.method ?? 'GET';
  const url = new URL(API_BASE + path, window.location.origin);
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === '') continue;
      url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = { 'X-Actor': readActor() };
  const token = readToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(url.toString(), { method, headers });
  const text = await res.text();
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    if (text) {
      try {
        const parsed = JSON.parse(text);
        if (parsed?.error) msg = parsed.error;
      } catch { msg = text; }
    }
    throw new Error(msg);
  }
  return text;
}

// preferencePair (BACI-359) collapses the GET + PUT-with-body + reshape
// boilerplate the global preference endpoints all share. `fromWire` maps the
// snake_case server payload onto the camelCase DTO; both the read and the
// write go through it. The per-pair set() wrappers build the wire body from
// their typed args. (Per-repo settings with extra verbs — default-feature's
// DELETE, board-hidden-states' null-coalesce — stay bespoke.)
export function preferencePair<Wire, Dto>(path: string, fromWire: (w: Wire) => Dto) {
  return {
    get: (): Promise<Dto> => call<Wire>(path).then(fromWire),
    set: (body: Wire): Promise<Dto> => call<Wire>(path, { method: 'PUT', body }).then(fromWire),
  };
}

// WebModeUnavailableError is thrown by stubs for surfaces the web build
// deliberately doesn't implement (e.g. the native folder picker without a
// path). Components SHOULD gate on WEB_MODE before calling these — this is
// the belt-and-braces second line, surfaced via the reportError modal.
export class WebModeUnavailableError extends Error {
  constructor(feature: string) {
    super(`${feature} is not available in the web build of bacio`);
    this.name = 'WebModeUnavailableError';
  }
}
