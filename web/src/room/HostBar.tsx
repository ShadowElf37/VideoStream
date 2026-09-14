import {
  Captions,
  CaptionsOff,
  ChevronLeft,
  ChevronRight,
  FastForward,
  FolderOpen,
  Gauge,
  Languages,
  Pause,
  Play,
  Rewind,
  Volume2,
} from 'lucide-react';
import { useRef, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { useNowPlaying } from '@/movie/store';
import { useTransport } from '@/movie/useTransport';
import { useBufferedRanges } from '@/movie/useBufferedRanges';
import { cn } from '@/lib/cn';
import { formatDelay, formatTime, trackLabel } from '@/lib/format';
import type { QualityPreset } from '@/proto/messages';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { IconButton } from '@/ui/IconButton';
import { Menu, MenuItem, MenuLabel, MenuSeparator } from '@/ui/Menu';
import { Popover } from '@/ui/Popover';
import { Slider } from '@/ui/Slider';
import { SeekBar } from './SeekBar';

const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];
export const PRESETS: QualityPreset[] = ['1080p-high', '1080p', '720p', '540p'];
const PRESET_LABEL: Record<QualityPreset, string> = {
  '1080p-high': '1080p · 8 Mbps',
  '1080p': '1080p · 5 Mbps',
  '720p': '720p · 3 Mbps',
  '540p': '540p · 1.5 Mbps',
};

export function allowedPresets(max: QualityPreset | undefined): QualityPreset[] {
  const i = max ? PRESETS.indexOf(max) : 0;
  return PRESETS.slice(Math.max(0, i));
}

