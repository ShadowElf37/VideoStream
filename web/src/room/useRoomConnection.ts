import { AudioPresets, DisconnectReason, Room, RoomEvent, type RoomOptions } from 'livekit-client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { getAudioContext, unlockAudio } from '@/audio/context';
import { api, ApiError } from '@/lib/api';
import { useChat } from '@/state/chat';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';

export interface JoinOptions {
  name: string;
  /** Only on the first connect; afterwards the cookie vouches for us. */
  password?: string;
  micDeviceId?: string;
  joinMuted: boolean;
}

function roomOptions(): RoomOptions {
  const p = usePrefs.getState();
  return {
    adaptiveStream: false,
    dynacast: false,
    webAudioMix: { audioContext: getAudioContext() },
    // The mic stays open while muted by default, so unmuting is instant;
    // releasing it is a preference, for people who want the indicator off.
    publishDefaults: { dtx: true, red: true, audioPreset: AudioPresets.speech, stopMicTrackOnMute: p.releaseMicOnMute },
    audioCaptureDefaults: {
      deviceId: p.micDeviceId || undefined,
      echoCancellation: p.echoCancellation,
      noiseSuppression: p.noiseSuppression,
      autoGainControl: p.autoGainControl,
    },
    stopLocalTrackOnUnpublish: true,
    disconnectOnPageLeave: true,
  };
}

/** Reasons where retrying with a fresh token is pointless. */
const FINAL_REASONS = new Set<DisconnectReason>([
  DisconnectReason.CLIENT_INITIATED,
  DisconnectReason.DUPLICATE_IDENTITY,
  DisconnectReason.PARTICIPANT_REMOVED,
  DisconnectReason.ROOM_DELETED,
]);

/** The key in a viewer link, or undefined. */
export function keyFromLink(link: string | undefined): string | undefined {
  if (!link) return undefined;
  try {
    return new URL(link).searchParams.get('k') ?? undefined;
  } catch {
    return undefined;
  }
}

/**
 * Token fetch + LiveKit connect lifecycle. Every (re)connect fetches a fresh
 * token and creates a fresh Room; the previous Room is only torn down once the
 * new one exists so the room view never unmounts during a reconnect.
 */
