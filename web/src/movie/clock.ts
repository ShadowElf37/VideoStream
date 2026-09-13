/**
 * Estimating this client's offset from the director's clock.
 *
 * Everything downstream is built on `target = anchorPos + (now + offset -
 * anchorAt) * rate`, so the offset is the difference between "in sync" and
 * "confidently wrong by a third of a second".
 *
 * The method is NTP's, because the problem is NTP's: a single measurement
 * cannot separate the offset from the path delay, but the *smallest* round
 * trip in a window has the least room to hide asymmetry, so it is the one to
 * trust. Averaging offsets is worse — it averages in the queuing spikes.
 */

export interface ClockSample {
  offsetMs: number;
  rttMs: number;
  /** Local time the sample was taken, for ageing the window out. */
  at: number;
}

/** How many samples to keep. Eight spans about two minutes at the steady rate. */
export const WINDOW = 8;

/** Samples older than this are stale — a laptop may have slept since. */
export const MAX_AGE_MS = 90_000;

/**
 * A round trip longer than this cannot be trusted at all: the offset error a
 * queued sample can hide is bounded by rtt/2, so a 1.5s sample may be 750ms
 * wrong, which dwarfs the deadband it would be feeding.
 */
export const MAX_RTT_MS = 1500;

/**
 * Local time, monotonic within the page session.
 *
 * `performance.timeOrigin + performance.now()` rather than `Date.now()`:
 * immune to the user's OS clock being stepped or the machine sleeping, both of
 * which would otherwise read as a sudden enormous drift.
 */
export function clientNowMs(): number {
  return performance.timeOrigin + performance.now();
}

/**
 * offsetOf computes one sample from a round trip, NTP-style.
 *
 * t0 send, t1 server receive, t2 server send, t3 client receive. Taking the
 * server's receive and send separately is what removes its own handling time
 * from the estimate rather than charging it to the network.
 */
export function offsetOf(t0: number, t1: number, t2: number, t3: number): ClockSample {
  return {
    offsetMs: (t1 - t0 + (t2 - t3)) / 2,
    rttMs: t3 - t0 - (t2 - t1),
    at: t3,
  };
}

/** addSample appends a sample, dropping stale and implausible ones. */
export function addSample(window: ClockSample[], s: ClockSample): ClockSample[] {
  if (!Number.isFinite(s.offsetMs) || !Number.isFinite(s.rttMs) || s.rttMs < 0) return window;
  const fresh = window.filter((w) => s.at - w.at < MAX_AGE_MS);
  // Keep a bad sample out rather than letting it win by being the newest.
  if (s.rttMs > MAX_RTT_MS) return fresh;
  return [...fresh, s].slice(-WINDOW);
}

/**
 * bestOffset returns the offset from the lowest-round-trip sample, or null
 * when nothing usable has arrived yet.
 */
export function bestOffset(window: ClockSample[]): number | null {
  let best: ClockSample | null = null;
  for (const s of window) {
    if (!best || s.rttMs < best.rttMs) best = s;
  }
  return best ? best.offsetMs : null;
}

/**
 * settle moves the applied offset toward a new estimate.
 *
 * Small changes are eased in so the sync controller is not chasing a step it
 * did not cause; a large one is taken immediately, because that is a laptop
 * waking or a network changing, and slewing several seconds at this rate would
 * take a minute of being visibly wrong.
 */
export const SNAP_MS = 250;
const ALPHA = 0.2;

export function settle(current: number | null, next: number): number {
  if (current === null) return next;
  if (Math.abs(next - current) > SNAP_MS) return next;
  return current + (next - current) * ALPHA;
}

/** targetMs is where the film should be, on this client's clock. */
export function targetMs(
  anchor: { anchorPosMs: number; anchorAtMs: number; rate: number; paused: boolean; durationMs: number },
  offsetMs: number,
  now = clientNowMs(),
): number {
  if (anchor.paused) return clampMs(anchor.anchorPosMs, anchor.durationMs);
  const elapsed = (now + offsetMs - anchor.anchorAtMs) * (anchor.rate || 1);
  return clampMs(anchor.anchorPosMs + elapsed, anchor.durationMs);
}

function clampMs(v: number, duration: number): number {
  if (!Number.isFinite(v) || v < 0) return 0;
  if (duration > 0 && v > duration) return duration;
  return v;
}
