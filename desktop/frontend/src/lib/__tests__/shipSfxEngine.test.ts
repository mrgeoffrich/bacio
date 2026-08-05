import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  armShipSfx,
  getShipSfxStatus,
  playShipSfx,
  resetShipSfxEngineForTests,
  setShipSfxEnabled,
  subscribeShipSfxStatus,
} from '../shipSfxEngine';

// BACI-375 regression suite. jsdom implements no Web Audio, so the whole
// suite is fake-driven: the point is to pin the *state machine* (which is
// what broke), not the browser's audio stack. Real playback is covered by
// the manual Safari / Chrome / desktop pass on the PR.
//
// The bug being pinned: BACI-336 marked the sound "unlocked" before its
// play() promise settled, so on WebKit — which grants autoplay per
// context, only from inside a gesture — the first-gesture unlock was
// disarmed forever and every later ship was silent. Nothing here may
// stand down until the context is genuinely running.

type FakeSource = {
  buffer: AudioBuffer | null;
  connect: ReturnType<typeof vi.fn>;
  start: ReturnType<typeof vi.fn>;
  stop: ReturnType<typeof vi.fn>;
  onended: (() => void) | null;
};

class FakeAudioContext {
  static instances: FakeAudioContext[] = [];
  static sources: FakeSource[] = [];
  // Whether resume() is granted. false models Safari refusing autoplay.
  static grantOnResume = true;

  state: AudioContextState = 'suspended';
  onstatechange: (() => void) | null = null;
  destination = {} as AudioDestinationNode;
  resumeCalls = 0;

  constructor() { FakeAudioContext.instances.push(this); }

  resume(): Promise<void> {
    this.resumeCalls += 1;
    // Browsers settle resume() asynchronously — flipping the state inside
    // the promise is what exercises the engine's "don't disarm
    // optimistically" path.
    return Promise.resolve().then(() => {
      if (!FakeAudioContext.grantOnResume) return;
      this.setState('running');
    });
  }

  setState(next: AudioContextState): void {
    this.state = next;
    this.onstatechange?.();
  }

  createGain(): GainNode {
    return { gain: { value: 0 }, connect: vi.fn() } as unknown as GainNode;
  }

  createBufferSource(): AudioBufferSourceNode {
    const src: FakeSource = {
      buffer: null, connect: vi.fn(), start: vi.fn(), stop: vi.fn(), onended: null,
    };
    FakeAudioContext.sources.push(src);
    return src as unknown as AudioBufferSourceNode;
  }

  decodeAudioData(_bytes: ArrayBuffer): Promise<AudioBuffer> {
    return Promise.resolve({ duration: 1 } as AudioBuffer);
  }
}

function okFetch() {
  return vi.fn(() => Promise.resolve({
    ok: true,
    status: 200,
    arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
  } as unknown as Response));
}

// flush drains the promise chains the engine fires and forgets (resume,
// fetch → arrayBuffer → decodeAudioData → publish).
function flush(): Promise<void> {
  return new Promise(resolve => { setTimeout(resolve, 0); });
}

function gesture(): void {
  window.dispatchEvent(new Event('pointerdown'));
}

function ctxOf(i = 0): FakeAudioContext {
  const c = FakeAudioContext.instances[i];
  if (!c) throw new Error(`no AudioContext at index ${i}`);
  return c;
}

// arm brings the engine to the state the app is in right after mount:
// listeners attached, toggle on, nothing built yet.
function arm(): void {
  armShipSfx('/assets/kaching-abc123.mp3');
  setShipSfxEnabled(true);
}

let warn: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  resetShipSfxEngineForTests();
  FakeAudioContext.instances = [];
  FakeAudioContext.sources = [];
  FakeAudioContext.grantOnResume = true;
  vi.stubGlobal('AudioContext', FakeAudioContext);
  vi.stubGlobal('fetch', okFetch());
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
  resetShipSfxEngineForTests();
  warn.mockRestore();
  vi.unstubAllGlobals();
});

