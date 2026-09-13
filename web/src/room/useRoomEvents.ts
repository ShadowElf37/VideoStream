import { Room, RoomEvent, type RemoteParticipant } from 'livekit-client';
import { useEffect } from 'react';
import { playNotification } from '@/audio/sounds';
import { decode } from '@/lib/data';
import { formatTime } from '@/lib/format';
import { mentionsName } from '@/lib/links';
import {
  Topics,
  type ChatMessage,
  type PresenceMessage,
  type ReactMessage,
  type RoomSettings,
  type TypingMessage,
} from '@/proto/messages';
import { useChat } from '@/state/chat';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { useMpvStore } from '@/host/useMpv';
import { displayName, isProjector } from './identity';

/**
 * Fan-in for everything that arrives over the data channel that isn't mpv
 * plumbing, plus participant join/leave system lines.
 */
export function useRoomEvents(room: Room | null) {
  useEffect(() => {
    if (!room) return;
    const chat = useChat.getState;
    const session = useSession.getState;

    const onData = (payload: Uint8Array, participant?: RemoteParticipant, _kind?: unknown, topic?: string) => {
      switch (topic) {
        case Topics.chat: {
          const m = decode<ChatMessage>(payload);
          if (!m) return;
          chat().add(m);
          const self = chat().self;
          const selfName = session().name;
          const fromOther = m.kind === 'user' && m.from.identity !== self;
          if (fromOther && document.hidden && usePrefs.getState().notificationSounds) playNotification();
          if (fromOther && selfName && mentionsName(m.text, selfName)) session().toast(`${m.from.name} mentioned you`, 'info', 3000);
          break;
        }
        case Topics.typing: {
          const t = decode<TypingMessage>(payload);
          if (t && participant) chat().setTyping(participant.identity, t.typing);
          break;
        }
        case Topics.react: {
          const r = decode<ReactMessage>(payload);
          if (r && participant) {
            // A pause request gets its own red banner for everyone rather than
            // a floating emoji plus a host-only toast: as a reaction it drifted
            // past in 2.6s among the hearts and was routinely missed.
            if (r.emoji === '⏸️') session().requestPause(displayName(participant));
            else session().addReaction(r.emoji, displayName(participant));
          }
          break;
        }
        case Topics.presence: {
          const p = decode<PresenceMessage>(payload);
          if (p && participant) session().setPresence(participant.identity, p);
          break;
        }
        case Topics.settings: {
          const s = decode<RoomSettings>(payload);
          if (s) session().setSettings(s);
          break;
        }
        default:
          break;
      }
    };

    const onJoin = (p: RemoteParticipant) => {
      if (isProjector(p)) {
        chat().addSystem('Projector connected');
        return;
      }
      chat().addSystem(`${displayName(p)} joined`);
    };
    const onLeave = (p: RemoteParticipant) => {
      session().removePresence(p.identity);
      chat().setTyping(p.identity, false);
      chat().addSystem(isProjector(p) ? 'Projector disconnected' : `${displayName(p)} left`);
    };

    room.on(RoomEvent.DataReceived, onData);
    room.on(RoomEvent.ParticipantConnected, onJoin);
    room.on(RoomEvent.ParticipantDisconnected, onLeave);
    const typingPrune = setInterval(() => chat().pruneTyping(), 1000);
    return () => {
      room.off(RoomEvent.DataReceived, onData);
      room.off(RoomEvent.ParticipantConnected, onJoin);
      room.off(RoomEvent.ParticipantDisconnected, onLeave);
      clearInterval(typingPrune);
    };
  }, [room]);

  // mpv events → chat system lines + "Now playing" toast.
  const lastEvent = useMpvStore((s) => s.lastEvent);
  const eventSeq = useMpvStore((s) => s.eventSeq);
  useEffect(() => {
    if (!lastEvent) return;
    const text = lastEvent.text || describeEvent(lastEvent.type, lastEvent.data);
    useChat.getState().addSystem(text, lastEvent.ts || Date.now());
    if (lastEvent.type === 'file-loaded') {
      const title = (lastEvent.data?.title as string | undefined) ?? useMpvStore.getState().state?.mediaTitle;
      useSession.getState().toast(`Now playing: ${title || text}`, 'info', 5000);
    } else if (lastEvent.type === 'error') {
      useSession.getState().toast(text, 'error', 6000);
    }
  }, [eventSeq]); // eslint-disable-line react-hooks/exhaustive-deps
}

function describeEvent(type: string, data?: Record<string, unknown>): string {
  const at = typeof data?.timePos === 'number' ? ` at ${formatTime(data.timePos)}` : '';
  switch (type) {
    // These come from mpv, which knows the playback changed but not who asked
    // for it — and with anyoneCanPause on it is often not the host. Stay
    // neutral rather than claim an actor we cannot identify.
    case 'pause':
      return `Paused${at}`;
    case 'unpause':
      return `Resumed${at}`;
    case 'seek':
      return `Seeked${at}`;
    case 'file-loaded':
      return 'Loaded a new file';
    case 'end-file':
      return 'Playback ended';
    default:
      return type;
  }
}
