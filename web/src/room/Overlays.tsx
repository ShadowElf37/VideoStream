import { useLocalParticipant, useParticipants } from '@livekit/components-react';
import { ConnectionQuality, type Participant } from 'livekit-client';
import { Clapperboard, Pause, Wifi, WifiOff } from 'lucide-react';
import { useEffect } from 'react';
import { cn } from '@/lib/cn';
import { colorFor } from '@/lib/colors';
import { useSession, type Reaction, type Toast } from '@/state/session';
import { Avatar } from '@/ui/Avatar';
import { Spinner } from '@/ui/Spinner';
import { Tooltip } from '@/ui/Tooltip';
import { useParticipantLive } from './hooks';
import { displayName, isProjector, parseMetadata } from './identity';

// Floating emoji reactions -------------------------------------------------

export function ReactionsLayer() {
  const reactions = useSession((s) => s.reactions);
  return (
    <div className="pointer-events-none absolute bottom-16 right-4 w-40 h-64 overflow-visible" aria-live="polite">
      {reactions.map((r) => (
        <FloatingReaction key={r.id} r={r} />
      ))}
    </div>
  );
}

function FloatingReaction({ r }: { r: Reaction }) {
  const remove = useSession((s) => s.removeReaction);
  useEffect(() => {
    const t = setTimeout(() => remove(r.id), 2600);
    return () => clearTimeout(t);
  }, [r.id, remove]);
  return (
    <span className="anim-float-up absolute bottom-0 flex flex-col items-center gap-0.5" style={{ right: `${r.x}%` }}>
      <span className="text-4xl drop-shadow-[0_2px_8px_rgba(0,0,0,0.6)]">{r.emoji}</span>
      <span className="text-[10px] text-white/80 bg-black/40 rounded px-1">{r.from}</span>
    </span>
  );
}

// Speaking chips -----------------------------------------------------------

export function SpeakingChips() {
  const participants = useParticipants();
  return (
    <div className="pointer-events-none absolute bottom-16 left-4 flex flex-col-reverse gap-1.5">
      {participants.filter((p) => !isProjector(p)).map((p) => (
        <SpeakingChip key={p.identity} p={p} />
      ))}
    </div>
  );
}

function SpeakingChip({ p }: { p: Participant }) {
  const { speaking } = useParticipantLive(p);
  if (!speaking) return null;
  const color = parseMetadata(p.metadata).color ?? colorFor(p.identity);
  return (
    <div className="anim-fade-in flex items-center gap-2 pr-3 pl-1 py-1 rounded-full bg-black/55 backdrop-blur-md text-white text-[12px] font-medium">
      <Avatar name={displayName(p)} color={color} size={22} speaking />
      {displayName(p)}
      {p.isLocal && <span className="text-white/60">(you)</span>}
    </div>
  );
}

// Toasts -------------------------------------------------------------------

export function Toasts() {
  const toasts = useSession((s) => s.toasts);
  return (
    <div className="pointer-events-none absolute top-4 left-1/2 -translate-x-1/2 z-30 flex flex-col items-center gap-2 w-[min(90%,520px)]">
      {toasts.map((t) => (
        <ToastItem key={t.id} t={t} />
      ))}
    </div>
  );
}

function ToastItem({ t }: { t: Toast }) {
  const dismiss = useSession((s) => s.dismissToast);
  useEffect(() => {
    const h = setTimeout(() => dismiss(t.id), t.ttl ?? 4000);
    return () => clearTimeout(h);
  }, [t.id, t.ttl, dismiss]);
  return (
    <div
      className={cn(
        'anim-fade-in glass-strong rounded-xl px-4 py-2 text-[13px] text-text max-w-full truncate',
        t.kind === 'warn' && 'border-warn/40',
        t.kind === 'error' && 'border-danger/40 text-danger',
      )}
    >
      {t.text}
    </div>
  );
}

// Centered glyphs ------------------------------------------------------------

export function PausedGlyph() {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
      <div className="anim-pop size-20 rounded-full bg-black/45 backdrop-blur-md flex items-center justify-center text-white">
        <Pause className="size-9" fill="currentColor" />
      </div>
    </div>
  );
}

export function BufferingGlyph() {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
      <div className="size-16 rounded-full bg-black/40 backdrop-blur-md flex items-center justify-center">
        <Spinner size={30} />
      </div>
    </div>
  );
}

export function WaitingState({ projectorOnline }: { projectorOnline: boolean }) {
  return (
    <div className="absolute inset-0 flex items-center justify-center p-4 sm:p-6">
      <div className="anim-fade-in max-w-md text-center">
        <div className="mx-auto size-12 sm:size-16 rounded-2xl glass flex items-center justify-center text-accent mb-3 sm:mb-4">
          <Clapperboard className="size-6 sm:size-8" />
        </div>
        <h2 className="text-base sm:text-lg font-semibold tracking-tight">{projectorOnline ? 'Projector is idle' : 'Waiting for the projector…'}</h2>
        {/* The how-to is desktop-only: a phone stage is too short for it and phones never run the projector. */}
        <div className="hidden sm:block">
          <p className="text-muted mt-1.5 text-sm">
            {projectorOnline
              ? 'It is connected but nothing is loaded yet. The host can pick a file from the Queue tab.'
              : 'On the machine with the files, start the projector with the projector link:'}
          </p>
          {!projectorOnline && (
            <pre className="mt-3 text-left text-[12px] font-mono glass rounded-xl px-3 py-2.5 whitespace-pre-wrap break-all">
              projector --room &quot;https://…/r/&lt;id&gt;?p=&lt;projectorKey&gt;&quot; ~/Movies/film.mkv
            </pre>
          )}
          <p className="text-muted mt-3 text-xs">Voice chat works meanwhile.</p>
        </div>
      </div>
    </div>
  );
}

// Connection quality glyph ---------------------------------------------------

export function QualityGlyph() {
  const { localParticipant } = useLocalParticipant();
  const { quality } = useParticipantLive(localParticipant);
  const map: Record<ConnectionQuality, { label: string; cls: string }> = {
    [ConnectionQuality.Excellent]: { label: 'Connection: excellent', cls: 'text-ok' },
    [ConnectionQuality.Good]: { label: 'Connection: good', cls: 'text-accent' },
    [ConnectionQuality.Poor]: { label: 'Connection: poor', cls: 'text-danger' },
    [ConnectionQuality.Lost]: { label: 'Connection lost', cls: 'text-danger' },
    [ConnectionQuality.Unknown]: { label: 'Connection: measuring…', cls: 'text-white/50' },
  };
  const q = map[quality] ?? map[ConnectionQuality.Unknown];
  return (
    <div className="absolute top-3 right-3 z-20">
      <Tooltip label={q.label} side="left">
        <span className={cn('inline-flex size-8 items-center justify-center rounded-lg bg-black/35 backdrop-blur-md', q.cls)}>
          {quality === ConnectionQuality.Lost ? <WifiOff className="size-4" /> : <Wifi className="size-4" />}
        </span>
      </Tooltip>
    </div>
  );
}
