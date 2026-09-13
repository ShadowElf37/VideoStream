import { useTracks } from '@livekit/components-react';
import { RemoteAudioTrack, RoomEvent, Track } from 'livekit-client';
import { useEffect, useRef } from 'react';

/**
 * Attaches every subscribed remote audio track to a (silent) element so it
 * flows through LiveKit's Web Audio mix. Tracks are never detached to mute:
 * gain 0 does that, which keeps the audio graph stable across Chrome versions.
 */
export function AudioRenderer() {
  const refs = useTracks([Track.Source.Microphone, Track.Source.ScreenShareAudio, Track.Source.Unknown], {
    onlySubscribed: true,
    updateOnlyOn: [RoomEvent.TrackSubscribed, RoomEvent.TrackUnsubscribed, RoomEvent.ParticipantDisconnected, RoomEvent.Reconnected],
  });
  const audio = refs.filter((r) => r.publication.track instanceof RemoteAudioTrack && !r.participant.isLocal);
  return (
    <div hidden aria-hidden>
      {audio.map((r) => (
        <AudioSink key={r.publication.trackSid} track={r.publication.track as RemoteAudioTrack} />
      ))}
    </div>
  );
}

function AudioSink({ track }: { track: RemoteAudioTrack }) {
  const el = useRef<HTMLAudioElement>(null);
  useEffect(() => {
    const node = el.current;
    if (!node) return;
    track.attach(node);
    return () => {
      track.detach(node);
    };
  }, [track]);
  return <audio ref={el} autoPlay playsInline />;
}
