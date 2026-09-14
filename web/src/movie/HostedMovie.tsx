import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '@/lib/api';
import type { PlaybackState } from '@/proto/messages';
import { useSession } from '@/state/session';
import { bufferedAheadMs, decide, initialSyncState, RESUME_BUFFER_MS, START_BUFFER_MS, type SyncState } from './sync';
import { clientNowMs, targetMs } from './clock';
import { isReady, REPORT_PERIOD_MS, reportKey } from './ready';
import { useSyncStats, type Correction } from './syncStats';

/**
 * The server-hosted player: a plain <video> over an HTTPS file, kept on the
 * room's clock.
 *
 * The buffer belongs to the browser here, which is the entire point. It
 * decides how far ahead to fetch, keeps what it has, and can seek anywhere in
 * the file — none of which a jitter buffer can do. Playback time is not
 * whatever happens to have arrived; it is what the director says, and this
 * steers the element toward it.
 */

/** How often to compare where we are against where the room is. */
const TICK_MS = 250;

/** Browsers fire `waiting` spuriously on every seek. */
const WAITING_DEBOUNCE_MS = 250;
const PLAYING_DEBOUNCE_MS = 150;

export interface HostedStatus {
  /** Signed error in ms; positive means ahead of the room. */
  errorMs: number;
  bufferedAheadMs: number;
  /** True while there is not enough buffered to play. */
  buffering: boolean;
  rate: number;
}

