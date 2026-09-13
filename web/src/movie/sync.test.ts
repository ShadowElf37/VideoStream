import { describe, expect, it } from 'vitest';
import {
  bufferedAheadMs,
  decide,
  ENTER_MS,
  ESCALATE_AFTER,
  EXIT_MS,
  HARD_MS,
  initialSyncState,
  MAX_RATE_DELTA,
  MIN_SEEK_INTERVAL_MS,
  SETTLE_MS,
  type SyncInput,
  type SyncState,
} from './sync';

const base: SyncInput = {
  errorMs: 0,
  now: 100_000,
  busy: false,
  gen: 1,
  bufferedAheadMs: 30_000,
  paused: false,
};

/** settled returns a state that has already seen generation 1 and is idle. */
function settled(over: Partial<SyncState> = {}): SyncState {
  return { ...initialSyncState, lastGen: 1, lastSeekAt: 0, ...over };
}

describe('decide', () => {
  it('does nothing while the error is within the deadband', () => {
    const { action } = decide({ ...base, errorMs: EXIT_MS - 10 }, settled());
    expect(action.kind).toBe('none');
    expect(action.rate).toBe(1);
  });

  it('nudges the rate for a moderate error, in the direction that closes it', () => {
    // Behind the room: play faster.
    const behind = decide({ ...base, errorMs: -400 }, settled());
    expect(behind.action.kind).toBe('rate');
    expect(behind.action.rate).toBeGreaterThan(1);

    // Ahead of the room: play slower.
    const ahead = decide({ ...base, errorMs: 400 }, settled());
    expect(ahead.action.kind).toBe('rate');
    expect(ahead.action.rate).toBeLessThan(1);
  });

  it('never exceeds the rate authority, where the change becomes audible', () => {
    for (const errorMs of [-999, -600, 600, 999]) {
      const { action } = decide({ ...base, errorMs }, settled());
      expect(Math.abs(action.rate - 1)).toBeLessThanOrEqual(MAX_RATE_DELTA + 1e-9);
    }
  });

  it('hard-seeks once the error is past correcting by rate', () => {
    const { action } = decide({ ...base, errorMs: HARD_MS + 500 }, settled());
    expect(action.kind).toBe('seek');
    expect(action.rate).toBe(1);
  });

  it('holds its mode through the hysteresis band instead of toggling', () => {
    // Cross ENTER to start correcting.
    let s = decide({ ...base, errorMs: ENTER_MS + 20 }, settled()).state;
    expect(s.correcting).toBe(true);

    // Between EXIT and ENTER it must keep correcting, not flap off.
    const mid = decide({ ...base, errorMs: (EXIT_MS + ENTER_MS) / 2 }, s);
    expect(mid.action.kind).toBe('rate');
    s = mid.state;

    // Only below EXIT does it stop, and then the rate returns to exactly 1 —
    // a stale nudge is how a client drifts in the direction it last corrected.
    const out = decide({ ...base, errorMs: EXIT_MS - 5 }, s);
    expect(out.action.kind).toBe('none');
    expect(out.action.rate).toBe(1);
  });

  it('follows a generation change immediately, whatever the error', () => {
    const { action } = decide({ ...base, gen: 2, errorMs: 5 }, settled());
    expect(action.kind).toBe('seek');
    if (action.kind === 'seek') expect(action.reason).toBe('generation');
  });

  it('does not seek on the very first state it sees', () => {
    // Joining is not a discontinuity to chase; the caller positions the
    // element before the loop starts.
    const { action } = decide({ ...base, gen: 7, errorMs: 5 }, initialSyncState);
    expect(action.kind).toBe('none');
  });

  it('ignores the error while seeking or stalled', () => {
    const busy = decide({ ...base, errorMs: 5000, busy: true }, settled());
    expect(busy.action.kind).toBe('none');

    // And for a moment after a seek, when the reading is meaningless.
    const justSought = decide(
      { ...base, errorMs: 5000, now: 100_000 },
      settled({ lastSeekAt: 100_000 - SETTLE_MS / 2 }),
    );
    expect(justSought.action.kind).toBe('none');
  });

  it('rate-limits hard seeks rather than storming', () => {
    // Past the settle window, so the error is trusted again, but still inside
    // the minimum interval between seeks.
    const lastSeekAt = 100_000;
    const now = lastSeekAt + SETTLE_MS + 100;
    expect(now - lastSeekAt).toBeLessThan(MIN_SEEK_INTERVAL_MS);

    const { action } = decide({ ...base, errorMs: 5000, now }, settled({ lastSeekAt }));
    expect(action.kind).toBe('rate');
  });

  it('will not jump forward into an empty buffer', () => {
    // Behind, with nothing buffered where it would land: jumping stalls again
    // immediately, and again after that. Run fast and let the buffer refill.
    const { action } = decide(
      { ...base, errorMs: -5000, bufferedAheadMs: 200 },
      settled(),
    );
    expect(action.kind).toBe('rate');
    expect(action.rate).toBeGreaterThan(1);
  });

  it('does jump forward when the target is buffered', () => {
    const { action } = decide(
      { ...base, errorMs: -5000, bufferedAheadMs: 10_000 },
      settled(),
    );
    expect(action.kind).toBe('seek');
  });

  it('widens the threshold after repeated seeks instead of seizing', () => {
    let s = settled();
    let now = base.now;
    for (let i = 0; i < ESCALATE_AFTER; i++) {
      const d = decide({ ...base, errorMs: 5000, now }, s);
      expect(d.action.kind).toBe('seek');
      s = d.state;
      now += MIN_SEEK_INTERVAL_MS + SETTLE_MS + 1;
    }
    expect(s.escalatedUntil).toBeGreaterThan(now);

    // An error that would have been a seek before is now corrected by rate.
    const after = decide({ ...base, errorMs: HARD_MS + 400, now }, s);
    expect(after.action.kind).toBe('rate');
  });

  describe('while paused', () => {
    it('does nothing for a small error', () => {
      const { action } = decide({ ...base, paused: true, errorMs: 60 }, settled());
      expect(action.kind).toBe('none');
    });

    it('snaps a large one, because a still picture hides the correction', () => {
      const { action } = decide({ ...base, paused: true, errorMs: 900 }, settled());
      expect(action.kind).toBe('seek');
      if (action.kind === 'seek') expect(action.reason).toBe('paused');
    });

    it('never leaves a rate nudge running', () => {
      const s = settled({ correcting: true });
      const { action, state } = decide({ ...base, paused: true, errorMs: 10 }, s);
      expect(action.rate).toBe(1);
      expect(state.correcting).toBe(false);
    });
  });
});

describe('bufferedAheadMs', () => {
  const ranges = (pairs: Array<[number, number]>): TimeRanges =>
    ({
      length: pairs.length,
      start: (i: number) => pairs[i][0],
      end: (i: number) => pairs[i][1],
    }) as TimeRanges;

  it('measures to the end of the range containing the position', () => {
    expect(bufferedAheadMs(ranges([[0, 30]]), 10)).toBe(20_000);
  });

  it('is zero in a gap', () => {
    expect(bufferedAheadMs(ranges([[0, 10], [40, 60]]), 25)).toBe(0);
  });

  it('picks the range the position is actually in', () => {
    expect(bufferedAheadMs(ranges([[0, 10], [40, 60]]), 45)).toBe(15_000);
  });

  it('handles no buffer at all', () => {
    expect(bufferedAheadMs(null, 5)).toBe(0);
    expect(bufferedAheadMs(ranges([]), 5)).toBe(0);
  });
});
