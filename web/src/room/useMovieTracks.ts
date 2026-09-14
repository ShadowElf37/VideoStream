import { useTracks } from '@livekit/components-react';
import { RemoteAudioTrack, RemoteTrackPublication, RemoteVideoTrack, Room, RoomEvent, Track } from 'livekit-client';
import { useEffect, useMemo } from 'react';
import { supportsJitterBufferTarget } from '@/lib/platform';
import { usePrefs } from '@/state/prefs';
import { isProjector, MOVIE_AUDIO_NAME, MOVIE_VIDEO_NAME } from './identity';

export interface MovieTracks {
  video: RemoteVideoTrack | null;
  audio: RemoteAudioTrack | null;
  videoPub: RemoteTrackPublication | null;
  audioPub: RemoteTrackPublication | null;
}

/** The projector's movie video/audio tracks (subscribed), or nulls. */
export function useMovieTracks(): MovieTracks {
  const refs = useTracks([Track.Source.ScreenShare, Track.Source.ScreenShareAudio, Track.Source.Unknown], {
    onlySubscribed: true,
    updateOnlyOn: [
      RoomEvent.TrackSubscribed,
      RoomEvent.TrackUnsubscribed,
      RoomEvent.TrackPublished,
      RoomEvent.TrackUnpublished,
      RoomEvent.ParticipantDisconnected,
      RoomEvent.Reconnected,
      RoomEvent.ParticipantMetadataChanged,
    ],
  });
  return useMemo(() => {
    const out: MovieTracks = { video: null, audio: null, videoPub: null, audioPub: null };
    for (const r of refs) {
      if (!isProjector(r.participant) || !(r.publication instanceof RemoteTrackPublication)) continue;
      const t = r.publication.track;
      const isVideoName = r.publication.trackName === MOVIE_VIDEO_NAME || r.publication.source === Track.Source.ScreenShare;
      const isAudioName = r.publication.trackName === MOVIE_AUDIO_NAME || r.publication.source === Track.Source.ScreenShareAudio;
      if (t instanceof RemoteVideoTrack && (isVideoName || r.publication.kind === Track.Kind.Video)) {
        out.video = t;
        out.videoPub = r.publication;
      } else if (t instanceof RemoteAudioTrack && (isAudioName || r.publication.kind === Track.Kind.Audio)) {
        out.audio = t;
        out.audioPub = r.publication;
      }
    }
    return out;
  }, [refs]);
}

type BufferedReceiver = RTCRtpReceiver & { jitterBufferTarget?: number | null; playoutDelayHint?: number | null };

function applyJitterTarget(track: RemoteVideoTrack | RemoteAudioTrack | null, seconds: number) {
  const r = track?.receiver as BufferedReceiver | undefined;
  if (!r) return;
  const ms = Math.round(Math.min(4000, Math.max(0, seconds * 1000)));
  try {
    if ('jitterBufferTarget' in r) r.jitterBufferTarget = ms;
  } catch (e) {
    console.warn('jitterBufferTarget rejected', e);
  }
  try {
    if ('playoutDelayHint' in r) r.playoutDelayHint = ms / 1000;
  } catch {
    /* older Chrome only */
  }
}

/**
 * Smoothness: the same jitter-buffer target on both movie tracks so they don't
 * fight. Re-applied when the tracks (re)appear, on reconnect and when the
 * preference changes. No-op on Safari.
 */
export function useSmoothness(room: Room | null, movie: MovieTracks) {
  const seconds = usePrefs((s) => s.smoothnessSec);
  useEffect(() => {
    if (!supportsJitterBufferTarget) return;
    const apply = () => {
      applyJitterTarget(movie.video, seconds);
      applyJitterTarget(movie.audio, seconds);
    };
    apply();
    // Receivers can be swapped during a full reconnect; apply again shortly after.
    const t = setTimeout(apply, 1500);
    room?.on(RoomEvent.Reconnected, apply);
    return () => {
      clearTimeout(t);
      room?.off(RoomEvent.Reconnected, apply);
    };
  }, [room, movie.video, movie.audio, seconds]);
}
