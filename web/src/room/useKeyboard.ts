import { useEffect } from 'react';
import { isTypingTarget } from '@/lib/platform';

export interface GlobalKeyHandlers {
  toggleMic: () => void;
  toggleDeafen: () => void;
  toggleMovieMuted: () => void;
  toggleReactions: () => void;
  toggleFullscreen: () => void;
  toggleSidebar: () => void;
  pttDown: () => void;
  pttUp: () => void;
  openHelp: () => void;
}

/**
 * Room-wide shortcuts. Ignored while typing. PTT (`v`) is hold-to-talk:
 * keydown enables, keyup disables; auto-repeat is filtered.
 */
export function useGlobalKeys(h: GlobalKeyHandlers, enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    const down = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;
      const k = e.key.toLowerCase();
      if (k === 'v') {
        if (!e.repeat) h.pttDown();
        e.preventDefault();
        return;
      }
      if (e.repeat) return;
      switch (k) {
        case 'm':
          h.toggleMic();
          break;
        case 'd':
          h.toggleDeafen();
          break;
        case 's':
          h.toggleMovieMuted();
          break;
        case 'r':
          h.toggleReactions();
          break;
        case 'f':
          h.toggleFullscreen();
          break;
        case 'c':
          h.toggleSidebar();
          break;
        case '?':
          h.openHelp();
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    const up = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'v') h.pttUp();
    };
    const blur = () => h.pttUp();
    window.addEventListener('keydown', down);
    window.addEventListener('keyup', up);
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('keydown', down);
      window.removeEventListener('keyup', up);
      window.removeEventListener('blur', blur);
    };
  }, [h, enabled]);
}

export const SHORTCUTS: Array<[string, string]> = [
  ['M', 'Mute / unmute mic'],
  ['D', 'Deafen (voices only)'],
  ['S', 'Mute movie audio'],
  ['V (hold)', 'Push to talk, when enabled'],
  ['R', 'Reactions'],
  ['F', 'Fullscreen'],
  ['C', 'Toggle sidebar'],
  ['Space', 'Play / pause (host, stage focused)'],
  ['← →', '±5 s (host)'],
  ['↑ ↓', '±60 s (host)'],
  ['[ ]', 'Slower / faster (host)'],
  ['J', 'Cycle subtitles (host)'],
  ['#', 'Cycle audio track (host)'],
  ['Z X', 'Subtitle delay −/+ 0.1 s (host)'],
];
