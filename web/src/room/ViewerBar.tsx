import { Hand } from 'lucide-react';
import { useRoomContext } from '@livekit/components-react';
import { useState } from 'react';
import { useMpv, useMpvStore, useSmoothTimePos } from '@/host/useMpv';
import { cn } from '@/lib/cn';
import { publish } from '@/lib/data';
import { formatTime } from '@/lib/format';
import { Topics } from '@/proto/messages';
import { useSession } from '@/state/session';
import { SeekBar } from './SeekBar';

/** Read-only progress for viewers plus "Request pause". */
export function ViewerBar({ visible }: { visible: boolean }) {
  const room = useRoomContext();
  const mpv = useMpv();
  const state = useMpvStore((s) => s.state);
  const settings = useSession((s) => s.settings);
  const pos = useSmoothTimePos();
  const [cooldown, setCooldown] = useState(false);

  const requestPause = async () => {
    if (cooldown) return;
    setCooldown(true);
    setTimeout(() => setCooldown(false), 4000);
    await publish(room, Topics.react, { emoji: '⏸️' }, { reliable: false });
    useSession.getState().addReaction('⏸️', 'you');
    if (settings?.anyoneCanPause) {
      const r = await mpv.send(['cycle', 'pause']);
      if (!r.ok) useSession.getState().toast('The projector ignored that (host-only)', 'warn');
    } else {
      useSession.getState().toast('Asked the host to pause', 'info', 2500);
    }
  };

  if (!state || state.idle) return null;

  return (
    <div
      className={cn(
        'absolute inset-x-0 bottom-0 z-20 px-4 pb-3 pt-10 bg-gradient-to-t from-black/70 via-black/30 to-transparent transition-opacity duration-200 ease-out',
        visible ? 'opacity-100' : 'opacity-0 pointer-events-none',
      )}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="max-w-[1400px] mx-auto">
        <SeekBar position={pos} duration={state.duration} chapters={state.chapters} />
        <div className="mt-1 flex items-center gap-3">
          <span className="font-mono text-[12px] text-white/90 tabular-nums">
            {formatTime(pos)} <span className="text-white/50">/ {formatTime(state.duration)}</span>
          </span>
          {state.chapters[state.chapter]?.title && (
            <span className="text-[12px] text-white/70 truncate">{state.chapters[state.chapter]?.title}</span>
          )}
          <span className="flex-1" />
          <button
            onClick={() => void requestPause()}
            disabled={cooldown}
            className="h-8 px-3 rounded-lg text-[12px] font-medium bg-white/10 hover:bg-white/20 text-white inline-flex items-center gap-1.5 disabled:opacity-50"
          >
            <Hand className="size-4" /> {settings?.anyoneCanPause ? (state.pause ? 'Resume' : 'Pause') : 'Request pause'}
          </button>
        </div>
      </div>
    </div>
  );
}
