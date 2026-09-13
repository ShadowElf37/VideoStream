import { describe, expect, it } from 'vitest';
import {
  addSample,
  bestOffset,
  MAX_AGE_MS,
  MAX_RTT_MS,
  offsetOf,
  settle,
  SNAP_MS,
  targetMs,
  WINDOW,
  type ClockSample,
} from './clock';

const sample = (offsetMs: number, rttMs: number, at = 0): ClockSample => ({ offsetMs, rttMs, at });

describe('offsetOf', () => {
  it('recovers a known offset from a symmetric round trip', () => {
    // Server runs 1000ms ahead; 40ms each way.
    const t0 = 0;
    const t1 = 1040; // server receive
    const t2 = 1050; // server send, 10ms later
    const t3 = 90; // client receive
    const s = offsetOf(t0, t1, t2, t3);
    expect(s.offsetMs).toBeCloseTo(1000, 0);
    // The round trip excludes the server's own handling time.
    expect(s.rttMs).toBeCloseTo(80, 0);
  });

  it('is unaffected by how long the server takes to answer', () => {
    const fast = offsetOf(0, 1040, 1050, 90);
    const slow = offsetOf(0, 1040, 1540, 580);
    expect(slow.offsetMs).toBeCloseTo(fast.offsetMs, 0);
    expect(slow.rttMs).toBeCloseTo(fast.rttMs, 0);
  });
});

describe('addSample', () => {
  it('keeps a bounded window', () => {
    let w: ClockSample[] = [];
    for (let i = 0; i < WINDOW + 5; i++) w = addSample(w, sample(i, 50, i));
    expect(w).toHaveLength(WINDOW);
  });

  it('drops samples old enough to be from before a sleep', () => {
    const w = addSample([sample(5, 20, 0)], sample(6, 20, MAX_AGE_MS + 1));
    expect(w).toHaveLength(1);
    expect(w[0].offsetMs).toBe(6);
  });

  it('refuses a round trip too long to mean anything', () => {
    // The offset error a queued sample hides is bounded by rtt/2, so this one
    // could be seconds wrong.
    const w = addSample([], sample(999, MAX_RTT_MS + 1));
    expect(w).toHaveLength(0);
  });

  it('refuses nonsense rather than poisoning the window', () => {
    expect(addSample([], sample(NaN, 20))).toHaveLength(0);
    expect(addSample([], sample(5, NaN))).toHaveLength(0);
    expect(addSample([], sample(5, -10))).toHaveLength(0);
  });
});

describe('bestOffset', () => {
  it('trusts the lowest round trip, not the average', () => {
    // The 400ms sample is badly queued and its offset is far off; the quick
    // one is the honest measurement.
    const w = [sample(1000, 400), sample(1200, 30), sample(900, 350)];
    expect(bestOffset(w)).toBe(1200);
  });

  it('is null before anything usable arrives', () => {
    expect(bestOffset([])).toBeNull();
  });
});

describe('settle', () => {
  it('takes the first estimate outright', () => {
    expect(settle(null, 1234)).toBe(1234);
  });

  it('eases small changes instead of stepping the target', () => {
    const next = settle(1000, 1100);
    expect(next).toBeGreaterThan(1000);
    expect(next).toBeLessThan(1100);
  });

  it('snaps a large change, which means the machine woke or the path moved', () => {
    expect(settle(1000, 1000 + SNAP_MS + 1)).toBe(1000 + SNAP_MS + 1);
  });
});

describe('targetMs', () => {
  const anchor = { anchorPosMs: 10_000, anchorAtMs: 500_000, rate: 1, paused: false, durationMs: 600_000 };

  it('advances with the clock while playing', () => {
    // Client clock reads 502_000 and is 0 offset from the server: 2s elapsed.
    expect(targetMs(anchor, 0, 502_000)).toBe(12_000);
  });

  it('applies the clock offset', () => {
    // This client's clock is 1s behind the server's, so the same reading is
    // actually a second later in server time.
    expect(targetMs(anchor, 1000, 502_000)).toBe(13_000);
  });

  it('does not move while paused, whatever the clock says', () => {
    const paused = { ...anchor, paused: true };
    expect(targetMs(paused, 0, 502_000)).toBe(10_000);
    expect(targetMs(paused, 0, 900_000)).toBe(10_000);
  });

  it('honours the rate', () => {
    expect(targetMs({ ...anchor, rate: 2 }, 0, 502_000)).toBe(14_000);
  });

  it('clamps to the film rather than running past the end', () => {
    expect(targetMs(anchor, 0, 9_999_999)).toBe(600_000);
  });

  it('never returns a negative target', () => {
    expect(targetMs(anchor, 0, 0)).toBe(0);
  });
});
