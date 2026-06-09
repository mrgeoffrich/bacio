// Shared Wails error normaliser (BACI-359). The generated bindings reject
// with assorted shapes; collapse them to Error so the per-domain wrappers
// can stay terse and the React layer sees a consistent error type.
export function normalize(err: unknown): Error {
  if (err instanceof Error) return err;
  if (typeof err === 'string') return new Error(err);
  return new Error(String((err as { message?: unknown })?.message ?? err));
}
