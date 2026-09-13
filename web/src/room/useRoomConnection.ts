import { AudioPresets, Room, RoomEvent, type RoomOptions } from 'livekit-client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { getAudioContext, unlockAudio } from '@/audio/context';
import { api, ApiError } from '@/lib/api';
import { useChat } from '@/state/chat';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';

export interface JoinOptions {
  name: string;
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
    publishDefaults: { dtx: true, red: true, audioPreset: AudioPresets.speech },
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

/**
 * Token fetch + LiveKit connect lifecycle. Creates a fresh Room per connect so
 * a reconnect-after-token-expiry starts from a clean slate.
 */
export function useRoomConnection(roomId: string) {
  const [room, setRoom] = useState<Room | null>(null);
  const [connected, setConnected] = useState(false);
  const roomRef = useRef<Room | null>(null);
  const lastJoin = useRef<JoinOptions | null>(null);
  const session = useSession;

  const teardown = useCallback(async () => {
    const r = roomRef.current;
    roomRef.current = null;
    setRoom(null);
    setConnected(false);
    if (r) {
      r.removeAllListeners();
      await r.disconnect().catch(() => undefined);
    }
  }, []);

  const connect = useCallback(
    async (opts: JoinOptions) => {
      lastJoin.current = opts;
      const s = session.getState();
      s.setPhase('connecting');
      await teardown();

      // The click that gets us here is the user gesture that unlocks audio.
      await unlockAudio();

      let token;
      try {
        token = await api.getToken(roomId, {
          name: opts.name,
          inviteKey: s.credentials.inviteKey,
          hostSecret: s.credentials.hostSecret,
          password: opts.password,
        });
      } catch (e) {
        const msg = e instanceof ApiError ? (e.status === 401 || e.status === 403 ? 'Wrong password or link.' : e.message) : String(e);
        s.setPhase('failed', msg);
        throw e;
      }
      s.setToken(token);
      useChat.getState().setSelf(token.identity);

      const r = new Room(roomOptions());
      roomRef.current = r;
      setRoom(r);

      r.on(RoomEvent.Reconnecting, () => session.getState().setPhase('reconnecting'));
      r.on(RoomEvent.Reconnected, () => {
        session.getState().setPhase('connected');
        session.getState().toast('Reconnected');
      });
      r.on(RoomEvent.Disconnected, (reason) => {
        setConnected(false);
        session.getState().setPhase('disconnected', reason !== undefined ? `Disconnected (${String(reason)})` : 'Disconnected');
      });
      r.on(RoomEvent.AudioPlaybackStatusChanged, () => session.getState().setAudioBlocked(!r.canPlaybackAudio));

      try {
        await r.connect(token.url, token.token, { autoSubscribe: true });
      } catch (e) {
        s.setPhase('failed', e instanceof Error ? e.message : String(e));
        await teardown();
        throw e;
      }
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
    [roomId, session, teardown],
  );

  /** Re-fetch a token and connect again (used after Disconnected / token expiry). */
  const reconnect = useCallback(async () => {
    const j = lastJoin.current;
    if (!j) return;
    await connect(j).catch(() => undefined);
  }, [connect]);

  const leave = useCallback(async () => {
    lastJoin.current = null;
    await teardown();
    session.getState().reset();
    useChat.getState().reset();
  }, [teardown, session]);

  useEffect(() => {
    return () => {
      void teardown();
    };
  }, [teardown]);

  return { room, connected, connect, reconnect, leave };
}
