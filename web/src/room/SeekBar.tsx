import { useCallback, useRef, useState } from 'react';
import { cn } from '@/lib/cn';
import { clamp, formatTime } from '@/lib/format';
import type { MpvChapter } from '@/proto/messages';

/**
 * mpv-style seek bar with chapter ticks and hover time. Read-only when no
 * `onSeek` is given (viewers). Pointer-drag scrubs; the seek fires on release.
 */
export function SeekBar({
  position,
  duration,
  chapters,
  onSeek,
  className,
}: {
  position: number;
  duration: number;
  chapters: MpvChapter[];
  onSeek?: (seconds: number) => void;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const [drag, setDrag] = useState<number | null>(null);
  const interactive = !!onSeek && duration > 0;
  const shown = drag ?? position;
  const pct = duration > 0 ? clamp(shown / duration, 0, 1) * 100 : 0;

  const secondsAt = useCallback(
    (clientX: number) => {
      const el = ref.current;
      if (!el || duration <= 0) return 0;
      const r = el.getBoundingClientRect();
      return clamp((clientX - r.left) / r.width, 0, 1) * duration;
    },
    [duration],
  );

  const hoverChapter = hover !== null ? [...chapters].reverse().find((c) => c.time <= hover) : undefined;

  return (
    <div
      ref={ref}
      role={interactive ? 'slider' : 'progressbar'}
      aria-label="Playback position"
      aria-valuemin={0}
      aria-valuemax={Math.round(duration)}
      aria-valuenow={Math.round(shown)}
      aria-valuetext={`${formatTime(shown)} of ${formatTime(duration)}`}
      tabIndex={interactive ? 0 : -1}
      className={cn('group relative h-6 w-full select-none touch-none', interactive ? 'cursor-pointer' : 'cursor-default', className)}
      onPointerMove={(e) => setHover(secondsAt(e.clientX))}
      onPointerLeave={() => setHover(null)}
      onPointerDown={(e) => {
        if (!interactive) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        setDrag(secondsAt(e.clientX));
      }}
      onPointerUp={(e) => {
        if (!interactive || drag === null) return;
        e.currentTarget.releasePointerCapture(e.pointerId);
        const s = secondsAt(e.clientX);
        setDrag(null);
        onSeek?.(s);
      }}
      onPointerMoveCapture={(e) => {
        if (drag !== null) setDrag(secondsAt(e.clientX));
      }}
      onKeyDown={(e) => {
        if (!interactive) return;
        const step = e.shiftKey ? 60 : 5;
        if (e.key === 'ArrowLeft') onSeek?.(clamp(position - step, 0, duration));
        else if (e.key === 'ArrowRight') onSeek?.(clamp(position + step, 0, duration));
        else return;
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <div className="absolute left-0 right-0 top-1/2 -translate-y-1/2 h-1 rounded-full bg-white/20 group-hover:h-1.5 transition-[height] duration-150">
        <div className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${pct}%` }} />
        {hover !== null && duration > 0 && (
          <div className="absolute inset-y-0 left-0 rounded-full bg-white/25" style={{ width: `${(hover / duration) * 100}%` }} />
        )}
        {duration > 0 &&
          chapters.map((c, i) =>
            c.time > 0 && c.time < duration ? (
              <span
                key={i}
                className="absolute top-1/2 -translate-y-1/2 h-2.5 w-0.5 rounded bg-white/70"
                style={{ left: `${(c.time / duration) * 100}%` }}
              />
            ) : null,
          )}
      </div>
      <div
        className={cn(
          'absolute top-1/2 -translate-y-1/2 -translate-x-1/2 size-3 rounded-full bg-accent shadow transition-opacity duration-150',
          interactive ? 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100' : 'opacity-0',
          drag !== null && 'opacity-100 scale-110',
        )}
        style={{ left: `${pct}%` }}
      />
      {hover !== null && duration > 0 && (
        <div
          className="pointer-events-none absolute -top-9 -translate-x-1/2 glass-strong rounded-md px-2 py-1 text-[11px] font-mono whitespace-nowrap"
          style={{ left: `${clamp((hover / duration) * 100, 4, 96)}%` }}
        >
          {formatTime(hover)}
          {hoverChapter?.title ? <span className="ml-1.5 font-sans text-muted">{hoverChapter.title}</span> : null}
        </div>
      )}
    </div>
  );
}