/** Host-only transport bar. Parent controls visibility; `onPin` keeps it open while a menu is up. */
export function HostBar({
  visible,
  onPin,
  onOpenQueue,
  videoRef,
}: {
  visible: boolean;
  onPin: (v: boolean) => void;
  onOpenQueue: () => void;
  videoRef: React.RefObject<HTMLVideoElement | null>;
}) {
  const mpv = useMpv();
  const state = useMpvStore((s) => s.state);
  const settings = useSession((s) => s.settings);
  const [busy, setBusy] = useState<string | null>(null);

  // Whichever player owns the room answers for position, duration and pause;
  // the transport routes commands to the same one. Reading mpv unconditionally
  // was how this bar came up greyed out at 0:00 over a film that was playing
  // perfectly — there is no mpv in server-hosted mode.
  const now = useNowPlaying();
  const transport = useTransport(now.hosted);
  // Only hosted media has an addressable buffer to draw.
  const buffered = useBufferedRanges(videoRef, now.hosted);

  const send = async (cmd: unknown[], label?: string) => {
    if (label) setBusy(label);
    const r = await mpv.send(cmd);
    if (label) setBusy(null);
    if (!r.ok) useSession.getState().toast(`mpv: ${r.error ?? 'command failed'}`, 'error');
    return r;
  };

  const pos = now.position;
  const duration = now.duration;
  const paused = now.paused;
  const idle = now.idle;
  const subs = state?.tracks.filter((t) => t.type === 'sub') ?? [];
  const audios = state?.tracks.filter((t) => t.type === 'audio') ?? [];
  const activeSub = subs.find((t) => t.selected);
  const activeAudio = audios.find((t) => t.selected);
  const presets = allowedPresets(settings?.maxPreset);
  const disabled = !now.controllable;
  // A pushed file has one audio track, burned-in subtitles and one bitrate:
  // the tracks were chosen and the quality fixed when it was encoded. These
  // controls exist only for the live projector, where mpv can still change
  // them mid-playback.
  const live = !now.hosted;

  return (
    <div
      className={cn(
        'absolute inset-x-0 bottom-0 z-20 px-4 pb-3 pt-10 bg-gradient-to-t from-black/75 via-black/35 to-transparent transition-opacity duration-200 ease-out',
        visible ? 'opacity-100' : 'opacity-0 pointer-events-none',
      )}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="max-w-[1400px] mx-auto">
        <SeekBar position={pos} duration={duration} chapters={state?.chapters ?? []} onSeek={(s) => void transport.seek(s * 1000, false)} buffered={buffered} />
        {/* One scrollable row on phones; wraps into two rows from tablet width up. */}
        <div className="mt-1 flex items-center gap-1 flex-nowrap overflow-x-auto [&>*]:shrink-0 sm:flex-wrap sm:overflow-visible sm:[&>*]:shrink">
          <IconButton label={paused ? 'Play' : 'Pause'} kbd="Space" size="md" disabled={disabled || idle} onClick={() => void transport.togglePause()}>
            {paused ? <Play /> : <Pause />}
          </IconButton>
          <IconButton label="Back 60 s" kbd="↓" size="sm" disabled={disabled || idle} onClick={() => void transport.seek(-60_000, true)}>
            <Rewind />
          </IconButton>
          <SeekChip label="−10" onClick={() => void transport.seek(-10_000, true)} disabled={disabled || idle} />
          <SeekChip label="+10" onClick={() => void transport.seek(10_000, true)} disabled={disabled || idle} />
          <IconButton label="Forward 60 s" kbd="↑" size="sm" disabled={disabled || idle} onClick={() => void transport.seek(60_000, true)}>
            <FastForward />
          </IconButton>

          <span className="ml-2 font-mono text-[12px] text-white/90 tabular-nums">
            {formatTime(pos)} <span className="text-white/50">/ {formatTime(duration)}</span>
          </span>

          {(state?.chapters.length ?? 0) > 0 && (
            <span className="ml-1 inline-flex items-center gap-0.5">
              <IconButton label="Previous chapter" size="sm" disabled={disabled} onClick={() => void send(['add', 'chapter', -1])}>
                <ChevronLeft />
              </IconButton>
              <span className="text-[12px] text-white/70 max-w-[180px] truncate">
                {state?.chapters[state.chapter]?.title ?? `Chapter ${(state?.chapter ?? 0) + 1}`}
              </span>
              <IconButton label="Next chapter" size="sm" disabled={disabled} onClick={() => void send(['add', 'chapter', 1])}>
                <ChevronRight />
              </IconButton>
            </span>
          )}

          <span className="flex-1" />

          {/* Speed, audio, subtitles, mpv volume and the encoder preset are all
              live-projector controls: they change what mpv is doing right now.
              A pushed file has none of those knobs left. */}
          {live && (
            <>
          {/* Speed */}
          <Menu
            onOpenChange={onPin}
            trigger={
              <button className="h-8 px-2.5 rounded-lg text-[12px] font-mono text-white/90 hover:bg-white/10 inline-flex items-center gap-1.5" disabled={disabled}>
                <Gauge className="size-4" />
                {(state?.speed ?? 1).toFixed(2).replace(/\.?0+$/, '')}×
              </button>
            }
          >
            <MenuLabel>Speed</MenuLabel>
            {SPEEDS.map((sp) => (
              <MenuItem key={sp} selected={Math.abs((state?.speed ?? 1) - sp) < 0.01} onSelect={() => void send(['set_property', 'speed', sp])}>
                {sp}×
              </MenuItem>
            ))}
          </Menu>

          {/* Audio track + delay */}
          <Popover
            onOpenChange={onPin}
            trigger={
              <button className="h-8 px-2.5 rounded-lg text-[12px] text-white/90 hover:bg-white/10 inline-flex items-center gap-1.5" disabled={disabled}>
                <Languages className="size-4" />
                <span className="max-w-[140px] truncate">{activeAudio ? trackLabel(activeAudio) : 'Audio'}</span>
              </button>
            }
            className="w-72"
          >
            <TrackList
              title="Audio track"
              items={audios.map((t) => ({ id: t.id, label: trackLabel(t), selected: t.selected }))}
              onPick={(id) => void send(['set_property', 'aid', id])}
            />
            <DelayRow
              label="Audio delay"
              value={state?.audioDelay ?? 0}
              step={0.05}
              onAdd={(d) => void send(['add', 'audio-delay', d])}
              onReset={() => void send(['set_property', 'audio-delay', 0])}
            />
          </Popover>

          {/* Subtitles + delay + visibility */}
          <Popover
            onOpenChange={onPin}
            trigger={
              <button className="h-8 px-2.5 rounded-lg text-[12px] text-white/90 hover:bg-white/10 inline-flex items-center gap-1.5" disabled={disabled}>
                {state?.subVisibility === false ? <CaptionsOff className="size-4" /> : <Captions className="size-4" />}
                <span className="max-w-[140px] truncate">{activeSub ? trackLabel(activeSub) : 'No subs'}</span>
              </button>
            }
            className="w-72"
          >
            <TrackList
              title="Subtitles"
              items={[{ id: 'no', label: 'None', selected: !activeSub }, ...subs.map((t) => ({ id: t.id, label: trackLabel(t), selected: t.selected }))]}
              onPick={(id) => void send(['set_property', 'sid', id])}
            />
            <DelayRow
              label="Subtitle delay"
              value={state?.subDelay ?? 0}
              step={0.1}
              onAdd={(d) => void send(['add', 'sub-delay', d])}
              onReset={() => void send(['set_property', 'sub-delay', 0])}
            />
            <button
              className="mt-2 w-full h-8 rounded-lg text-[13px] bg-panel border border-hairline hover:bg-hover"
              onClick={() => void send(['cycle', 'sub-visibility'])}
            >
              {state?.subVisibility === false ? 'Show subtitles' : 'Hide subtitles'}
            </button>
          </Popover>

          {/* mpv volume */}
          <Popover
            onOpenChange={onPin}
            trigger={
              <button className="h-8 px-2 rounded-lg text-white/90 hover:bg-white/10 inline-flex items-center gap-1.5 text-[12px] font-mono" disabled={disabled}>
                <Volume2 className="size-4" />
                {Math.round(state?.volume ?? 100)}
              </button>
            }
            className="w-56"
          >
            <div className="text-xs text-muted mb-2">Projector (mpv) volume — what everyone hears</div>
            <MpvVolume value={state?.volume ?? 100} onSet={(v) => void send(['set_property', 'volume', v])} />
            <div className="text-[11px] text-muted mt-1.5 flex justify-between">
              <span>0</span>
              <span>100</span>
              <span>130</span>
            </div>
          </Popover>

          {/* Quality */}
          <Menu
            onOpenChange={onPin}
            trigger={
              <button className="h-8 px-2.5 rounded-lg text-[12px] text-white/90 hover:bg-white/10" disabled={disabled}>
                {state?.preset ?? 'Quality'}
              </button>
            }
          >
            <MenuLabel>Encoder preset</MenuLabel>
            {presets.map((p) => (
              <MenuItem key={p} selected={state?.preset === p} onSelect={() => void send(['vs/quality', p], 'quality')} disabled={busy === 'quality'}>
                {PRESET_LABEL[p]}
              </MenuItem>
            ))}
            {presets.length < PRESETS.length && (
              <>
                <MenuSeparator />
                <div className="px-2 py-1 text-[11px] text-muted">Capped at {settings?.maxPreset} by room settings</div>
              </>
            )}
          </Menu>
            </>
          )}

          <Button size="sm" variant="primary" onClick={onOpenQueue} className="ml-1">
            <FolderOpen className="size-4" /> Open…
          </Button>
        </div>
      </div>
    </div>
  );
}

