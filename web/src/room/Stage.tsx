import { useRoomContext } from '@livekit/components-react';
import { useCallback, useRef, useState } from 'react';
import { useMpvStore } from '@/host/useMpv';
import { cn } from '@/lib/cn';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { HostBar } from './HostBar';
import { MovieVideo } from './MovieVideo';
import { BufferingGlyph, HoldingCard, IntentEcho, PauseRequestBanner, PausedGlyph, QualityGlyph, ReactionsLayer, SpeakingChips, Toasts, WaitingState } from './Overlays';
import { StatsOverlay } from './StatsOverlay';
import { ViewerBar } from './ViewerBar';
import { HostedMovie, type HostedStatus } from '@/movie/HostedMovie';
import { useIntentStore } from '@/movie/intent';
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
  const onHostedStatus = useCallback((st: HostedStatus) => {
    setStalled(st.buffering);
    // The loading card comes down when this machine actually has a picture,
    // not when the command landed — which is the difference between a card
    // that means something and a 200 ms flash.
    if (!st.buffering) useIntentStore.getState().markStarted();
  }, []);
  const stageRef = useRef<HTMLDivElement>(null);
  const hasMovie = hosted || !!movie.video;
  // Nothing to obscure without a picture, so keep the bar (and its "Open…") up.
  // Scoped to the stage: the bar belongs to the player, not the page.
  const bar = useAutoHide(1600, hasMovie, stageRef);
  const isHost = role === 'host';

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
      className={cn('relative h-full w-full bg-[#0b0b0d] overflow-hidden', !bar.visible && hasMovie && 'cursor-none')}
      onDoubleClick={(e) => {
        if ((e.target as HTMLElement).closest('button, [role=slider]')) return;
        onToggleFullscreen();
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
      {holding ? (
        <HoldingCard names={playback.state?.waitingFor ?? []} isHost={isHost} onStart={() => void transport.start()} />
      ) : (
        hosted && <IntentEcho />
      )}

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
