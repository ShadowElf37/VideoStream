import { useMemo } from 'react';
import { useMpv } from '@/host/useMpv';
import { api } from '@/lib/api';
import { useSession } from '@/state/session';

/**
 * One transport over two very different players.
 *
 * The controls should not care whether the film is a file on the server or a
 * live stream from someone's desktop, so this is the seam. Hosted commands go
 * over HTTP, where the session already proves the role; live commands go down
 * the data channel to the projector, which has to work out who sent them.
 */
export interface Transport {
  /** True when the server is the one playing the film. */
  hosted: boolean;
  togglePause(): Promise<void>;
  setPaused(paused: boolean): Promise<void>;
  /** Absolute when relative is false. */
  seek(ms: number, relative: boolean): Promise<void>;
  load(mediaId: string, mode: 'replace' | 'append'): Promise<void>;
  /** Override a waitForEveryone hold: start now, whoever is still buffering. */
  start(): Promise<void>;
}

export function useTransport(hosted: boolean): Transport {
  const mpv = useMpv();

  return useMemo<Transport>(() => {
    const session = () => useSession.getState().token?.session ?? '';
    const toast = (e: unknown) =>
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');

    if (!hosted) {
      return {
        hosted: false,
        togglePause: async () => {
          const r = await mpv.send(['cycle', 'pause']);
          if (!r.ok) useSession.getState().toast(r.error ?? 'the projector refused that', 'warn');
        },
        setPaused: async (paused) => {
          await mpv.send(['set', 'pause', paused]);
        },
        seek: async (ms, relative) => {
          await mpv.send(['seek', ms / 1000, relative ? 'relative' : 'absolute']);
        },
        load: async (path, mode) => {
          const r = await mpv.send(['vs/load', path, mode]);
          if (!r.ok) useSession.getState().toast(`Load failed: ${r.error ?? 'unknown error'}`, 'error');
        },
        // Nothing holds on the live path; there is no buffer to wait for.
        start: async () => undefined,
      };
    }

    const send = async (body: Parameters<typeof api.playback>[1]) => {
      try {
        await api.playback(session(), body);
      } catch (e) {
        toast(e);
      }
    };
    return {
      hosted: true,
      togglePause: () => send({ action: 'toggle' }),
      setPaused: (paused) => send({ action: paused ? 'pause' : 'play' }),
      seek: (ms, relative) => send({ action: 'seek', posMs: Math.round(ms), relative }),
      load: (mediaId, mode) => send({ action: mode === 'append' ? 'enqueue' : 'load', mediaId }),
      start: () => send({ action: 'start' }),
    };
  }, [hosted, mpv]);
}
