import { useMemo } from 'react';
import { useAudio } from '@/audio/useAudioModel';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { usePlaybackStore } from '@/movie/store';
import { useTransport } from '@/movie/useTransport';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { useGlobalKeys } from './useKeyboard';
import { useRequestPause } from './useRequestPause';

/**
 * The room's keymap, mounted where it can reach both players.
 *
 * Keys used to split by focus: the dock's on the window, the transport's on
 * the stage element. Click Play in the library and Space re-pressed that
 * button instead of pausing, because the stage no longer had focus. There is
 * no focus now, only the room: every key means the same thing wherever the
 * last click landed, and only a dialog, a menu or a text field takes it away.
 */
export function RoomKeys({
  enabled,
  toggleReactions,
  toggleFullscreen,
  openHelp,
}: {
  enabled: boolean;
  toggleReactions: () => void;
  toggleFullscreen: () => void;
  openHelp: () => void;
}) {
  const { dispatch } = useAudio();
  const setPref = usePrefs((s) => s.set);
  const isHost = useSession((s) => s.role) === 'host';
  const hosted = usePlaybackStore((s) => !!s.state && !s.state.idle && !!s.state.url);
  const liveIdle = useMpvStore((s) => s.state?.idle ?? true);
  const projectorOnline = useMpvStore((s) => s.projectorOnline);
  const speed = useMpvStore((s) => s.state?.speed ?? 1);
  const transport = useTransport(hosted);
  const mpv = useMpv();
  const { request: requestPause } = useRequestPause();

  const keys = useMemo(() => {
    // Nothing loaded anywhere: the transport keys have no film to act on,
    // and sending them anyway only produces a toast about it.
    const controllable = hosted || (projectorOnline && !liveIdle);
    // mpv-only: track switching and subtitle delay have no meaning for a
    // file the server is playing, so on that path they do nothing.
    const live = (cmd: unknown[]) => {
      if (isHost && !hosted && controllable) void mpv.send(cmd);
    };
    return {
      toggleMic: () => dispatch({ type: 'toggleMic' }),
      toggleDeafen: () => dispatch({ type: 'toggleDeafen' }),
      toggleMovieMuted: () => dispatch({ type: 'toggleMovieMuted' }),
      toggleReactions,
      toggleFullscreen,
      toggleSidebar: () => setPref('sidebarOpen', !usePrefs.getState().sidebarOpen),
      pttDown: () => dispatch({ type: 'pttDown' }),
      pttUp: () => dispatch({ type: 'pttUp' }),
      openHelp,
      playPause: () => {
        if (!controllable) return;
        if (isHost) void transport.togglePause();
        else void requestPause();
      },
      seek: (ms: number) => {
        if (isHost && controllable) void transport.seek(ms, true);
      },
      speedStep: (dir: 1 | -1) =>
        live(['set_property', 'speed', dir > 0 ? Math.min(4, +(speed * 1.1).toFixed(2)) : Math.max(0.25, +(speed * 0.9).toFixed(2))]),
      cycleSubs: () => live(['cycle', 'sid']),
      cycleAudio: () => live(['cycle', 'aid']),
      subDelay: (s: number) => live(['add', 'sub-delay', s]),
    };
  }, [dispatch, setPref, isHost, hosted, liveIdle, projectorOnline, speed, transport, mpv, requestPause, toggleReactions, toggleFullscreen, openHelp]);

  useGlobalKeys(keys, enabled);
  return null;
}
