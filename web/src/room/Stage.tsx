import { useRoomContext } from '@livekit/components-react';
import { useCallback, useRef, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { cn } from '@/lib/cn';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { HostBar } from './HostBar';
import { MovieVideo } from './MovieVideo';
import { BufferingGlyph, PauseRequestBanner, PausedGlyph, QualityGlyph, ReactionsLayer, SpeakingChips, Toasts, WaitingState } from './Overlays';
import { StatsOverlay } from './StatsOverlay';
import { ViewerBar } from './ViewerBar';
import { useAutoHide } from './hooks';
import { useMovieTracks, useQualityPreference, useSmoothness } from './useMovieTracks';

export function Stage({
  onOpenQueue,
  onToggleFullscreen,
  videoRef,
}: {
  onOpenQueue: () => void;
  onToggleFullscreen: () => void;
  videoRef: React.RefObject<HTMLVideoElement | null>;
}) {
  const room = useRoomContext();
  const mpv = useMpv();
  const role = useSession((s) => s.role);
  const statsOverlay = usePrefs((s) => s.statsOverlay);
  const state = useMpvStore((s) => s.state);
  const projectorOnline = useMpvStore((s) => s.projectorOnline);
  const movie = useMovieTracks();
  useSmoothness(room, movie);
  useQualityPreference(movie);

  const [stalled, setStalled] = useState(false);
  const onStalled = useCallback((v: boolean) => setStalled(v), []);
  const stageRef = useRef<HTMLDivElement>(null);
  const hasMovie = !!movie.video;
  // Nothing to obscure without a picture, so keep the bar (and its "Open…") up.
  const bar = useAutoHide(2500, hasMovie);
  const isHost = role === 'host';

  // mpv-style keys while the stage has focus (host only).
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!isHost || e.metaKey || e.ctrlKey || e.altKey) return;
    const send = (cmd: unknown[]) => void mpv.send(cmd);
    const speed = state?.speed ?? 1;
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

  const paused = !!state && !state.idle && state.pause;

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
      <MovieVideo track={movie.video} onStalled={onStalled} videoRef={videoRef} />

      {!hasMovie && <WaitingState projectorOnline={projectorOnline} />}
      {hasMovie && paused && !stalled && <PausedGlyph />}
      {hasMovie && stalled && !paused && <BufferingGlyph />}

      <PauseRequestBanner />
      <QualityGlyph />
      {statsOverlay && <StatsOverlay movie={movie} />}
      <Toasts />
      <SpeakingChips />
      <ReactionsLayer />

      {isHost ? <HostBar visible={bar.visible} onPin={bar.pin} onOpenQueue={onOpenQueue} /> : <ViewerBar visible={bar.visible} />}
    </div>
  );
}
