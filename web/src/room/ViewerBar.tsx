import { Hand } from 'lucide-react';
import { useMpvStore } from '@/host/useMpv';
import { useNowPlaying } from '@/movie/store';
import { useBufferedRanges } from '@/movie/useBufferedRanges';
import { useRoomLag } from '@/movie/useRoomLag';
import { cn } from '@/lib/cn';
import { formatTime } from '@/lib/format';
import { useSession } from '@/state/session';
import { HostedQuality } from './HostBar';
import { SeekBar } from './SeekBar';
import { useRequestPause } from './useRequestPause';

/** Read-only progress for viewers plus "Request pause". */
export function ViewerBar({ visible, videoRef }: { visible: boolean; videoRef: React.RefObject<HTMLVideoElement | null> }) {
  const settings = useSession((s) => s.settings);
  const { request: requestPause, cooldown } = useRequestPause();

  // Whichever player owns the room; reading mpv alone showed 0:00 over a
  // perfectly good server-hosted film.
  const liveState = useMpvStore((s) => s.state);
  const now = useNowPlaying();
  const buffered = useBufferedRanges(videoRef, now.hosted);
  const lag = useRoomLag(now.hosted);
  const pos = now.position;

  if (now.idle) return null;

  return (
    <div
      className={cn(
        'absolute inset-x-0 bottom-0 z-20 px-4 pb-3 pt-10 bg-gradient-to-t from-black/70 via-black/30 to-transparent transition-opacity duration-200 ease-out',
        visible ? 'opacity-100' : 'opacity-0 pointer-events-none',
      )}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="max-w-[1400px] mx-auto">
        <SeekBar
          position={lag.position ?? pos}
          roomPosition={pos}
          pending={lag.pending}
          duration={now.duration}
          chapters={liveState?.chapters ?? []}
          buffered={buffered}
        />
        <div className="mt-1 flex items-center gap-3">
          <span className="font-mono text-[12px] text-white/90 tabular-nums">
            {formatTime(pos)} <span className="text-white/50">/ {formatTime(now.duration)}</span>
          </span>
          {now.title && <span className="text-[12px] text-white/70 truncate">{now.title}</span>}
          <span className="flex-1" />
          <HostedQuality />
          <button
            onClick={() => void requestPause()}
            disabled={cooldown}
            className="h-8 px-3 rounded-lg text-[12px] font-medium bg-white/10 hover:bg-white/20 text-white inline-flex items-center gap-1.5 disabled:opacity-50"
          >
            <Hand className="size-4" /> {settings?.anyoneCanPause ? (now.paused ? 'Resume' : 'Pause') : 'Request pause'}
          </button>
        </div>
      </div>
    </div>
  );
}
