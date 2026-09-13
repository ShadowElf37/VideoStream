import type { RemoteVideoTrack } from 'livekit-client';
import { useEffect, useRef } from 'react';

/**
 * The stage's player, kept behind a tiny prop interface so an MSE / LL-HLS
 * "big buffer" mode can be swapped in later without touching the rest.
 */
export function MovieVideo({
  track,
  onStalled,
  videoRef,
}: {
  track: RemoteVideoTrack | null;
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
