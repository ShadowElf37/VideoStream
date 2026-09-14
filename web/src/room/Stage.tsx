import { useRoomContext } from '@livekit/components-react';
import { useCallback, useRef, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { cn } from '@/lib/cn';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { HostBar } from './HostBar';
import { MovieVideo } from './MovieVideo';
import { BufferingGlyph, HoldingCard, PauseRequestBanner, PausedGlyph, QualityGlyph, ReactionsLayer, SpeakingChips, Toasts, WaitingState } from './Overlays';
import { StatsOverlay } from './StatsOverlay';
import { ViewerBar } from './ViewerBar';
import { HostedMovie, type HostedStatus } from '@/movie/HostedMovie';
import { usePlayback } from '@/movie/usePlayback';
import { useTransport } from '@/movie/useTransport';
import { useAutoHide } from './hooks';
import { useMovieTracks, useSmoothness } from './useMovieTracks';

export function Stage({
  onOpenLibrary,
  onToggleFullscreen,
  videoRef,
}: {
  onOpenLibrary: () => void;
  onToggleFullscreen: () => void;
  videoRef: React.RefObject<HTMLVideoElement | null>;
}) {
  const room = useRoomContext();
  const mpv = useMpv();
  const role = useSession((s) => s.role);
  const statsOverlay = usePrefs((s) => s.statsOverlay);
  const projectorMode = usePrefs((s) => s.projectorMode);
  const state = useMpvStore((s) => s.state);
  const projectorOnline = useMpvStore((s) => s.projectorOnline);
  const movie = useMovieTracks();
  useSmoothness(room, movie);

  // Two ways a film can reach this room, and they are mutually exclusive.
  //
  // Hosted is the primary one: a file on the server, fetched over HTTPS, with
  // the browser owning a real buffer and the director owning playback time.
  // Live is the desktop projector publishing RTP, where the client cannot
  // seek or buffer ahead and the picture necessarily trails the projector.
  //
  // Hosted wins when something is loaded, because a room cannot be watching
  // two films at once and the server's answer is the authoritative one.
  const connected = useSession((s) => s.phase) === 'connected';
  const playback = usePlayback(room, connected);
  const hosted = !!playback.state && !playback.state.idle && !!playback.state.url;
  const transport = useTransport(hosted);

  const [stalled, setStalled] = useState(false);
  const onStalled = useCallback((v: boolean) => setStalled(v), []);
  // Stable, because HostedMovie's control loop lists it as a dependency: an
  // inline lambda here tore the 250 ms interval down and rebuilt it on every
  // render of this component.
  const onHostedStatus = useCallback((st: HostedStatus) => setStalled(st.buffering), []);
  const stageRef = useRef<HTMLDivElement>(null);
  const hasMovie = hosted || !!movie.video;
  // Nothing to obscure without a picture, so keep the bar (and its "Open…") up.
  // Scoped to the stage: the bar belongs to the player, not the page.
  const bar = useAutoHide(1600, hasMovie, stageRef);
  const isHost = role === 'host';

  // mpv-style keys while the stage has focus (host only).
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!isHost || e.metaKey || e.ctrlKey || e.altKey) return;
    const send = (cmd: unknown[]) => void mpv.send(cmd);
    const speed = state?.speed ?? 1;
    // In hosted mode the transport keys go to the director; the rest (track
    // switching, subtitle delay) are mpv-only and simply do nothing, which is
    // honest — a burned-in subtitle has no delay to adjust.
    if (hosted) {
      switch (e.key) {
        case ' ':
          e.preventDefault();
          void transport.togglePause();
          return;
        case 'ArrowLeft':
          e.preventDefault();
          void transport.seek(-5000, true);
          return;
        case 'ArrowRight':
          e.preventDefault();
          void transport.seek(5000, true);
          return;
        case 'ArrowUp':
          e.preventDefault();
          void transport.seek(60_000, true);
          return;
        case 'ArrowDown':
          e.preventDefault();
          void transport.seek(-60_000, true);
          return;
      }
    }
    switch (e.key) {
      case ' ':
        send(['cycle', 'pause']);
        break;
      case 'ArrowLeft':
        send(['seek', -5, 'relative']);
        break;
      case 'ArrowRight':
        send(['seek', 5, 'relative']);
        break;
      case 'ArrowUp':
        send(['seek', 60, 'relative']);
        break;
      case 'ArrowDown':
        send(['seek', -60, 'relative']);
        break;
      case '[':
        send(['set_property', 'speed', Math.max(0.25, +(speed * 0.9).toFixed(2))]);
        break;
      case ']':
        send(['set_property', 'speed', Math.min(4, +(speed * 1.1).toFixed(2))]);
        break;
      case 'j':
        send(['cycle', 'sid']);
        break;
      case '#':
        send(['cycle', 'aid']);
        break;
      case 'z':
        send(['add', 'sub-delay', -0.1]);
        break;
      case 'x':
        send(['add', 'sub-delay', 0.1]);
        break;
      default:
        return;
    }
    e.preventDefault();
    e.stopPropagation();
  };

  // Whichever player is live decides what "paused" means. In hosted mode the
  // director's word is final; in live mode it is mpv's.
  const paused = hosted
    ? !!playback.state?.paused
    : !!state && !state.idle && state.pause;
  // Holding is a kind of paused, but not the kind anyone pressed: it gets the
  // card that says who the room is waiting for, instead of the red glyph.
  const holding = hosted && !!playback.state?.holding;

  return (
    <div
      ref={stageRef}
      tabIndex={0}
      aria-label="Stage"
      className={cn('relative h-full w-full bg-[#0b0b0d] outline-none overflow-hidden', !bar.visible && hasMovie && 'cursor-none')}
      onKeyDown={onKeyDown}
      onDoubleClick={(e) => {
        if ((e.target as HTMLElement).closest('button, [role=slider]')) return;
        onToggleFullscreen();
      }}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest('button, [role=slider], a, input')) return;
        stageRef.current?.focus({ preventScroll: true });
      }}
    >
      {hosted && playback.state && playback.offsetMs !== null ? (
        <HostedMovie
          state={playback.state}
          offsetMs={playback.offsetMs}
          videoRef={videoRef}
          onStatus={onHostedStatus}
        />
      ) : (
        <MovieVideo track={movie.video} paused={paused} onStalled={onStalled} videoRef={videoRef} />
      )}

      {!hasMovie && <WaitingState projectorOnline={projectorOnline} projectorMode={projectorMode} isHost={isHost} onOpenLibrary={onOpenLibrary} />}
      {hasMovie && paused && !stalled && !holding && <PausedGlyph />}
      {hasMovie && stalled && !paused && <BufferingGlyph />}
      {holding && <HoldingCard names={playback.state?.waitingFor ?? []} isHost={isHost} onStart={() => void transport.start()} />}

      <PauseRequestBanner />
      <QualityGlyph />
      {statsOverlay && <StatsOverlay movie={movie} />}
      <Toasts />
      <SpeakingChips />
      <ReactionsLayer />

      {isHost ? <HostBar visible={bar.visible} onPin={bar.pin} onOpenLibrary={onOpenLibrary} videoRef={videoRef} /> : <ViewerBar visible={bar.visible} videoRef={videoRef} />}
    </div>
  );
}
