import {
  RemoteAudioTrack,
  RemoteParticipant,
  RemoteTrackPublication,
  Room,
  RoomEvent,
  Track,
  type Participant,
} from 'livekit-client';
import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef } from 'react';
import { publish } from '@/lib/data';
import { Topics, type PresenceMessage } from '@/proto/messages';
import { usePrefs } from '@/state/prefs';
import { isProjector, PROJECTOR_IDENTITY } from '@/room/identity';
import { initialAudioState, micEnabled, movieGain, presenceOf, reduce, voiceGain, type AudioAction, type AudioState } from './model';

export interface AudioController {
  state: AudioState;
  dispatch: (a: AudioAction) => void;
  micEnabled: boolean;
}

export const AudioModelContext = createContext<AudioController | null>(null);

export function useAudio(): AudioController {
  const c = useContext(AudioModelContext);
  if (!c) throw new Error('useAudio outside AudioModelContext');
  return c;
}

const DEAFEN_UNSUBSCRIBE_MS = 10_000;
const DUCK_RELEASE_MS = 600;

function isMoviePublication(p: Participant, pub: { trackName: string; source: Track.Source }): boolean {
  return isProjector(p) || pub.trackName === 'movie.audio' || pub.source === Track.Source.ScreenShareAudio;
}

/**
 * Owns the audio reducer and applies its derived gains to a LiveKit room.
 * Idempotent: every application re-derives from state so reconnects and late
 * subscriptions converge to the same result.
 */
