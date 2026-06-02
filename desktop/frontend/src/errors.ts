// errors.ts — central error bus for the desktop app (BACI-43).
//
// Every unexpected failure in the UI (Wails binding rejection, thrown
// exception, render-time crash, unhandled promise) goes through
// reportError(). The ErrorModal mounted in App.tsx subscribes to the
// bus and surfaces one error at a time in a dismissable modal.
//
// The bus is a module-level singleton (not a React context) so
// non-React callers reach it too: the window.error /
// unhandledrejection global handlers, and ErrorBoundary's
// componentDidCatch.

export type ReportedError = {
  id: string;
  headline: string;
  message: string;
  detail?: string;
  source?: string;
  at: number;
  count: number;
};

export type ReportOptions = {
  headline?: string;
  source?: string;
};

type Listener = (e: ReportedError) => void;

const listeners = new Set<Listener>();
// Recent reports for coalescing: a back-to-back duplicate (same
// headline + message) within COALESCE_WINDOW_MS bumps the existing
// entry's count instead of pushing a new one. The cap is so a
// sustained failure (DB read-only, network down) doesn't flood the
// queue.
const RECENT_LIMIT = 16;
const COALESCE_WINDOW_MS = 2_000;
const recent: ReportedError[] = [];

function normalize(err: unknown): { message: string; detail?: string } {
  if (err instanceof Error) {
    return { message: err.message || String(err), detail: err.stack };
  }
  if (typeof err === 'string') return { message: err };
  if (err == null) return { message: 'Unknown error' };
  const m = (err as { message?: unknown }).message;
  if (typeof m === 'string' && m) return { message: m };
  try { return { message: JSON.stringify(err) }; }
  catch { return { message: String(err) }; }
}

// humanise strips a few common Go-side prefixes our services tack on
// ("boardservice.go:NN:", "service:", "bacio:") so the modal body
// reads as a sentence, not a stack frame.
function humanise(raw: string): string {
  let s = raw.trim();
  s = s.replace(/^[a-z_]+\.go:\d+:\s*/, '');
  s = s.replace(/^(service|bacio):\s*/i, '');
  return s;
}

// makeId is a tiny client-side uuid replacement. We don't ship a uuid
// dep in this package and the id is only used as a React key — a
// timestamp + counter is plenty unique.
let idSeq = 0;
function makeId(): string {
  idSeq += 1;
  return `${Date.now()}-${idSeq}`;
}

export function reportError(err: unknown, opts: ReportOptions = {}): void {
  const { message: rawMessage, detail } = normalize(err);
  const message = humanise(rawMessage);
  const headline = opts.headline ?? 'Something went wrong';
  const now = Date.now();

  // Coalesce repeats so a flapping poll doesn't fan into 30 modals.
  const last = recent.length > 0 ? recent[recent.length - 1] : null;
  if (
    last &&
    last.headline === headline &&
    last.message === message &&
    now - last.at < COALESCE_WINDOW_MS
  ) {
    last.count += 1;
    last.at = now;
    listeners.forEach(l => l(last));
    return;
  }

  const entry: ReportedError = {
    id: makeId(),
    headline,
    message,
    detail,
    source: opts.source,
    at: now,
    count: 1,
  };
  recent.push(entry);
  while (recent.length > RECENT_LIMIT) recent.shift();
  listeners.forEach(l => l(entry));
}

export function subscribeErrors(listener: Listener): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

// installGlobalErrorHandlers wires window.error and unhandledrejection
// to the bus. Called once from main.tsx. Belt-and-braces: every known
// async call site already uses .catch(reportError), but a sync throw
// in an event handler, a stray Promise.reject() without a .catch, or
// a setTimeout callback would otherwise vanish.
export function installGlobalErrorHandlers(): void {
  window.addEventListener('error', (e) => {
    reportError(e.error ?? e.message, { source: 'window.error' });
  });
  window.addEventListener('unhandledrejection', (e) => {
    reportError(e.reason, { source: 'unhandled-rejection' });
  });
}
