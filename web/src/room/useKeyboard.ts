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
  /** Space. Play/pause for the host, a pause request for everyone else. */
  playPause: () => void;
  /** Arrows. Relative, in milliseconds. */
  seek: (ms: number) => void;
  /** mpv-only extras; the handler makes them no-ops on the hosted path. */
  speedStep: (dir: 1 | -1) => void;
  cycleSubs: () => void;
  cycleAudio: () => void;
  subDelay: (seconds: number) => void;
}

/** The part of a KeyboardEvent the router reads, so tests need no DOM. */
export interface KeyLike {
  key: string;
  repeat: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  target: EventTarget | null;
  preventDefault(): void;
}

/**
 * Where a key press is allowed to mean something else.
 *
 * Text fields, obviously. Dialogs and menus too: they are overlays that
 * announce their own context and trap focus, so the person can see that the
 * rules changed. Nothing else does — not the button that happens to be the
 * last thing clicked, not a slider, not the sidebar. Space is play/pause
 * wherever the focus ring ended up.
 */
export function yieldsKeys(target: EventTarget | null): boolean {
  if (isTypingTarget(target)) return true;
  if (typeof Element === 'undefined' || !(target instanceof Element)) return false;
  return !!target.closest('[role="dialog"], [role="menu"], [role="listbox"]');
}

export function routeKeyDown(e: KeyLike, h: GlobalKeyHandlers): void {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (yieldsKeys(e.target)) return;
  // Transport keys first: they may auto-repeat, and Space is claimed on every
  // press so a focused button never gets to treat it as a click.
  switch (e.key) {
    case ' ':
      if (!e.repeat) h.playPause();
      e.preventDefault();
      return;
    case 'ArrowLeft':
      h.seek(-5000);
      e.preventDefault();
      return;
    case 'ArrowRight':
      h.seek(5000);
      e.preventDefault();
      return;
    case 'ArrowUp':
      h.seek(60_000);
      e.preventDefault();
      return;
    case 'ArrowDown':
      h.seek(-60_000);
      e.preventDefault();
      return;
  }
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
    case '[':
      h.speedStep(-1);
      break;
    case ']':
      h.speedStep(1);
      break;
    case 'j':
      h.cycleSubs();
      break;
    case '#':
      h.cycleAudio();
      break;
    case 'z':
      h.subDelay(-0.1);
      break;
    case 'x':
      h.subDelay(0.1);
      break;
    case '?':
      h.openHelp();
      break;
    default:
      return;
  }
  e.preventDefault();
}

export function routeKeyUp(e: KeyLike, h: GlobalKeyHandlers): void {
  if (e.key.toLowerCase() === 'v') h.pttUp();
  // A focused button activates on the keyup of Space in some browsers
  // (Firefox), whatever happened to the keydown.
  if (e.key === ' ' && !yieldsKeys(e.target)) e.preventDefault();
}

/**
 * The room's one keymap. Every key means the same thing wherever the last
 * click landed; only what `yieldsKeys` lists takes them away. PTT (`v`) is
 * hold-to-talk: keydown enables, keyup disables; auto-repeat is filtered.
 *
 * Listens in the capture phase so it runs before the focused element's own
 * handler, and marks the event handled (`preventDefault`) rather than
 * swallowing it: the focused widget then stands down, and anything else
 * listening on the window — the transport bar's auto-hide — still sees it.
 */
export function useGlobalKeys(h: GlobalKeyHandlers, enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    const down = (e: KeyboardEvent) => routeKeyDown(e, h);
    const up = (e: KeyboardEvent) => routeKeyUp(e, h);
    const blur = () => h.pttUp();
    window.addEventListener('keydown', down, true);
    window.addEventListener('keyup', up, true);
    window.addEventListener('blur', blur);
    return () => {
      window.removeEventListener('keydown', down, true);
      window.removeEventListener('keyup', up, true);
      window.removeEventListener('blur', blur);
    };
  }, [h, enabled]);
}

export const SHORTCUTS: Array<[string, string]> = [
  ['Space', 'Play / pause (viewers: request a pause)'],
  ['← →', '±5 s (host)'],
  ['↑ ↓', '±60 s (host)'],
  ['M', 'Mute / unmute mic'],
  ['D', 'Deafen (voices only)'],
  ['S', 'Mute movie audio'],
  ['V (hold)', 'Push to talk, when enabled'],
  ['R', 'Reactions'],
  ['F', 'Fullscreen'],
  ['C', 'Toggle sidebar'],
  ['[ ]', 'Slower / faster (projector mode)'],
  ['J', 'Cycle subtitles (projector mode)'],
  ['#', 'Cycle audio track (projector mode)'],
  ['Z X', 'Subtitle delay −/+ 0.1 s (projector mode)'],
];
