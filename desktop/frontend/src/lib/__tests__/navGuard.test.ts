import { describe, it, expect, vi, beforeEach } from 'vitest';
import { registerNavGuard, requestNavigation, resetNavGuards } from '../navGuard';

// The registry on its own, away from any component tree. The contract is
// easy to get subtly wrong in a way no page-level test would catch — an
// unregister that clears the slot unconditionally would strand the app
// with navigation permanently blocked, or permanently unguarded.

beforeEach(() => resetNavGuards());

describe('navGuard', () => {
  it('runs the navigation immediately when nothing is armed', () => {
    const proceed = vi.fn();
    requestNavigation(proceed);
    expect(proceed).toHaveBeenCalledTimes(1);
  });

  it('hands the navigation to an armed guard instead of running it', () => {
    const held: (() => void)[] = [];
    registerNavGuard((proceed) => {
      held.push(proceed);
      return true;
    });

    const proceed = vi.fn();
    requestNavigation(proceed);
    expect(proceed).not.toHaveBeenCalled();
    expect(held).toHaveLength(1);

    // The guard owns it, and running it later is what completes the trip.
    held[0]();
    expect(proceed).toHaveBeenCalledTimes(1);
  });

  it('continues immediately when a guard declines to intercept', () => {
    // A guard that is mounted but not currently blocking — the clean-form
    // case — must not cost the user a click.
    registerNavGuard(() => false);
    const proceed = vi.fn();
    requestNavigation(proceed);
    expect(proceed).toHaveBeenCalledTimes(1);
  });

  it('stops intercepting once unregistered', () => {
    const unregister = registerNavGuard(() => true);
    const blocked = vi.fn();
    requestNavigation(blocked);
    expect(blocked).not.toHaveBeenCalled();

    unregister();
    const allowed = vi.fn();
    requestNavigation(allowed);
    expect(allowed).toHaveBeenCalledTimes(1);
  });

  it('restores the previous guard when the inner one unregisters', () => {
    const outer = vi.fn(() => true);
    registerNavGuard(outer);
    const inner = vi.fn(() => true);
    const dropInner = registerNavGuard(inner);

    requestNavigation(() => {});
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();

    dropInner();
    requestNavigation(() => {});
    expect(outer).toHaveBeenCalledTimes(1);
  });

  it('ignores a stale unregister that fires after a newer guard armed', () => {
    // Unmount order isn't guaranteed; a late cleanup must not disarm the
    // guard that replaced it.
    const dropFirst = registerNavGuard(() => true);
    const second = vi.fn(() => true);
    registerNavGuard(second);

    dropFirst();
    requestNavigation(() => {});
    expect(second).toHaveBeenCalledTimes(1);
  });
});
