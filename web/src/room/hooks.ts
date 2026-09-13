import { ConnectionQuality, ParticipantEvent, type Participant } from 'livekit-client';
import { useCallback, useEffect, useMemo, useState } from 'react';

export function useMediaQuery(query: string): boolean {
  const [match, setMatch] = useState(() => (typeof window !== 'undefined' ? window.matchMedia(query).matches : false));
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setMatch(mq.matches);
    on();
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, [query]);
  return match;
}

export const useIsNarrow = () => useMediaQuery('(max-width: 720px)');

/** Live per-participant flags that `useParticipants` does not re-render for. */
export function useParticipantLive(p: Participant) {
  const [speaking, setSpeaking] = useState(p.isSpeaking);
  const [quality, setQuality] = useState<ConnectionQuality>(p.connectionQuality);
  const [micMuted, setMicMuted] = useState(!p.isMicrophoneEnabled);
  useEffect(() => {
    setSpeaking(p.isSpeaking);
    setQuality(p.connectionQuality);
    setMicMuted(!p.isMicrophoneEnabled);
    const onSpeak = (v: boolean) => setSpeaking(v);
    const onQuality = (q: ConnectionQuality) => setQuality(q);
    const onMute = () => setMicMuted(!p.isMicrophoneEnabled);
    p.on(ParticipantEvent.IsSpeakingChanged, onSpeak);
    p.on(ParticipantEvent.ConnectionQualityChanged, onQuality);
    p.on(ParticipantEvent.TrackMuted, onMute);
    p.on(ParticipantEvent.TrackUnmuted, onMute);
    p.on(ParticipantEvent.TrackPublished, onMute);
    p.on(ParticipantEvent.TrackUnpublished, onMute);
    p.on(ParticipantEvent.LocalTrackPublished, onMute);
    p.on(ParticipantEvent.LocalTrackUnpublished, onMute);
    return () => {
      p.off(ParticipantEvent.IsSpeakingChanged, onSpeak);
      p.off(ParticipantEvent.ConnectionQualityChanged, onQuality);
      p.off(ParticipantEvent.TrackMuted, onMute);
      p.off(ParticipantEvent.TrackUnmuted, onMute);
      p.off(ParticipantEvent.TrackPublished, onMute);
      p.off(ParticipantEvent.TrackUnpublished, onMute);
      p.off(ParticipantEvent.LocalTrackPublished, onMute);
      p.off(ParticipantEvent.LocalTrackUnpublished, onMute);
    };
  }, [p]);
  return { speaking, quality, micMuted };
}

/** Auto-hide helper: `visible` while the pointer moves / element focused, hides after `delay`. */
export function useAutoHide(delay = 2500, enabled = true) {
  const [visible, setVisible] = useState(true);
  const [pinned, setPinned] = useState(false);
  useEffect(() => {
    if (!enabled) {
      setVisible(true);
      return;
    }
    let t: ReturnType<typeof setTimeout> | null = null;
    const arm = () => {
      setVisible(true);
      if (t) clearTimeout(t);
      t = setTimeout(() => setVisible(false), delay);
    };
    arm();
    const onMove = () => arm();
    window.addEventListener('pointermove', onMove, { passive: true });
    window.addEventListener('pointerdown', onMove, { passive: true });
    window.addEventListener('keydown', onMove);
    return () => {
      if (t) clearTimeout(t);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerdown', onMove);
      window.removeEventListener('keydown', onMove);
    };
  }, [delay, enabled]);
  return { visible: visible || pinned || !enabled, pin: setPinned };
}

/**
 * Fullscreen the whole document (not the room root): Radix portals render
 * into <body>, and anything outside the fullscreen element is invisible.
 */
export function useFullscreen() {
  const [active, setActive] = useState(false);
  useEffect(() => {
    const on = () => setActive(!!document.fullscreenElement);
    document.addEventListener('fullscreenchange', on);
    return () => document.removeEventListener('fullscreenchange', on);
  }, []);
  const toggle = useCallback(async () => {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await document.documentElement.requestFullscreen({ navigationUI: 'hide' });
    } catch (e) {
      console.warn('fullscreen failed', e);
    }
  }, []);
  return useMemo(() => ({ active, toggle }), [active, toggle]);
}
