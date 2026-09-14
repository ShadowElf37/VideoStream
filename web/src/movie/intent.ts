import { create } from 'zustand';
import { formatTime } from '@/lib/format';
import type { PlaybackIntent } from '@/proto/messages';

/**
 * What the room just did, and whose doing it was.
 *
 * Pause has a loud red glyph; a seek had nothing at all, so a viewer saw the
 * picture jump with no idea who moved it or why. The director echoes the
 * intent on its own topic the moment a command lands, ahead of the state, and
 * this turns that into something to look at.
 *
 * Everything below the store is pure, because the wording is the feature: the
 * difference between "Seeked" and "Alice skipped to 1:12:30" is the whole
 * issue, and that is worth having tests argue over.
 */

/**
 * The line between a nudge and a jump. A ±10 s tap is a correction and gets a
 * corner chip; going to a different part of the film deserves the middle of
 * the screen, because the viewer has lost their place and needs to be told
 * where they are now.
 */
export const SMALL_SEEK_MS = 30_000;

/** How long a seek echo stays up. Long enough to read, short enough to ignore. */
export const ECHO_MS = 1500;

export type IntentDisplay =
  | { kind: 'none' }
  | { kind: 'chip'; text: string }
  | { kind: 'glyph'; forward: boolean; time: string; who: string }
  | { kind: 'loading'; title: string; who: string };

export function describeIntent(i: PlaybackIntent | null): IntentDisplay {
  if (!i) return { kind: 'none' };
  const who = i.actor.name.trim();
  if (i.action === 'load') {
    return { kind: 'loading', title: i.title || 'the next title', who };
  }
  if (i.action !== 'seek') return { kind: 'none' };

  const delta = i.toMs - i.fromMs;
  const forward = delta >= 0;
  if (Math.abs(delta) <= SMALL_SEEK_MS) {
    const arrow = forward ? '⏩' : '⏪';
    const size = `${Math.max(1, Math.round(Math.abs(delta) / 1000))} s`;
    return { kind: 'chip', text: who ? `${arrow} ${size} · ${who}` : `${arrow} ${size}` };
  }
  return { kind: 'glyph', forward, time: formatTime(i.toMs / 1000), who };
}

/**
 * The transient toast for the actions that have no glyph of their own.
 *
 * Null for a seek — it has the chip — and null for the actor themselves, who
 * pressed the button and does not need telling.
 */
export function intentToast(i: PlaybackIntent, selfIdentity: string | undefined): string | null {
  if (selfIdentity && i.actor.identity === selfIdentity) return null;
  const who = i.actor.name.trim();
  if (!who) return null;
  switch (i.action) {
    case 'pause':
      return `${who} paused`;
    case 'play':
      return `${who} resumed`;
    case 'stop':
      return `${who} stopped playback`;
    default:
      return null;
  }
}

/**
 * The chat system line, which everybody gets including the actor — chat is the
 * record of what happened, not a notification.
 *
 * Neutral when nobody is named: that is the server advancing a playlist, and
 * claiming an actor we do not have is exactly the mistake the live mpv path
 * avoids by staying vague.
 */
export function intentSystemLine(i: PlaybackIntent): string | null {
  const who = i.actor.name.trim();
  const at = formatTime(i.toMs / 1000);
  switch (i.action) {
    case 'load':
      return who ? `${who} started ${i.title || 'a new title'}` : `Now playing: ${i.title || 'a new title'}`;
    case 'pause':
      return who ? `${who} paused at ${at}` : `Paused at ${at}`;
    case 'play':
      return who ? `${who} resumed at ${at}` : `Resumed at ${at}`;
    case 'stop':
      return who ? `${who} stopped playback` : 'Playback stopped';
    case 'seek': {
      const delta = i.toMs - i.fromMs;
      const dir = delta >= 0 ? 'forward' : 'back';
      if (Math.abs(delta) <= SMALL_SEEK_MS) {
        const size = `${Math.max(1, Math.round(Math.abs(delta) / 1000))} s`;
        return who ? `${who} skipped ${dir} ${size}` : `Skipped ${dir} ${size}`;
      }
      return who ? `${who} skipped to ${at}` : `Skipped to ${at}`;
    }
    default:
      return null;
  }
}

interface IntentStore {
  intent: PlaybackIntent | null;
  /**
   * True when this arrived on the data channel rather than out of a state
   * snapshot. A late joiner is handed the last intent in their first state
   * fetch, and flashing a chip for a seek that happened ten minutes ago would
   * be a lie; the loading card, which is gated on the local player instead, is
   * exactly what they do want.
   */
  live: boolean;
  /** Set once the local player has started the title a load asked for. */
  started: boolean;
  /** Returns true when this was new; the caller then announces it. */
  receive(i: PlaybackIntent | null, live: boolean): boolean;
  markStarted(): void;
  clear(): void;
}

export const useIntentStore = create<IntentStore>()((set, get) => ({
  intent: null,
  live: false,
  started: false,
  receive: (i, live) => {
    if (!i) return false;
    const cur = get().intent;
    if (cur && i.seq <= cur.seq) {
      // The same echo arriving again out of a state snapshot must not
      // downgrade one we already saw live, nor reopen a card we closed.
      return false;
    }
    set({ intent: i, live, started: false });
    return true;
  },
  markStarted: () => set({ started: true }),
  clear: () => set({ intent: null, live: false, started: false }),
}));
