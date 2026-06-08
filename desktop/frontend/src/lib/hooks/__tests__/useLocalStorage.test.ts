import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useLocalStorage } from '../useLocalStorage';

// useLocalStorage mirrors React state to localStorage. jsdom provides a real
// localStorage, so the round-trip suite drives the actual store and clears it
// between tests; the fallback suite stubs getItem/setItem to throw to prove a
// hardened profile is non-fatal.
describe('useLocalStorage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('returns the initial value when the key is absent', () => {
    const { result } = renderHook(() => useLocalStorage('k', 'fallback'));
    expect(result.current[0]).toBe('fallback');
  });

  it('reads and deserializes a value already on disk', () => {
    localStorage.setItem('k', 'stored');
    const { result } = renderHook(() => useLocalStorage('k', 'fallback'));
    expect(result.current[0]).toBe('stored');
  });

  it('round-trips: a persisted change is readable by a fresh hook and on disk', () => {
    const first = renderHook(() => useLocalStorage('k', 'a'));
    act(() => first.result.current[1]('b'));
    expect(first.result.current[0]).toBe('b');
    expect(localStorage.getItem('k')).toBe('b');

    // A second mount sees the persisted value, not the initial.
    const second = renderHook(() => useLocalStorage('k', 'a'));
    expect(second.result.current[0]).toBe('b');
  });

  it('accepts a functional updater like useState', () => {
    localStorage.setItem('n', '1');
    const { result } = renderHook(() =>
      useLocalStorage<number>('n', 0, {
        serialize: (v) => String(v),
        deserialize: (raw) => Number(raw),
      }),
    );
    expect(result.current[0]).toBe(1);
    act(() => result.current[1]((prev) => prev + 1));
    expect(result.current[0]).toBe(2);
    expect(localStorage.getItem('n')).toBe('2');
  });

  it('honours a custom codec for the exact on-disk format (boolean as 1/0)', () => {
    const codec = {
      serialize: (v: boolean) => (v ? '1' : '0'),
      deserialize: (raw: string) => raw === '1',
    };
    const { result } = renderHook(() => useLocalStorage<boolean>('flag', false, codec));

    expect(result.current[0]).toBe(false);
    act(() => result.current[1](true));
    expect(result.current[0]).toBe(true);
    expect(localStorage.getItem('flag')).toBe('1');

    act(() => result.current[1](false));
    expect(localStorage.getItem('flag')).toBe('0');
  });

  describe('hardened-profile fallback (storage throws on access)', () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    it('falls back to the initial value when getItem throws', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new Error('getItem denied');
      });
      const { result } = renderHook(() => useLocalStorage('k', 'fallback'));
      expect(result.current[0]).toBe('fallback');
    });

    it('keeps updating in-memory state when setItem throws, without raising', () => {
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
        throw new Error('setItem denied');
      });
      const { result } = renderHook(() => useLocalStorage('k', 'a'));
      // Persistence is best-effort; the swallowed throw must not bubble out of
      // the setter, and the live value still tracks the update.
      expect(() => act(() => result.current[1]('b'))).not.toThrow();
      expect(result.current[0]).toBe('b');
    });
  });
});