export function useAudioModel(room: Room | null, connected: boolean): AudioController {
  const prefs = usePrefs();
  const [state, dispatch] = useReducer(reduce, undefined, (): AudioState => ({
    ...initialAudioState,
    micMuted: prefs.joinMuted,
    deafenImpliesMute: prefs.deafenImpliesMute,
    ptt: prefs.ptt,
    movieMuted: prefs.movieMuted,
    movieVolume: prefs.movieVolume,
    duckDb: prefs.duckDb,
    voiceVolumes: prefs.voiceVolumes,
  }));

  const enabled = micEnabled(state);
  const stateRef = useRef(state);
  stateRef.current = state;

  // Persist the bits that should survive a reload.
  const patch = usePrefs((s) => s.patch);
  useEffect(() => {
    patch({
      movieMuted: state.movieMuted,
      movieVolume: state.movieVolume,
      voiceVolumes: state.voiceVolumes,
      ptt: state.ptt,
      duckDb: state.duckDb as 0 | -6 | -12,
      deafenImpliesMute: state.deafenImpliesMute,
    });
  }, [patch, state.movieMuted, state.movieVolume, state.voiceVolumes, state.ptt, state.duckDb, state.deafenImpliesMute]);

  // 1. Mic enable/disable (track stays published; mute is instant).
  const micChain = useRef(Promise.resolve());
  useEffect(() => {
    if (!room || !connected) return;
    micChain.current = micChain.current
      .then(() => room.localParticipant.setMicrophoneEnabled(enabled))
      .then(() => undefined)
      .catch((e) => console.warn('setMicrophoneEnabled failed', e));
  }, [room, connected, enabled]);

  // 2. Remote gains. Elements stay attached; gain 0 is "muted".
  const applyGains = useCallback(() => {
    if (!room) return;
    const s = stateRef.current;
    const mg = movieGain(s);
    for (const p of room.remoteParticipants.values()) {
      for (const pub of p.audioTrackPublications.values()) {
        const track = pub.track;
        if (!(track instanceof RemoteAudioTrack)) continue;
        const target = isMoviePublication(p, pub) ? mg : voiceGain(s, p.identity);
        track.setVolume(target);
      }
    }
  }, [room]);

  useEffect(applyGains, [applyGains, state.deafened, state.voiceVolumes, state.movieMuted, state.movieVolume, state.ducking, state.duckDb]);

  useEffect(() => {
    if (!room) return;
    const reapply = () => applyGains();
    room.on(RoomEvent.TrackSubscribed, reapply);
    room.on(RoomEvent.Reconnected, reapply);
    room.on(RoomEvent.ParticipantMetadataChanged, reapply);
    room.on(RoomEvent.AudioPlaybackStatusChanged, reapply);
    return () => {
      room.off(RoomEvent.TrackSubscribed, reapply);
      room.off(RoomEvent.Reconnected, reapply);
      room.off(RoomEvent.ParticipantMetadataChanged, reapply);
      room.off(RoomEvent.AudioPlaybackStatusChanged, reapply);
    };
  }, [room, applyGains]);

  // 3. Ducking: any non-projector active speaker ducks the movie; 600 ms release.
  useEffect(() => {
    if (!room || state.duckDb >= 0) {
      dispatch({ type: 'setDucking', ducking: false });
      return;
    }
    let release: ReturnType<typeof setTimeout> | null = null;
    const onSpeakers = (speakers: Participant[]) => {
      const talking = speakers.some((p) => p.identity !== PROJECTOR_IDENTITY && !isProjector(p));
      if (talking) {
        if (release) clearTimeout(release);
        release = null;
        dispatch({ type: 'setDucking', ducking: true });
      } else if (!release) {
        release = setTimeout(() => {
          release = null;
          dispatch({ type: 'setDucking', ducking: false });
        }, DUCK_RELEASE_MS);
      }
    };
    room.on(RoomEvent.ActiveSpeakersChanged, onSpeakers);
    return () => {
      room.off(RoomEvent.ActiveSpeakersChanged, onSpeakers);
      if (release) clearTimeout(release);
    };
  }, [room, state.duckDb]);

  // 4. Long deafen → unsubscribe voice publications to save bandwidth.
  const voiceUnsubscribed = useRef(false);
  useEffect(() => {
    if (!room) return;
    const setVoiceSubscriptions = (subscribed: boolean) => {
      for (const p of room.remoteParticipants.values()) {
        if (isProjector(p)) continue;
        for (const pub of p.audioTrackPublications.values()) {
          if (pub instanceof RemoteTrackPublication && pub.source === Track.Source.Microphone) pub.setSubscribed(subscribed);
        }
      }
    };
    if (!state.deafened) {
      if (voiceUnsubscribed.current) {
        voiceUnsubscribed.current = false;
        setVoiceSubscriptions(true);
      }
      return;
    }
    const t = setTimeout(() => {
      voiceUnsubscribed.current = true;
      setVoiceSubscriptions(false);
    }, DEAFEN_UNSUBSCRIBE_MS);
    const onPublished = (pub: RemoteTrackPublication, p: RemoteParticipant) => {
      if (voiceUnsubscribed.current && !isProjector(p) && pub.source === Track.Source.Microphone) pub.setSubscribed(false);
    };
    room.on(RoomEvent.TrackPublished, onPublished);
    return () => {
      clearTimeout(t);
      room.off(RoomEvent.TrackPublished, onPublished);
    };
  }, [room, state.deafened]);

  // 5. Presence broadcast on join, on reconnect and whenever the visible bits change.
  const presence = useMemo<PresenceMessage>(() => presenceOf(state), [state]);
  useEffect(() => {
    if (!room || !connected) return;
    void publish(room, Topics.presence, presence, { reliable: true });
    const onReconnected = () => void publish(room, Topics.presence, presence, { reliable: true });
    const onJoined = () => void publish(room, Topics.presence, presence, { reliable: true });
    room.on(RoomEvent.Reconnected, onReconnected);
    room.on(RoomEvent.ParticipantConnected, onJoined);
    return () => {
      room.off(RoomEvent.Reconnected, onReconnected);
      room.off(RoomEvent.ParticipantConnected, onJoined);
    };
  }, [room, connected, presence.micMuted, presence.deafened, presence.ptt]); // eslint-disable-line react-hooks/exhaustive-deps

  return useMemo(() => ({ state, dispatch, micEnabled: enabled }), [state, enabled]);
}
