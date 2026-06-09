import { useMemo } from 'react';

// BACI-312: the IANA zone list for the timezone picker, sourced from the
// browser's own tz database via Intl.supportedValuesOf. Memoised — the
// list is large (~400 zones) and never changes for a session. The
// try/catch covers older engines that lack supportedValuesOf: we fall
// back to a tiny seed plus whatever's currently stored / detected so the
// picker still renders something selectable. (BACI-364: lifted verbatim
// out of SystemSettingsSection so the form shell stays thin.)
export function useTimezoneOptions(timezone: string): string[] {
  return useMemo(() => {
    let zones: string[] = [];
    // Intl.supportedValuesOf is ES2022; the project's lib target is ES2020,
    // so the global Intl type doesn't declare it. Narrow through a precise
    // optional-method view rather than reaching for `any`. The try/catch
    // (plus the optional call) covers engines that lack it entirely.
    const intl = Intl as typeof Intl & {
      supportedValuesOf?: (key: 'timeZone') => string[];
    };
    try {
      zones = intl.supportedValuesOf?.('timeZone') ?? ['UTC'];
    } catch {
      zones = ['UTC'];
    }
    const set = new Set(zones);
    // Always include the currently-stored value + the browser's own zone
    // so neither can ever be unselectable (e.g. a stored zone the engine
    // doesn't list, or a fresh DB before the auto-detect write lands).
    if (timezone) set.add(timezone);
    try {
      const browser = Intl.DateTimeFormat().resolvedOptions().timeZone;
      if (browser) set.add(browser);
    } catch { /* Intl unavailable — the seed list still renders */ }
    return Array.from(set).sort();
  }, [timezone]);
}
