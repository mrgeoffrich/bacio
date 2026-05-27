#!/usr/bin/env python3
"""Generate a short 'ka-ching' style WAV for the BACI-240 Shipped pill.

Hand-rolled in-process so the asset is unambiguously CC0 (we made it,
we release it). Two onset transients (the 'ka' and 'ching'), each a
short sum of musical-fifth-related sine partials with an exponential
decay envelope, written as mono 16-bit PCM WAV. Targets ~7-15 KB so
the embed cost is trivial.

Usage:
  python3 scripts/gen_kaching.py desktop/frontend/src/assets/shipped-ka-ching.wav

Re-run any time the asset needs to be regenerated. The WAV file is
checked in (Vite's URL-import shape), this script is the auditable
provenance record alongside assets/LICENSE.txt.
"""

import math
import struct
import sys
import wave


SAMPLE_RATE = 22050   # 22.05 kHz is plenty for a UI ping; halves bytes vs 44.1k.
TOTAL_MS = 320        # ~320ms wall-clock total.
CHANNELS = 1
SAMPLE_WIDTH = 2      # 16-bit signed PCM.


def envelope(t_ms: float, attack_ms: float, decay_ms: float) -> float:
    """Triangle attack + exponential decay envelope, peak == 1.0."""
    if t_ms < 0 or t_ms > attack_ms + decay_ms:
        return 0.0
    if t_ms <= attack_ms:
        return t_ms / attack_ms
    # Exponential decay over `decay_ms` down to ~-43 dB.
    d = (t_ms - attack_ms) / decay_ms
    return math.exp(-5.0 * d)


def tone_burst(start_ms: float, attack_ms: float, decay_ms: float,
               freqs):
    """A sum of sine partials, each (Hz, gain), windowed by the envelope.

    Returns a list of float samples covering the whole TOTAL_MS span
    (zero outside the burst's envelope window) so the caller can sum
    multiple bursts without offset bookkeeping.
    """
    n = int(SAMPLE_RATE * TOTAL_MS / 1000)
    out = [0.0] * n
    for i in range(n):
        t_ms = 1000.0 * i / SAMPLE_RATE
        local_ms = t_ms - start_ms
        env = envelope(local_ms, attack_ms, decay_ms)
        if env == 0.0:
            continue
        sample = 0.0
        for f, g in freqs:
            sample += g * math.sin(2.0 * math.pi * f * (local_ms / 1000.0))
        out[i] = env * sample
    return out


def main(path: str) -> None:
    n_samples = int(SAMPLE_RATE * TOTAL_MS / 1000)

    # "Ka" — short, lower, percussive. A C5-ish thunk with a touch of
    # second-harmonic for body.
    ka = tone_burst(start_ms=0, attack_ms=4, decay_ms=80,
                    freqs=[(523.25, 0.55), (1046.5, 0.20)])
    # "Ching" — bell-like, higher, longer tail. Major fifth above the
    # ka with two octave overtones.
    ching = tone_burst(start_ms=70, attack_ms=6, decay_ms=240,
                       freqs=[(987.77, 0.45), (1975.5, 0.25), (1318.5, 0.18)])

    mix = [ka[i] + ching[i] for i in range(n_samples)]

    # Normalise to peak amplitude 0.85 to leave headroom.
    peak = max(abs(x) for x in mix) or 1.0
    scale = 0.85 / peak

    pcm = bytearray()
    for x in mix:
        v = int(max(-1.0, min(1.0, x * scale)) * 32767)
        pcm += struct.pack("<h", v)

    with wave.open(path, "wb") as w:
        w.setnchannels(CHANNELS)
        w.setsampwidth(SAMPLE_WIDTH)
        w.setframerate(SAMPLE_RATE)
        w.writeframes(bytes(pcm))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: gen_kaching.py <output.wav>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1])
