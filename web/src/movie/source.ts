import { isSafari } from '@/lib/platform';
import { AUTO, levelIndex, levelName, useQuality, type Level } from './quality';

/**
 * Pointing the <video> element at whatever the director handed us.
 *
 * Three cases, and the branching is unavoidable because browsers genuinely
 * differ here:
 *
 *   - an HLS master playlist through hls.js, which is Chrome and Firefox;
 *   - the same playlist natively on Safari, which does it better than any
 *     library can and, on iOS, is the only way at all;
 *   - a plain MP4, for a title pushed before renditions existed. Its file is
 *     not fragmented, so there is nothing to make a playlist out of, and
 *     progressive download is exactly what it was written for.
 *
 * hls.js is loaded on demand. It is a couple of hundred kilobytes that Safari
 * never needs and that nobody needs before a film is picked, so making it part
 * of the shell would be a cost paid by everyone for a minority of sessions.
 *
 * What does *not* change across the three is the thing that matters: the sync
 * control loop drives `currentTime` and `playbackRate` on the element, and
 * those mean the same thing under MSE as over a file.
 */

export interface MovieSource {
  destroy(): void;
}

/** True when the browser can play HLS without help. */
export function supportsNativeHls(el: HTMLVideoElement): boolean {
  return el.canPlayType('application/vnd.apple.mpegurl') !== '';
}

/**
 * preferNative decides which of the two HLS paths to take.
 *
 * `canPlayType` on its own is no longer the discriminator everyone's snippets
 * assume: current Chromium answers "maybe" for HLS and really does play it,
 * which would silently take Chrome down the native path and lose the level
 * API the quality menu is built on. So the rule is Safari-first — it plays
 * HLS better than a library can, and on iOS there is no MSE to run one in —
 * and hls.js for everyone else who can support it.
 */
export function preferNative(el: HTMLVideoElement): boolean {
  return isSafari && supportsNativeHls(el);
}

export function isPlaylist(url: string): boolean {
  return url.split('?')[0]!.endsWith('.m3u8');
}

/**
 * attachSource points the element at a URL and returns a handle that undoes
 * it. Asynchronous only because of the dynamic import; the element is
 * assigned in the same task for the two paths that do not need it.
 */
export async function attachSource(el: HTMLVideoElement, url: string, startAtSec: number): Promise<MovieSource> {
  if (isPlaylist(url) && !preferNative(el)) {
    const hls = await attachHls(el, url, startAtSec);
    if (hls) return hls;
  }
  // Native HLS and plain MP4 are the same code: hand the URL to the element.
  // Neither offers a rendition to pick — Safari adapts on its own and an MP4
  // has nothing to adapt between — so the quality menu has nothing to show.
  useQuality.getState().clear();
  el.src = url;
  el.preload = 'auto';
  el.currentTime = startAtSec;
  return {
    destroy() {
      el.removeAttribute('src');
      el.load();
    },
  };
}

async function attachHls(el: HTMLVideoElement, url: string, startAtSec: number): Promise<MovieSource | null> {
  const { default: Hls } = await import('hls.js');
  if (!Hls.isSupported()) return null;

  const hls = new Hls({
    // A deep buffer is the whole point of hosted playback: the browser owns
    // it, keeps it, and rides out shaky Wi-Fi with it. hls.js's defaults are
    // tuned for live streaming and are far shallower than this wants.
    maxBufferLength: 60,
    maxMaxBufferLength: 600,
    backBufferLength: 30,
    lowLatencyMode: false,
    // The sync loop seeks constantly and expects a seek to land. Fragments
    // are ~2 s, so a handful of retries is a second, not a minute.
    fragLoadPolicy: {
      default: {
        maxTimeToFirstByteMs: 10_000,
        maxLoadTimeMs: 60_000,
        timeoutRetry: { maxNumRetry: 2, retryDelayMs: 200, maxRetryDelayMs: 1000 },
        errorRetry: { maxNumRetry: 4, retryDelayMs: 200, maxRetryDelayMs: 2000 },
      },
    },
  });

  hls.on(Hls.Events.MANIFEST_PARSED, () => {
    const levels: Level[] = hls.levels.map((l, index) => ({
      index,
      name: levelName(l.height, index),
      height: l.height,
      kbps: Math.round((l.bitrate || 0) / 1000),
    }));
    // Largest first, to read the way the menu is written.
    levels.sort((a, b) => b.height - a.height);
    useQuality.getState().set(levels, (name) => {
      hls.currentLevel = levelIndex(levels, name);
    });
    // Start near where the room is rather than at zero. The manifest has to
    // be parsed first or there is no timeline to seek within.
    if (startAtSec > 0) el.currentTime = startAtSec;
  });

  hls.on(Hls.Events.LEVEL_SWITCHED, (_e, data) => {
    useQuality.getState().setActive(data.level);
  });

  hls.on(Hls.Events.ERROR, (_e, data) => {
    if (!data.fatal) return;
    // The recoveries hls.js documents, in order. A fatal error left alone is
    // a frozen picture for the rest of the film.
    switch (data.type) {
      case Hls.ErrorTypes.NETWORK_ERROR:
        hls.startLoad();
        break;
      case Hls.ErrorTypes.MEDIA_ERROR:
        hls.recoverMediaError();
        break;
      default:
        hls.destroy();
    }
  });

  hls.loadSource(url);
  hls.attachMedia(el);

  return {
    destroy() {
      useQuality.getState().clear();
      hls.destroy();
    },
  };
}

/** The remembered preference, validated against what is actually on offer. */
export function resolveRemembered(levels: Level[], remembered: string): string {
  if (remembered === AUTO) return AUTO;
  return levels.some((l) => l.name === remembered) ? remembered : AUTO;
}