export function useRoomConnection() {
  const [room, setRoom] = useState<Room | null>(null);
  const [connected, setConnected] = useState(false);
  const roomRef = useRef<Room | null>(null);
  const lastJoin = useRef<JoinOptions | null>(null);
  const autoRetried = useRef(false);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const session = useSession;

  const dispose = useCallback((r: Room | null) => {
    if (!r) return;
    r.removeAllListeners();
    void r.disconnect().catch(() => undefined);
  }, []);

  const connect = useCallback(
    async (opts: JoinOptions) => {
      lastJoin.current = opts;
      if (retryTimer.current) clearTimeout(retryTimer.current);
      const s = session.getState();
      s.setPhase('connecting');

      // The click that gets us here is the user gesture that unlocks audio.
      await unlockAudio();

      let token;
      try {
        token = await api.getToken({
          name: opts.name,
          key: s.credentials.key,
          password: opts.password || undefined,
        });
      } catch (e) {
        const auth = e instanceof ApiError && e.isAuth;
        const msg = e instanceof ApiError ? (auth ? e.message : e.message) : String(e);
        if (auth && roomRef.current) {
          // A reconnect the door refused: our link rotated while we were
          // away and this device is not remembered. Back to the door, with
          // the reason, rather than a reconnect button that can never work.
          const old = roomRef.current;
          roomRef.current = null;
          setRoom(null);
          setConnected(false);
          dispose(old);
          s.setLinkExpired(true);
        }
        s.setPhase(roomRef.current ? 'disconnected' : 'failed', msg);
        throw e;
      }
      s.setToken(token);
      s.setName(opts.name);
      s.setLinkExpired(false);
      // From here on the cookie vouches for us; keep the current key too, and
      // drop the password from memory.
      const key = keyFromLink(token.links.viewer);
      s.setCredentials({ key });
      // The address bar becomes the invite link: a refresh keeps working, and
      // "copy the URL" is a way to invite someone.
      if (key && typeof window !== 'undefined') {
        const url = new URL(window.location.href);
        url.search = `?k=${encodeURIComponent(key)}`;
        window.history.replaceState(window.history.state, '', url);
      }
      useChat.getState().setSelf(token.identity);

      const old = roomRef.current;
      const r = new Room(roomOptions());
      roomRef.current = r;
      setConnected(false);
      setRoom(r);
      dispose(old);

      r.on(RoomEvent.Reconnecting, () => session.getState().setPhase('reconnecting'));
      r.on(RoomEvent.Reconnected, () => {
        session.getState().setPhase('connected');
        session.getState().toast('Reconnected');
      });
      r.on(RoomEvent.Disconnected, (reason) => {
        if (roomRef.current !== r) return;
        setConnected(false);
        const final = reason !== undefined && FINAL_REASONS.has(reason);
        session.getState().setPhase('disconnected', describeReason(reason));
        // One automatic retry with a fresh token (covers token expiry and server restarts).
        if (!final && !autoRetried.current && lastJoin.current) {
          autoRetried.current = true;
          retryTimer.current = setTimeout(() => void connect(lastJoin.current!).catch(() => undefined), 1500);
        }
      });
      r.on(RoomEvent.AudioPlaybackStatusChanged, () => session.getState().setAudioBlocked(!r.canPlaybackAudio));

      try {
        await r.connect(token.url, token.token, { autoSubscribe: true });
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        if (old) {
          // Reconnect attempt failed: keep the room view, show the card.
          session.getState().setPhase('disconnected', msg);
        } else {
          session.getState().setPhase('failed', msg);
          roomRef.current = null;
          setRoom(null);
          dispose(r);
        }
        throw e;
      }
      autoRetried.current = false;
      await r.startAudio().catch(() => session.getState().setAudioBlocked(true));
      session.getState().setAudioBlocked(!r.canPlaybackAudio);

      // Publish the mic straight away (muted if asked) so the first unmute is instant.
      try {
        await r.localParticipant.setMicrophoneEnabled(true);
        if (opts.joinMuted) await r.localParticipant.setMicrophoneEnabled(false);
      } catch (e) {
        session.getState().toast('Microphone unavailable: ' + (e instanceof Error ? e.message : String(e)), 'warn', 6000);
      }
      if (opts.micDeviceId) await r.switchActiveDevice('audioinput', opts.micDeviceId).catch(() => undefined);
      const spk = usePrefs.getState().speakerDeviceId;
      if (spk) await r.switchActiveDevice('audiooutput', spk).catch(() => undefined);

      session.getState().setPhase('connected');
      setConnected(true);
    },
    [session, dispose],
  );

  /** Re-fetch a token and connect again (used from the reconnect card). */
  const reconnect = useCallback(async () => {
    const j = lastJoin.current;
    if (!j) return;
    await connect(j).catch(() => undefined);
  }, [connect]);

  const leave = useCallback(async () => {
    lastJoin.current = null;
    if (retryTimer.current) clearTimeout(retryTimer.current);
    const r = roomRef.current;
    roomRef.current = null;
    setRoom(null);
    setConnected(false);
    dispose(r);
    session.getState().reset();
    useChat.getState().reset();
  }, [dispose, session]);

  useEffect(() => {
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current);
      dispose(roomRef.current);
      roomRef.current = null;
    };
  }, [dispose]);

  return { room, connected, connect, reconnect, leave };
}

function describeReason(reason?: DisconnectReason): string {
  switch (reason) {
    case DisconnectReason.DUPLICATE_IDENTITY:
      return 'You joined from another tab or device.';
    case DisconnectReason.PARTICIPANT_REMOVED:
      return 'You were removed from the room.';
    case DisconnectReason.ROOM_DELETED:
      return 'The room was closed.';
    case DisconnectReason.SERVER_SHUTDOWN:
      return 'The media server restarted.';
    case DisconnectReason.CLIENT_INITIATED:
      return 'Disconnected.';
    default:
      return 'The connection to the room was lost.';
  }
}
