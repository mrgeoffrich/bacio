import { useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';

// A codec turns a value of type T into the exact string stored on disk and
// back. The default (below) is identity for string-typed values; non-string
// preferences (a boolean stored as '1'/'0', a validated string union, …)
// supply their own so each key keeps its precise on-disk serialization.
export type LocalStorageCodec<T> = {
  serialize: (value: T) => string;
  deserialize: (raw: string) => T;
};

// readLocalStorage / writeLocalStorage are the hardened-profile-safe
// primitives every localStorage-backed preference in the app builds on.
// localStorage is always present inside the Wails webview, but a hardened
// browser profile can throw on access (and a non-browser env — SSR, a bare
// test runner — may not define it at all). Both are non-fatal: a read falls
// back to `null`, a write is silently dropped. Consolidated here (BACI-355)
// from the half-dozen bespoke try/catch read/persist pairs that each
// re-implemented this fallback (App theme/active-repo, the Docs sort/sidebar
// prefs, the Shipping-column scope) — a single place to get the fallback
// right rather than a place to forget the try/catch and leak.
export function readLocalStorage(key: string): string | null {
  try {
    return globalThis.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

export function writeLocalStorage(key: string, value: string): void {
  try {
    globalThis.localStorage?.setItem(key, value);
  } catch {
    /* non-fatal — a hardened profile just won't persist the preference */
  }
}

// Default codec: identity for string-typed values. `String(value)` round-trips
// a string verbatim, and the deserialize cast hands the raw string straight
// back — callers relying on the default are responsible for T being string-
// shaped (theme, the repo prefix). Anything else passes an explicit codec.
function defaultSerialize<T>(value: T): string {
  return String(value);
}
function defaultDeserialize<T>(raw: string): T {
  return raw as unknown as T;
}

// useLocalStorage is a `useState` drop-in whose value is mirrored to
// localStorage under `key`. It reads the persisted value lazily on mount
// (falling back to `initial` when the key is absent or storage is
// unavailable) and re-persists on every change, all through the
// readLocalStorage / writeLocalStorage primitives so the hardened-profile
// fallback stays non-fatal and lives in one place. Pass a `codec` for
// non-string values to preserve the key's exact on-disk format.
//
// The serializer is held in a latest-value ref (Dan Abramov's pattern, same
// as useInterval) so an inline codec never churns the persist effect — the
// effect re-runs only when the key or the value actually changes.
export function useLocalStorage<T>(
  key: string,
  initial: T,
  codec?: Partial<LocalStorageCodec<T>>,
): [T, Dispatch<SetStateAction<T>>] {
  const serialize = codec?.serialize ?? defaultSerialize;
  const deserialize = codec?.deserialize ?? defaultDeserialize;

  const [value, setValue] = useState<T>(() => {
    const raw = readLocalStorage(key);
    return raw === null ? initial : deserialize(raw);
  });

  // Keep the ref pointed at the latest serializer so the persist effect can
  // depend on [key, value] alone — an inline `{ serialize }` would otherwise
  // be a fresh identity each render and re-run the write needlessly.
  const serializeRef = useRef(serialize);
  useEffect(() => {
    serializeRef.current = serialize;
  }, [serialize]);

  useEffect(() => {
    writeLocalStorage(key, serializeRef.current(value));
  }, [key, value]);

  return [value, setValue];
}
