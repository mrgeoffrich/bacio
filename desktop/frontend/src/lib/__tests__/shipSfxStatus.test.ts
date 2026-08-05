import { describe, it, expect } from 'vitest';
import { decideShipSfxStatus } from '../shipSfxGate';
import type { ShipSfxStatusInput } from '../shipSfxGate';

// BACI-375: decideShipSfxStatus is the diagnosability contract — it is
// what turns "the ka-ching didn't play" from a mystery into a sentence in
// the Settings pane. The two rows that matter most are the pair the old
// HTMLAudioElement path could never tell apart: a context that hasn't had
// a gesture yet (locked, benign) vs one that had a gesture and is still
// not running (blocked, the browser refused us).

function input(over: Partial<ShipSfxStatusInput> = {}): ShipSfxStatusInput {
  return {
    enabled: true,
    supported: true,
    gestureSeen: false,
    contextState: null,
    bufferReady: false,
    loadError: '',
    ...over,
  };
}

describe('decideShipSfxStatus', () => {
  it('reports off when the toggle is off, whatever else is true', () => {
    const s = decideShipSfxStatus(input({
      enabled: false, contextState: 'running', bufferReady: true, loadError: 'boom',
    }));
    expect(s).toEqual({ state: 'off', detail: '' });
  });

  it('reports unavailable when Web Audio is unreachable', () => {
    const s = decideShipSfxStatus(input({ supported: false }));
    expect(s.state).toBe('unavailable');
    expect(s.detail).toMatch(/Web Audio/);
  });

  it('reports unavailable with the load error, ahead of the context state', () => {
    const s = decideShipSfxStatus(input({
      contextState: 'running', bufferReady: false, loadError: "Couldn't load the sound: HTTP 404",
    }));
    expect(s).toEqual({ state: 'unavailable', detail: "Couldn't load the sound: HTTP 404" });
  });

  it('reports unavailable for a closed context', () => {
    const s = decideShipSfxStatus(input({ contextState: 'closed', gestureSeen: true }));
    expect(s.state).toBe('unavailable');
  });

  it('reports locked when no gesture has landed yet', () => {
    expect(decideShipSfxStatus(input({ contextState: null })).state).toBe('locked');
    expect(decideShipSfxStatus(input({ contextState: 'suspended' })).state).toBe('locked');
  });

  it('reports blocked once a gesture has been seen and the context is still not running', () => {
    const s = decideShipSfxStatus(input({ gestureSeen: true, contextState: 'suspended' }));
    expect(s.state).toBe('blocked');
    expect(s.detail).toMatch(/suspended/);
  });

  it('reports blocked for a WebKit interruption', () => {
    const s = decideShipSfxStatus(input({ gestureSeen: true, contextState: 'interrupted' as AudioContextState }));
    expect(s.state).toBe('blocked');
    expect(s.detail).toMatch(/interrupted/);
  });

  it('reports loading when the context is running but the sound is not decoded', () => {
    const s = decideShipSfxStatus(input({ gestureSeen: true, contextState: 'running', bufferReady: false }));
    expect(s).toEqual({ state: 'loading', detail: '' });
  });

  it('reports ready when the context is running and the sound is decoded', () => {
    const s = decideShipSfxStatus(input({ gestureSeen: true, contextState: 'running', bufferReady: true }));
    expect(s).toEqual({ state: 'ready', detail: '' });
  });

  it('reports ready without a gesture when the browser granted autoplay outright', () => {
    // Chromium's origin-level sticky activation can leave the context
    // running before this page ever sees a click.
    const s = decideShipSfxStatus(input({ gestureSeen: false, contextState: 'running', bufferReady: true }));
    expect(s.state).toBe('ready');
  });
});
