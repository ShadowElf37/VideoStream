/**
 * The control law that keeps this client's playhead on the room's.
 *
 * Pure and separate from the element it drives, because every number here is a
 * judgement call that will be re-argued, and the only honest way to have that
 * argument is against tests.
 *
 * The shape is a deadband with hysteresis: do nothing while the error is
 * small, ease it out with an inaudible rate change while it is moderate, and
 * jump only when it is genuinely large. The alternative — correcting
 * continuously — means permanently running at the wrong speed to chase
 * measurement noise.
 */

/**
 * Below this the error is indistinguishable from measurement noise: roughly
 * one frame at 24fps, and about the floor of a WAN offset estimate.
 */
export const EXIT_MS = 50;

/**
 * Above this it is worth correcting. Two to three frames — below the point
 * where a shared reaction in voice chat reads as early.
 *
 * The 2:1 gap to EXIT_MS is the hysteresis: without it the controller toggles
 * on and off at the boundary and the film audibly wobbles.
 */
export const ENTER_MS = 100;

/**
 * How long a correction should take. A 500ms error closes in 10s at 5%: fast
 * enough that nobody sits out of sync through a scene, slow enough that the
 * rate change itself is not perceptible.
 */
export const TAU_MS = 10_000;

/**
 * Rate authority. `preservesPitch` is on by default in current browsers, so 5%
 * is inaudible on speech; past about 6% music starts to sound time-stretched.
 */
export const MAX_RATE_DELTA = 0.05;

/**
 * Past this, correcting by rate would take longer than a scene (a full second
 * needs 20s at 5%), and it is also where voice chat gives it away — someone
 * reacts in your ear before the line lands. Jump instead.
 */
export const HARD_MS = 1000;

/** After a jump the error reading is meaningless until the seek settles. */
export const SETTLE_MS = 1500;

/** A seek storm looks far worse than a second and a half of drift. */
export const MIN_SEEK_INTERVAL_MS = 3000;

/**
 * Repeated jumps almost always mean a bad offset estimate or a machine that
 * cannot decode at 1.0x. Widening the threshold and saying so beats seizing.
 */
export const ESCALATE_AFTER = 3;
export const ESCALATE_WINDOW_MS = 15_000;
export const ESCALATED_HARD_MS = 2500;
export const ESCALATION_HOLD_MS = 60_000;

export interface SyncInput {
  /** Signed error in ms: positive means this client is ahead of the room. */
  errorMs: number;
  /** Wall time now, for rate limiting. */
  now: number;
  /** True while the element is seeking, stalled, or has nothing to play. */
  busy: boolean;
  /** The room's generation; a change is licence to jump. */
  gen: number;
  /** Seconds of media buffered ahead of the target. */
  bufferedAheadMs: number;
  paused: boolean;
}

export interface SyncState {
  /** Timestamps of recent hard seeks, for rate limiting and escalation. */
  seeks: number[];
  /** When the last seek was issued, to let it settle. */
  lastSeekAt: number;
  /** Whether the rate nudge is currently engaged (the hysteresis latch). */
  correcting: boolean;
  lastGen: number;
  escalatedUntil: number;
}

export const initialSyncState: SyncState = {
  seeks: [],
  lastSeekAt: 0,
  correcting: false,
  lastGen: -1,
  escalatedUntil: 0,
};

export type SyncAction =
  | { kind: 'none'; rate: number }
  | { kind: 'rate'; rate: number }
  | { kind: 'seek'; rate: number; reason: 'generation' | 'drift' | 'paused' };

export interface SyncDecision {
  action: SyncAction;
  state: SyncState;
}

/**
 * decide works out what to do about the current error.
 *
 * Deliberately does not touch the media element — the caller applies it — so
 * this can be tested exhaustively without a DOM.
 */
export function decide(input: SyncInput, prev: SyncState): SyncDecision {
  const state: SyncState = { ...prev, seeks: prev.seeks };

  // A generation change is a real jump the room made, not drift. Follow it
  // immediately, and do not let the seek limiter refuse it: refusing would
  // leave this client watching a different part of the film.
  if (input.gen !== prev.lastGen) {
    state.lastGen = input.gen;
    if (prev.lastGen !== -1) {
      return seek(state, input.now, 'generation');
    }
  }

  // Paused is a free correction: the picture is already still, so snapping is
  // invisible. Most drift gets absorbed here and viewers never see a
  // correction at all.
  if (input.paused) {
    state.correcting = false;
    if (Math.abs(input.errorMs) > ENTER_MS * 2.5) {
      return seek(state, input.now, 'paused');
    }
    return { action: { kind: 'none', rate: 1 }, state };
  }

  // Mid-seek or stalled, the error reading means nothing.
  if (input.busy || input.now - state.lastSeekAt < SETTLE_MS) {
    return { action: { kind: 'none', rate: 1 }, state };
  }

  const hard = input.now < state.escalatedUntil ? ESCALATED_HARD_MS : HARD_MS;
  const e = input.errorMs;

  if (Math.abs(e) > hard) {
    // Behind and with nothing buffered where we would land: jumping forward
    // would stall again immediately, and again after that. Run fast and let
    // the buffer refill instead. This one condition is the difference between
    // recovering and spinning forever on a weak connection.
    const wouldStall = e < 0 && input.bufferedAheadMs < 1000;
    if (!wouldStall && input.now - state.lastSeekAt >= MIN_SEEK_INTERVAL_MS) {
      return seek(state, input.now, 'drift');
    }
    state.correcting = true;
    return { action: { kind: 'rate', rate: rateFor(e) }, state };
  }

  const magnitude = Math.abs(e);
  if (magnitude > ENTER_MS) state.correcting = true;
  else if (magnitude < EXIT_MS) state.correcting = false;

  if (!state.correcting) {
    // Exactly 1, not 1.003: leaving a stale nudge running is how a client ends
    // up permanently drifting in the direction it was last corrected.
    return { action: { kind: 'none', rate: 1 }, state };
  }
  return { action: { kind: 'rate', rate: rateFor(e) }, state };
}

function rateFor(errorMs: number): number {
  const delta = clamp(-errorMs / TAU_MS, -MAX_RATE_DELTA, MAX_RATE_DELTA);
  return 1 + delta;
}

function seek(state: SyncState, now: number, reason: 'generation' | 'drift' | 'paused'): SyncDecision {
  const seeks = [...state.seeks, now].filter((t) => now - t < ESCALATE_WINDOW_MS);
  const next: SyncState = { ...state, seeks, lastSeekAt: now, correcting: false };
  if (seeks.length >= ESCALATE_AFTER) {
    next.escalatedUntil = now + ESCALATION_HOLD_MS;
    next.seeks = [];
  }
  return { action: { kind: 'seek', rate: 1, reason }, state: next };
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

/** bufferedAheadMs measures contiguous buffered media after a position. */
export function bufferedAheadMs(ranges: TimeRanges | null, atSeconds: number): number {
  if (!ranges) return 0;
  for (let i = 0; i < ranges.length; i++) {
    if (atSeconds >= ranges.start(i) - 0.25 && atSeconds <= ranges.end(i)) {
      return (ranges.end(i) - atSeconds) * 1000;
    }
  }
  return 0;
}
