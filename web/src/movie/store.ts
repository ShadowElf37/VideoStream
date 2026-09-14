import { create } from 'zustand';
import { useEffect, useState } from 'react';
import type { PlaybackState } from '@/proto/messages';
import { useMpvStore, useSmoothTimePos } from '@/host/useMpv';
import { clientNowMs, targetMs } from './clock';

/**
 * The room's playback state, where anything can read it.
 *
 * A store rather than props because the transport bar, the seek bar, the stage
 * and the keyboard handler all need the same answer to "what is playing and
 * where is it", and they are scattered across the tree. This mirrors
 * `useMpvStore`, which does the same job for the live path.
 */
interface PlaybackStore {
  state: PlaybackState | null;
  offsetMs: number | null;
  set(state: PlaybackState | null, offsetMs: number | null): void;
}

export const usePlaybackStore = create<PlaybackStore>()((set) => ({
  state: null,
  offsetMs: null,
  set: (state, offsetMs) => set({ state, offsetMs }),
}));

/**
 * What is playing and where, from whichever player owns the room.
 *
 * The controls should not have to know which one that is. Before this existed
 * they read mpv's state unconditionally, so with a server-hosted film they saw
 * no projector, no duration and no position — and rendered themselves greyed
 * out at 0:00 over a film that was playing perfectly.
 */
export interface NowPlaying {
  /** True when the server is playing a file. */
  hosted: boolean;
  /** Nothing loaded anywhere. */
  idle: boolean;
  paused: boolean;
  /** Seconds. */
  position: number;
  duration: number;
  /** False when there is no player at all to command. */
  controllable: boolean;
  title: string;
  /**
   * The room is parked at a discontinuity waiting for everyone to buffer
   * (`waitForEveryone`). It is paused as well — holding is the reason, and
   * this is what lets the stage explain it instead of showing a bare PAUSED.
   */
  holding: boolean;
  /** Who it is waiting for. */
  waitingFor: string[];
}

export function useNowPlaying(): NowPlaying {
  const playback = usePlaybackStore((s) => s.state);
  const offsetMs = usePlaybackStore((s) => s.offsetMs);
  const mpv = useMpvStore((s) => s.state);
  const projectorOnline = useMpvStore((s) => s.projectorOnline);
  const livePos = useSmoothTimePos();

  const hosted = !!playback && !playback.idle && !!playback.url;
  const hostedPos = useHostedPosition(hosted ? playback : null, offsetMs);

  if (hosted && playback) {
    return {
      hosted: true,
      idle: false,
      paused: playback.paused,
      position: hostedPos,
      duration: playback.durationMs / 1000,
      controllable: true,
      title: playback.title,
      holding: playback.holding,
      waitingFor: playback.waitingFor ?? [],
    };
  }
  return {
    hosted: false,
    idle: mpv?.idle ?? true,
    paused: mpv?.pause ?? true,
    position: livePos,
    duration: mpv?.duration ?? 0,
    // The live path needs its projector present to command anything.
    controllable: projectorOnline,
    title: mpv?.mediaTitle ?? '',
    // The live projector has no buffer to wait on: it publishes RTP and the
    // clients take what arrives.
    holding: false,
    waitingFor: [],
  };
}

/**
 * A ticking position for hosted playback, computed from the anchor rather than
 * read off the element — so the bar shows where the *room* is, which is what
 * everyone else sees too.
 */
function useHostedPosition(state: PlaybackState | null, offsetMs: number | null): number {
  const [pos, setPos] = useState(0);
  useEffect(() => {
    if (!state || offsetMs === null) return;
    const tick = () =>
      setPos(
        targetMs(
          {
            anchorPosMs: state.anchorPosMs,
            anchorAtMs: state.anchorAtMs,
            rate: state.rate,
            paused: state.paused,
            durationMs: state.durationMs,
          },
          offsetMs,
          clientNowMs(),
        ) / 1000,
      );
    tick();
    if (state.paused) return;
    const id = setInterval(tick, 100);
    return () => clearInterval(id);
  }, [state, offsetMs]);
  return pos;
}
