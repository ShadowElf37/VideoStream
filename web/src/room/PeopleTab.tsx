import { useParticipants } from '@livekit/components-react';
import { ConnectionQuality, type Participant } from 'livekit-client';
import { Crown, Ellipsis, HeadphoneOff, MicOff, MicVocal, Radio } from 'lucide-react';
import { useRef } from 'react';
import { useAudio } from '@/audio/useAudioModel';
import { VOICE_VOLUME_MAX } from '@/audio/model';
import { useMpvStore } from '@/host/useMpv';
import { cn } from '@/lib/cn';
import { colorFor } from '@/lib/colors';
import { useSession } from '@/state/session';
import { Avatar } from '@/ui/Avatar';
import { Menu, MenuItem, MenuSeparator } from '@/ui/Menu';
import { Slider } from '@/ui/Slider';
import { Tooltip } from '@/ui/Tooltip';
import { useParticipantLive } from './hooks';
import { displayName, isHost, isProjector, parseMetadata } from './identity';

// TODO(server): host handoff and kick need server endpoints; hidden until then.
const FEATURE_MODERATION = false;

export function PeopleTab() {
  const participants = useParticipants();
  const projectorOnline = useMpvStore((s) => s.projectorOnline);
  const mpv = useMpvStore((s) => s.state);
  const people = participants.filter((p) => !isProjector(p)).sort((a, b) => {
    if (a.isLocal !== b.isLocal) return a.isLocal ? -1 : 1;
    if (isHost(a) !== isHost(b)) return isHost(a) ? -1 : 1;
    return displayName(a).localeCompare(displayName(b));
  });

  return (
    <div className="p-2">
      <div className="flex items-center gap-3 px-2 py-2 rounded-xl bg-panel border border-hairline mb-2">
        <span className={cn('size-8 rounded-lg inline-flex items-center justify-center', projectorOnline ? 'bg-accent/15 text-accent' : 'bg-hover text-muted')}>
          <Radio className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium">Projector</div>
          <div className="text-[11px] text-muted truncate">
            {!projectorOnline ? 'offline' : mpv && !mpv.idle ? `live · ${mpv.mediaTitle || 'playing'}` : 'idle'}
          </div>
        </div>
        <span className={cn('size-2 rounded-full', projectorOnline ? (mpv && !mpv.idle ? 'bg-ok' : 'bg-accent') : 'bg-muted/40')} />
      </div>

      <div className="px-2 pt-1 pb-1 text-[11px] uppercase tracking-wider text-muted">{people.length} in the room</div>
      <ul className="space-y-0.5">
        {people.map((p) => (
          <PersonRow key={p.identity} p={p} />
        ))}
      </ul>
    </div>
  );
}

function PersonRow({ p }: { p: Participant }) {
  const { speaking, quality, micMuted } = useParticipantLive(p);
  const remotePresence = useSession((s) => s.presence[p.identity]);
  const { state, dispatch, micEnabled } = useAudio();
  // Our own presence isn't echoed back, so derive it from the audio model.
  const presence = p.isLocal ? { micMuted: !micEnabled, deafened: state.deafened, ptt: state.ptt } : remotePresence;
  const meta = parseMetadata(p.metadata);
  const color = meta.color ?? colorFor(p.identity);
  const name = displayName(p);
  const volume = state.voiceVolumes[p.identity] ?? 1;
  const mutedForMe = volume === 0;
  const lastNonZero = useRef(1);
  if (volume > 0) lastNonZero.current = volume;

  const effectiveMuted = p.isLocal ? !micEnabled : micMuted || !!presence?.micMuted;

  return (
    <li className="group rounded-xl px-2 py-2 hover:bg-hover/60">
      <div className="flex items-center gap-3">
        <Avatar name={name} color={color} size={34} speaking={speaking} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5 min-w-0">
            <span className="text-[13px] font-medium truncate">{name}</span>
            {p.isLocal && <span className="text-[11px] text-muted">you</span>}
            {isHost(p) && (
              <Tooltip label="Host">
                <Crown className="size-3.5 text-accent shrink-0" />
              </Tooltip>
            )}
          </div>
          <div className="flex items-center gap-1.5 text-muted h-4">
            {effectiveMuted && (
              <Tooltip label="Mic muted">
                <MicOff className="size-3.5 text-danger" />
              </Tooltip>
            )}
            {presence?.deafened && (
              <Tooltip label="Deafened">
                <HeadphoneOff className="size-3.5 text-warn" />
              </Tooltip>
            )}
            {presence?.ptt && (
              <Tooltip label="Push to talk">
                <MicVocal className="size-3.5" />
              </Tooltip>
            )}
            <QualityDots q={quality} />
          </div>
        </div>
        {!p.isLocal && (
          <Menu
            side="bottom"
            align="end"
            trigger={
              <button aria-label={`Options for ${name}`} className="size-7 rounded-lg inline-flex items-center justify-center text-muted hover:text-text hover:bg-hover opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100">
                <Ellipsis className="size-4" />
              </button>
            }
          >
            <MenuItem
              selected={mutedForMe}
              onSelect={() => dispatch({ type: 'setVoiceVolume', identity: p.identity, volume: mutedForMe ? lastNonZero.current : 0 })}
            >
              {mutedForMe ? 'Unmute for me' : 'Mute for me'}
            </MenuItem>
            <MenuItem onSelect={() => dispatch({ type: 'setVoiceVolume', identity: p.identity, volume: 1 })}>Reset volume</MenuItem>
            {FEATURE_MODERATION && (
              <>
                <MenuSeparator />
                <MenuItem disabled>Make host</MenuItem>
                <MenuItem disabled destructive>
                  Kick
                </MenuItem>
              </>
            )}
          </Menu>
        )}
      </div>
      {!p.isLocal && (
        <div className="mt-1.5 flex items-center gap-2 pl-[46px]">
          <Slider
            label={`${name} volume`}
            min={0}
            max={VOICE_VOLUME_MAX}
            step={0.05}
            value={volume}
            ticks={[1]}
            disabled={state.deafened}
            onChange={(v) => dispatch({ type: 'setVoiceVolume', identity: p.identity, volume: v })}
          />
          <span className={cn('w-10 text-right font-mono text-[11px] tabular-nums', mutedForMe ? 'text-danger' : 'text-muted')}>{Math.round(volume * 100)}%</span>
        </div>
      )}
    </li>
  );
}

function QualityDots({ q }: { q: ConnectionQuality }) {
  const n = q === ConnectionQuality.Excellent ? 3 : q === ConnectionQuality.Good ? 2 : q === ConnectionQuality.Poor ? 1 : 0;
  const label =
    q === ConnectionQuality.Excellent ? 'Connection: excellent' : q === ConnectionQuality.Good ? 'Connection: good' : q === ConnectionQuality.Poor ? 'Connection: poor' : q === ConnectionQuality.Lost ? 'Connection lost' : 'Connection: unknown';
  return (
    <Tooltip label={label}>
      <span className="inline-flex items-end gap-px h-3 ml-auto" aria-label={label}>
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className={cn('w-1 rounded-sm', i < n ? (n === 1 ? 'bg-danger' : n === 2 ? 'bg-accent' : 'bg-ok') : 'bg-hairline-strong')}
            style={{ height: 4 + i * 3 }}
          />
        ))}
      </span>
    </Tooltip>
  );
}
