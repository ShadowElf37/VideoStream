import { create } from 'zustand';

/**
 * The control loop's instrumentation.
 *
 * `HostedMovie` recomputes drift, buffer depth and rate four times a second in
 * order to decide what to do, and until this existed it threw all of it away
 * except one boolean. A sync loop you cannot observe is one you cannot debug:
 * with two viewers on two machines, "the film is a bit out" is not a report
 * anyone can act on, and "you are 340 ms ahead, buffer 12 s, rate 1.000, last
 * correction a seek 9 s ago" is.
 *
 * A store rather than props because the writer is the player and the reader is
 * the stats overlay, which live on different branches of the tree.
 */

/** The last thing the loop actually did. `kind: 'none'` never lands here. */
export interface Correction {
  kind: 'rate' | 'seek';
  /** 'generation' | 'drift' | 'paused' for a seek; the signed rate for a nudge. */
  reason: string;
  /** Client clock, `clientNowMs()`. */
  at: number;
}

export interface SyncStats {
  /** Signed drift in ms; positive means this client is ahead of the room. */
  errorMs: number;
  bufferedAheadMs: number;
  /** True while the gate is holding the picture for want of buffer. */
  buffering: boolean;
  rate: number;
  /** The room's generation counter, so a missed jump is visible as a jump. */
  gen: number;
  /** This client's measured offset from the director's clock. */
  offsetMs: number;
  lastCorrection: Correction | null;
  /** When this sample was taken, so a frozen readout is obvious. */
  at: number;
}

interface SyncStatsStore {
  stats: SyncStats | null;
  report(s: SyncStats): void;
  clear(): void;
}

export const useSyncStats = create<SyncStatsStore>()((set) => ({
  stats: null,
  report: (stats) => set({ stats }),
  // Cleared when the hosted player goes away, which is what hides the block:
  // stale sync numbers over a live RTP picture would be a lie.
  clear: () => set({ stats: null }),
}));

/**
 * Thresholds for reading drift at a glance. 100 ms is where the controller
 * itself starts correcting (`ENTER_MS`); 500 ms is half the point at which it
 * gives up nudging and jumps, and comfortably past where a shared reaction in
 * voice chat gives the gap away.
 */
export const DRIFT_WARN_MS = 100;
export const DRIFT_BAD_MS = 500;

export type Tone = 'ok' | 'warn' | 'bad';

export function driftTone(errorMs: number): Tone {
  const m = Math.abs(errorMs);
  if (!Number.isFinite(m) || m >= DRIFT_BAD_MS) return 'bad';
  if (m >= DRIFT_WARN_MS) return 'warn';
  return 'ok';
}

/** Signed, so "which side of the room am I on" is readable without thinking. */
export function formatDrift(errorMs: number): string {
  if (!Number.isFinite(errorMs)) return '—';
  const r = Math.round(errorMs);
  const sign = r > 0 ? '+' : r < 0 ? '−' : '';
  return `${sign}${Math.abs(r)} ms`;
}

/** Buffer ahead: seconds once it is deep, milliseconds while it is not. */
export function formatAhead(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—';
  return ms >= 10_000 ? `${(ms / 1000).toFixed(0)} s` : `${(ms / 1000).toFixed(1)} s`;
}

/** Four decimals: a 5% nudge is 1.05, and a stale 1.003 is the bug to catch. */
export function formatRate(rate: number): string {
  return Number.isFinite(rate) ? rate.toFixed(4) : '—';
}

export function formatCorrection(c: Correction | null, now: number): string {
  if (!c) return 'none';
  const ago = Math.max(0, Math.round((now - c.at) / 1000));
  return `${c.kind} · ${c.reason} · ${ago}s ago`;
}