/** Local echo while dragging, one command per ~100 ms plus the final value. */
function MpvVolume({ value, onSet }: { value: number; onSet: (v: number) => void }) {
  const [local, setLocal] = useState<number | null>(null);
  const lastSent = useRef(0);
  return (
    <Slider
      label="mpv volume"
      min={0}
      max={130}
      step={1}
      value={local ?? value}
      accent
      onChange={(v) => {
        setLocal(v);
        const now = performance.now();
        if (now - lastSent.current > 100) {
          lastSent.current = now;
          onSet(v);
        }
      }}
      onCommit={(v) => {
        onSet(v);
        setTimeout(() => setLocal(null), 400);
      }}
    />
  );
}

function SeekChip({ label, onClick, disabled }: { label: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      className="h-8 px-2 rounded-lg text-[12px] font-mono text-white/90 hover:bg-white/10 disabled:opacity-40"
      onClick={onClick}
      disabled={disabled}
    >
      {label}
    </button>
  );
}

function TrackList({
  title,
  items,
  onPick,
}: {
  title: string;
  items: Array<{ id: number | string; label: string; selected: boolean }>;
  onPick: (id: number | string) => void;
}) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wider text-muted mb-1.5">{title}</div>
      {items.length === 0 ? (
        <div className="text-sm text-muted py-1">No tracks</div>
      ) : (
        <div className="max-h-52 overflow-y-auto -mx-1">
          {items.map((t) => (
            <button
              key={t.id}
              onClick={() => onPick(t.id)}
              className={cn(
                'w-full text-left h-8 px-2 rounded-lg text-[13px] truncate hover:bg-hover',
                t.selected && 'text-accent',
              )}
            >
              {t.selected ? '● ' : ''}
              {t.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function DelayRow({
  label,
  value,
  step,
  onAdd,
  onReset,
}: {
  label: string;
  value: number;
  step: number;
  onAdd: (delta: number) => void;
  onReset: () => void;
}) {
  return (
    <div className="mt-3 flex items-center justify-between gap-2">
      <span className="text-[12px] text-muted">{label}</span>
      <span className="inline-flex items-center gap-1">
        <button className="h-7 px-2 rounded-md bg-panel border border-hairline text-[12px] font-mono hover:bg-hover" onClick={() => onAdd(-step)}>
          −{step}
        </button>
        <button className="h-7 min-w-[64px] rounded-md text-[12px] font-mono hover:bg-hover" onClick={onReset} title="Reset">
          {formatDelay(value, step < 0.1 ? 2 : 1)}
        </button>
        <button className="h-7 px-2 rounded-md bg-panel border border-hairline text-[12px] font-mono hover:bg-hover" onClick={() => onAdd(step)}>
          +{step}
        </button>
      </span>
    </div>
  );
}
