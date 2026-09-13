import type { RemoteVideoTrack } from 'livekit-client';
import { useEffect, useRef } from 'react';

/**
 * The stage's player, kept behind a tiny prop interface so an MSE / LL-HLS
 * "big buffer" mode can be swapped in later without touching the rest.
 */
export function MovieVideo({
  track,
  paused,
  onStalled,
  videoRef,
}: {
  track: RemoteVideoTrack | null;
  /** The projector's pause state, from mpv.state. */
  paused: boolean;
  onStalled: (stalled: boolean) => void;
  videoRef: React.RefObject<HTMLVideoElement | null>;
}) {
  const stalledRef = useRef(false);
  useEffect(() => {
    const el = videoRef.current;
    if (!el) return;
    el.muted = false;
    const set = (v: boolean) => {
      if (stalledRef.current === v) return;
      stalledRef.current = v;
      onStalled(v);
    };
    const onWait = () => set(true);
    const onPlay = () => set(false);
    el.addEventListener('waiting', onWait);
    el.addEventListener('stalled', onWait);
    el.addEventListener('playing', onPlay);
    el.addEventListener('canplay', onPlay);
    el.addEventListener('timeupdate', onPlay);
    return () => {
      el.removeEventListener('waiting', onWait);
      el.removeEventListener('stalled', onWait);
      el.removeEventListener('playing', onPlay);
      el.removeEventListener('canplay', onPlay);
      el.removeEventListener('timeupdate', onPlay);
    };
  }, [onStalled, videoRef]);

  // Act on the pause locally instead of waiting for the stream to dry up.
  //
  // The command reaches every client in about a round trip, while the picture
  // each client is showing runs a whole jitter buffer behind the projector —
  // 1.5s by default. Waiting for the frames to stop arriving meant pause took
  // that long to appear to do anything (measured: 1555 ms and 37 further
  // frames after the click, while the projector itself acted in 0 ms).
  //
  // Freezing here costs no synchronisation: every client was already running
  // its own buffer-depth behind, and each one freezes and resumes at the same
  // point in the film it was already at, so the offsets between viewers are
  // exactly what they were during playback. The buffer is left alone, which
  // is the point — it is what absorbs shaky Wi-Fi, and shrinking it to make
  // the controls feel quick would trade away the thing it exists for.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !track) return;
    if (paused) el.pause();
    else void el.play().catch(() => undefined);
  }, [paused, track, videoRef]);

  useEffect(() => {
    const el = videoRef.current;
    if (!el || !track) return;
    track.attach(el);
    void el.play().catch(() => undefined);
    return () => {
      track.detach(el);
      stalledRef.current = false;
      onStalled(false);
    };
  }, [track, videoRef, onStalled]);

  return (
    <video
      ref={videoRef}
      className="stage-video"
      playsInline
      autoPlay
      muted={false}
      disablePictureInPicture={false}
      controls={false}
      style={{ visibility: track ? 'visible' : 'hidden' }}
    />
  );
}
