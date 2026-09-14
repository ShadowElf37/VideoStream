import { useCallback, useEffect, useRef, useState } from 'react';
import type { Room } from 'livekit-client';
import { RoomEvent } from 'livekit-client';
import { api } from '@/lib/api';
import { Topics, type PlaybackState } from '@/proto/messages';
import { useSession } from '@/state/session';
import { addSample, bestOffset, clientNowMs, offsetOf, settle, type ClockSample } from './clock';
import { usePlaybackStore } from './store';

/**
 * The room's playback state, and this client's offset from the director's
 * clock.
 *
 * Two independent streams of truth arrive here: the state itself, which says
 * where the film is, and the clock offset, which says what "now" means. Both
 * are needed before a target can be computed at all.
 */

const decoder = new TextDecoder();

/** Startup burst: a single probe can be a hundred milliseconds wrong. */
const BURST = 5;
const BURST_GAP_MS = 400;

/**
 * Steady rate. Two consumer clocks drift by well under a millisecond a minute
 * — orders below the deadband — so this is about surviving suspends and route
 * changes, not about drift.
 */
const PROBE_PERIOD_MS = 15_000;

export interface Playback {
  state: PlaybackState | null;
  /** Client→director clock offset in ms, or null until measured. */
  offsetMs: number | null;
  /** True once both a state and an offset are known. */
  ready: boolean;
}

export function usePlayback(room: Room | null, connected: boolean): Playback {
  const [state, setState] = useState<PlaybackState | null>(null);
  const [offsetMs, setOffsetMs] = useState<number | null>(null);
  const window = useRef<ClockSample[]>([]);

  // Fetch once on join, so the first frame is right without waiting for a
  // broadcast, and again on reconnect where any number of them were missed.
  const refetch = useCallback(() => {
    const session = useSession.getState().token?.session;
    if (!session) return;
    void api
      .getPlayback(session)
      .then(setState)
      .catch(() => undefined);
  }, []);

  useEffect(() => {
    if (!connected) return;
    refetch();
  }, [connected, refetch]);

  // Broadcasts. Unreliable and 1 Hz, which is fine because each one is
  // self-sufficient: a lost packet costs nothing, the next one still says
  // exactly where the film is.
  useEffect(() => {
    if (!room) return;
    const onData = (payload: Uint8Array, _p: unknown, _k: unknown, topic?: string) => {
      if (topic !== Topics.playback) return;
      try {
        const next = JSON.parse(decoder.decode(payload)) as PlaybackState;
        // Drop a packet that overtook a newer one.
        setState((cur) => (cur && cur.seq > next.seq ? cur : next));
      } catch {
        /* a malformed packet is not worth tearing anything down for */
      }
    };
    room.on(RoomEvent.DataReceived, onData);
    room.on(RoomEvent.Reconnected, refetch);
    return () => {
      room.off(RoomEvent.DataReceived, onData);
      room.off(RoomEvent.Reconnected, refetch);
    };
  }, [room, refetch]);

  // Clock probes.
  useEffect(() => {
    if (!connected) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const probe = async () => {
      const t0 = clientNowMs();
      try {
        const { nowMs } = await api.serverTime();
        const t3 = clientNowMs();
        // The server answers from one clock reading, so its receive and send
        // are the same instant. The round trip still excludes whatever the
        // network did in each direction.
        window.current = addSample(window.current, offsetOf(t0, nowMs, nowMs, t3));
        const best = bestOffset(window.current);
        if (best !== null && !stopped) setOffsetMs((cur) => settle(cur, best));
      } catch {
        /* a failed probe just means no new sample */
      }
    };

    void (async () => {
      for (let i = 0; i < BURST && !stopped; i++) {
        await probe();
        await new Promise((r) => setTimeout(r, BURST_GAP_MS));
      }
      const tick = async () => {
        if (stopped) return;
        await probe();
        timer = setTimeout(() => void tick(), PROBE_PERIOD_MS);
      };
      timer = setTimeout(() => void tick(), PROBE_PERIOD_MS);
    })();

    // Waking from sleep or changing network invalidates every assumption the
    // window is built on.
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        window.current = [];
        void probe();
      }
    };
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [connected]);

  // Publish for everything that is not on this component's branch of the tree:
  // the transport bar, the seek bar, the keyboard handler.
  useEffect(() => {
    usePlaybackStore.getState().set(state, offsetMs);
  }, [state, offsetMs]);

  return { state, offsetMs, ready: state !== null && offsetMs !== null };
}
