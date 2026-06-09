import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { reportError } from '../../errors';

// useOptimisticToggle is the simplest form of the BACI-357 optimistic dance:
// a self-owned boolean that flips on screen immediately, persists in the
// background, and reverts if the persist rejects. It is the single helper the
// PipelineView display toggles (auto-ship / backlog-collapsed / impact-primary)
// consume today, and the one Phase 4b's FeaturePropertyToggle is built on — so
// it lives here, built once.
//
// The value is owned by the hook but `setValue` is exposed so a fetch effect
// can seed it from the server (`setValue(!!serverValue)`) without tripping the
// optimistic/persist machinery — only `toggle` / `set` persist.
//
// By default a failed persist reverts *silently* (no modal), matching the
// PipelineView toggles: a flaky display-preference PUT just flips back. Pass an
// `errorHeadline` to also surface the failure through the central error bus.

export type OptimisticToggleOptions = {
  // reportError headline on a failed persist. Omit for a silent revert.
  errorHeadline?: string;
};

export type OptimisticToggle = {
  value: boolean;
  // Plain setter for seeding from a fetch, bypassing persist/rollback.
  setValue: Dispatch<SetStateAction<boolean>>;
  // Flip optimistically, persist, revert on failure.
  toggle: () => void;
  // Optimistically set a specific value, persist, revert on failure.
  set: (next: boolean) => void;
};

export function useOptimisticToggle(
  initial: boolean,
  persist: (next: boolean) => unknown | Promise<unknown>,
  options: OptimisticToggleOptions = {},
): OptimisticToggle {
  const [value, setValue] = useState<boolean>(initial);

  // Latest-value refs so `set` / `toggle` stay stable (never re-created on a
  // value or inline-persist change) yet always read the current value, persist
  // fn, and error policy — Dan Abramov's pattern, same as useInterval.
  const valueRef = useRef(value);
  const persistRef = useRef(persist);
  const headlineRef = useRef(options.errorHeadline);
  useEffect(() => {
    valueRef.current = value;
    persistRef.current = persist;
    headlineRef.current = options.errorHeadline;
  });

  const set = useCallback((next: boolean) => {
    const prev = valueRef.current;
    if (next === prev) return; // nothing to flip, nothing to persist
    setValue(next); // optimistic
    Promise.resolve(persistRef.current(next)).catch(err => {
      setValue(prev); // revert the optimistic flip
      if (headlineRef.current) {
        reportError(err, { headline: headlineRef.current });
      }
    });
  }, []);

  const toggle = useCallback(() => {
    set(!valueRef.current);
  }, [set]);

  return { value, setValue, toggle, set };
}