describe('shipSfxEngine — the autoplay unlock', () => {
  it('does not disarm the gesture path when the unlock is refused', async () => {
    // THE BACI-375 BUG. Safari refuses the resume; the engine must stay
    // armed so the user's next click gets another go.
    FakeAudioContext.grantOnResume = false;
    arm();

    gesture();
    await flush();
    expect(ctxOf().resumeCalls).toBe(1);

    gesture();
    await flush();
    expect(ctxOf().resumeCalls).toBe(2);
    expect(FakeAudioContext.instances).toHaveLength(1);
  });

  it('detaches the listeners once the unlock actually lands', async () => {
    arm();

    gesture();
    await flush();
    expect(ctxOf().state).toBe('running');
    expect(ctxOf().resumeCalls).toBe(1);

    gesture();
    await flush();
    expect(ctxOf().resumeCalls).toBe(1);
  });

  it('re-arms the listeners when the context later drops out of running', async () => {
    arm();
    gesture();
    await flush();
    expect(ctxOf().resumeCalls).toBe(1);

    // A tab backgrounding / audio-device change / WebKit interruption.
    FakeAudioContext.grantOnResume = false;
    ctxOf().setState('suspended');
    expect(getShipSfxStatus().state).toBe('blocked');

    gesture();
    await flush();
    expect(ctxOf().resumeCalls).toBe(2);
  });

  it('builds nothing for an opted-out session, and a click while off is not counted as a refusal', async () => {
    armShipSfx('/assets/kaching-abc123.mp3');

    gesture();
    await flush();
    expect(FakeAudioContext.instances).toHaveLength(0);
    expect(getShipSfxStatus().state).toBe('off');

    // Turning the toggle on must read as "waiting for a click", not
    // "the browser refused us" — the click above proved nothing.
    setShipSfxEnabled(true);
    expect(getShipSfxStatus().state).toBe('locked');
  });
});

describe('shipSfxEngine — status', () => {
  it('walks off → locked → ready', async () => {
    expect(getShipSfxStatus().state).toBe('off');

    arm();
    expect(getShipSfxStatus().state).toBe('locked');

    gesture();
    await flush();
    expect(getShipSfxStatus().state).toBe('ready');
  });

  it('reports blocked rather than locked once a gesture has been refused', async () => {
    FakeAudioContext.grantOnResume = false;
    arm();

    gesture();
    await flush();
    const status = getShipSfxStatus();
    expect(status.state).toBe('blocked');
    expect(status.detail).toMatch(/suspended/);
  });

  it('returns a referentially stable snapshot when nothing changed', async () => {
    arm();
    gesture();
    await flush();

    const first = getShipSfxStatus();
    playShipSfx();
    expect(getShipSfxStatus()).toBe(first);
  });

  it('notifies subscribers on a genuine change only', async () => {
    const seen = vi.fn();
    const unsubscribe = subscribeShipSfxStatus(seen);

    arm();                       // off → locked
    expect(seen).toHaveBeenCalledTimes(1);

    gesture();
    await flush();               // → ready (via loading)
    const afterUnlock = seen.mock.calls.length;
    expect(getShipSfxStatus().state).toBe('ready');

    playShipSfx();               // no state change
    expect(seen).toHaveBeenCalledTimes(afterUnlock);

    unsubscribe();
    setShipSfxEnabled(false);
    expect(seen).toHaveBeenCalledTimes(afterUnlock);
  });
});

describe('shipSfxEngine — playback', () => {
  it('starts a source node when the context is running and the sound is decoded', async () => {
    arm();
    gesture();
    await flush();

    playShipSfx();
    expect(FakeAudioContext.sources).toHaveLength(1);
    expect(FakeAudioContext.sources[0].start).toHaveBeenCalledTimes(1);
    expect(FakeAudioContext.sources[0].buffer).not.toBeNull();
  });

  it('stops the previous source so back-to-back ships restart rather than overlap', async () => {
    arm();
    gesture();
    await flush();

    playShipSfx();
    playShipSfx();
    expect(FakeAudioContext.sources).toHaveLength(2);
    expect(FakeAudioContext.sources[0].stop).toHaveBeenCalledTimes(1);
  });

  it('starts nothing on a suspended context, warns once, and retries the resume', async () => {
    FakeAudioContext.grantOnResume = false;
    arm();
    gesture();
    await flush();
    const before = ctxOf().resumeCalls;

    playShipSfx();
    playShipSfx();
    await flush();

    expect(FakeAudioContext.sources).toHaveLength(0);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(ctxOf().resumeCalls).toBeGreaterThan(before);
  });

  it('starts nothing and builds no context while disabled', async () => {
    armShipSfx('/assets/kaching-abc123.mp3');

    playShipSfx();
    await flush();

    expect(FakeAudioContext.instances).toHaveLength(0);
    expect(FakeAudioContext.sources).toHaveLength(0);
  });
});

describe('shipSfxEngine — loading the sound', () => {
  it('lands a fetch failure as unavailable with the reason, and a later ship retries', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({
      ok: false, status: 404, arrayBuffer: () => Promise.resolve(new ArrayBuffer(0)),
    } as unknown as Response)));
    arm();

    gesture();
    await flush();
    const failed = getShipSfxStatus();
    expect(failed.state).toBe('unavailable');
    expect(failed.detail).toMatch(/404/);

    // The asset comes good (a slow static server, a transient blip).
    vi.stubGlobal('fetch', okFetch());
    playShipSfx();
    await flush();
    expect(getShipSfxStatus().state).toBe('ready');
  });
});
