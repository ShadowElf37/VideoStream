/**
 * What this client tells the director when the room is waiting for everyone.
 *
 * The predicate is the same one the start gate uses — "could I play through
 * from here without stalling a second later" — because the answer the director
 * wants is exactly the answer the player already computes for itself. Anything
 * looser and the room starts and someone stalls anyway, which is the failure
 * waitForEveryone exists to prevent.
 */

import { START_BUFFER_MS } from './sync';

/** How often to repeat the answer, whether or not it changed. */
export const REPORT_PERIOD_MS = 2000;

/** The tolerance for "buffered to the end", matching the director's atEnd. */
const END_SLACK_MS = 250;

export function isReady(bufferedAheadMs: number, positionMs: number, durationMs: number): boolean {
  if (bufferedAheadMs >= START_BUFFER_MS) return true;
  // The last three seconds of a film will never have three seconds ahead of
  // them. Without this the room would hold at the credits until the timeout.
  return durationMs > 0 && positionMs + bufferedAheadMs >= durationMs - END_SLACK_MS;
}

/** The key that decides whether an answer is worth sending early. */
export function reportKey(gen: number, ready: boolean): string {
  return `${gen}:${ready}`;
}