export function HostedMovie({
  state,
  offsetMs,
  videoRef,
  onStatus,
}: {
  state: PlaybackState;
  offsetMs: number;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  onStatus?: (s: HostedStatus) => void;
}) {
  const sync = useRef<SyncState>(initialSyncState);
  const [buffering, setBuffering] = useState(true);
  const stalled = useRef(false);
  const waitTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastCorrection = useRef<Correction | null>(null);

  // The stats block belongs to the hosted player, so it goes when the player
  // does — otherwise the last sample would sit there over a live RTP picture
  // looking current.
  useEffect(() => () => useSyncStats.getState().clear(), []);

  // Readiness reports, for waitForEveryone. Sent whether or not the room is
  // holding: the director decides a hold from the answers it already has, so
  // a window that is warm before the command lands is what makes the release
  // quick rather than a two-second pause of its own.
  const lastReport = useRef({ key: '', gen: -1, ahead: 0, ready: false });
  const postReport = useCallback((gen: number, ahead: number, ready: boolean) => {
    const session = useSession.getState().token?.session;
    if (!session) return;
    lastReport.current = { key: reportKey(gen, ready), gen, ahead, ready };
    void api.playbackReady(session, { gen, bufferedAheadMs: Math.round(ahead), ready }).catch(() => undefined);
  }, []);
  useEffect(() => {
    if (!state.url) return;
    const id = setInterval(() => {
      const r = lastReport.current;
      if (r.gen >= 0) postReport(r.gen, r.ahead, r.ready);
    }, REPORT_PERIOD_MS);
    return () => clearInterval(id);
  }, [state.url, postReport]);

  // Point the element at the file. Guarded on the URL so that a re-render, a
  // pause, or a new broadcast never reassigns src — which would throw away
  // the whole buffer, the one thing this design exists to keep.
  const currentUrl = useRef<string | null>(null);
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !state.url) return;
    if (currentUrl.current === state.url) return;
    currentUrl.current = state.url;
    el.src = state.url;
    el.preload = 'auto';
    // Start near where the room is rather than at zero, then let the loop
    // close the rest.
    el.currentTime = targetMs(anchorOf(state), offsetMs) / 1000;
    sync.current = initialSyncState;
    setBuffering(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.url, videoRef]);

  // Stall detection, debounced. The raw events lie: every seek fires
  // `waiting`, and turning that straight into a spinner makes the UI strobe.
  useEffect(() => {
    const el = videoRef.current;
    if (!el) return;
    const clear = () => {
      if (waitTimer.current) {
        clearTimeout(waitTimer.current);
        waitTimer.current = null;
      }
    };
    const onWait = () => {
      clear();
      waitTimer.current = setTimeout(() => {
        stalled.current = true;
        setBuffering(true);
      }, WAITING_DEBOUNCE_MS);
    };
    const onPlaying = () => {
      clear();
      waitTimer.current = setTimeout(() => {
        stalled.current = false;
        setBuffering(false);
      }, PLAYING_DEBOUNCE_MS);
    };
    el.addEventListener('waiting', onWait);
    el.addEventListener('stalled', onWait);
    el.addEventListener('playing', onPlaying);
    el.addEventListener('canplay', onPlaying);
    return () => {
      clear();
      el.removeEventListener('waiting', onWait);
      el.removeEventListener('stalled', onWait);
      el.removeEventListener('playing', onPlaying);
      el.removeEventListener('canplay', onPlaying);
    };
  }, [videoRef]);

  // The control loop.
  useEffect(() => {
    const el = videoRef.current;
    if (!el || !state.url) return;

    const id = setInterval(() => {
      const now = clientNowMs();
      const target = targetMs(anchorOf(state), offsetMs);
      const targetSec = target / 1000;
      const ahead = bufferedAheadMs(el.buffered, el.currentTime);
      const busy = el.seeking || el.readyState < 2;

      // The gate: hold the picture until there is enough to play through,
      // rather than starting and stalling a second later.
      const need = stalled.current ? RESUME_BUFFER_MS : START_BUFFER_MS;
      const gated = !state.paused && ahead < need && el.readyState < 3;
      if (gated !== buffering) setBuffering(gated);

      const errorMs = el.currentTime * 1000 - target;
      const { action, state: nextSync } = decide(
        { errorMs, now, busy, gen: state.gen, bufferedAheadMs: ahead, paused: state.paused },
        sync.current,
      );
      sync.current = nextSync;
      if (action.kind !== 'none') {
        lastCorrection.current = {
          kind: action.kind,
          reason: action.kind === 'seek' ? action.reason : formatNudge(action.rate),
          at: now,
        };
      }

      if (action.kind === 'seek') el.currentTime = targetSec;
      if (el.playbackRate !== action.rate) el.playbackRate = action.rate;

      // Pause is applied from the room's state directly, not inferred from
      // the error, so it is instant regardless of how much is buffered.
      if (state.paused) {
        if (!el.paused) el.pause();
      } else if (el.paused && !gated) {
        void el.play().catch(() => undefined);
      }

      useSyncStats.getState().report({
        errorMs,
        bufferedAheadMs: ahead,
        buffering: gated,
        rate: action.rate,
        gen: state.gen,
        offsetMs,
        lastCorrection: lastCorrection.current,
        at: now,
      });
      // A flip in readiness goes out at once rather than waiting for the next
      // heartbeat: the last person to finish buffering is the one everybody
      // else is watching a card about.
      const ready = isReady(ahead, el.currentTime * 1000, state.durationMs);
      if (reportKey(state.gen, ready) !== lastReport.current.key) postReport(state.gen, ahead, ready);
      else lastReport.current.ahead = ahead;

      onStatus?.({ errorMs, bufferedAheadMs: ahead, buffering: gated, rate: action.rate });
    }, TICK_MS);
    return () => clearInterval(id);
  }, [state, offsetMs, videoRef, buffering, onStatus, postReport]);

  return (
    <video
      ref={videoRef}
      className="stage-video"
      playsInline
      controls={false}
      disablePictureInPicture={false}
      // Volume is handled in the Web Audio graph, where the movie can be
      // boosted past 1.0 and where deafen leaves it alone. The element's own
      // volume must stay untouched or it would scale that twice.
      style={{ visibility: state.url ? 'visible' : 'hidden' }}
    />
  );
}

/** A rate nudge reads better as the percentage it is than as 1.0187. */
function formatNudge(rate: number): string {
  const pct = (rate - 1) * 100;
  return `${pct > 0 ? '+' : pct < 0 ? '−' : ''}${Math.abs(pct).toFixed(1)}%`;
}

function anchorOf(s: PlaybackState) {
  return {
    anchorPosMs: s.anchorPosMs,
    anchorAtMs: s.anchorAtMs,
    rate: s.rate,
    paused: s.paused,
    durationMs: s.durationMs,
  };
}
