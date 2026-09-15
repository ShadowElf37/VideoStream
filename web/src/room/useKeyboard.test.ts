import { describe, expect, it, vi, type Mock } from 'vitest';
import { routeKeyDown, routeKeyUp, type GlobalKeyHandlers, type KeyLike } from './useKeyboard';

type Mocked = { [K in keyof GlobalKeyHandlers]: Mock<GlobalKeyHandlers[K]> };

function handlers(): Mocked {
  return {
    toggleMic: vi.fn<() => void>(),
    toggleDeafen: vi.fn<() => void>(),
    toggleMovieMuted: vi.fn<() => void>(),
    toggleReactions: vi.fn<() => void>(),
    toggleFullscreen: vi.fn<() => void>(),
    toggleSidebar: vi.fn<() => void>(),
    pttDown: vi.fn<() => void>(),
    pttUp: vi.fn<() => void>(),
    openHelp: vi.fn<() => void>(),
    playPause: vi.fn<() => void>(),
    seek: vi.fn<(ms: number) => void>(),
    speedStep: vi.fn<(dir: 1 | -1) => void>(),
    cycleSubs: vi.fn<() => void>(),
    cycleAudio: vi.fn<() => void>(),
    subDelay: vi.fn<(seconds: number) => void>(),
  };
}

function key(k: string, over: Partial<Omit<KeyLike, 'preventDefault'>> = {}): KeyLike & { preventDefault: Mock<() => void> } {
  return { key: k, repeat: false, metaKey: false, ctrlKey: false, altKey: false, target: null, preventDefault: vi.fn<() => void>(), ...over };
}

describe('routeKeyDown', () => {
  it('Space is play/pause and is claimed so a focused button cannot take it', () => {
    const h = handlers();
    const e = key(' ');
    routeKeyDown(e, h);
    expect(h.playPause).toHaveBeenCalledTimes(1);
    expect(e.preventDefault).toHaveBeenCalled();
  });

  it('a repeating Space stays claimed but does not toggle again', () => {
    const h = handlers();
    const e = key(' ', { repeat: true });
    routeKeyDown(e, h);
    expect(h.playPause).not.toHaveBeenCalled();
    expect(e.preventDefault).toHaveBeenCalled();
  });

  it('arrows seek and may repeat', () => {
    const h = handlers();
    routeKeyDown(key('ArrowLeft', { repeat: true }), h);
    routeKeyDown(key('ArrowRight'), h);
    routeKeyDown(key('ArrowUp'), h);
    routeKeyDown(key('ArrowDown'), h);
    expect(h.seek.mock.calls).toEqual([[-5000], [5000], [60_000], [-60_000]]);
  });

  it('letter keys ignore auto-repeat', () => {
    const h = handlers();
    routeKeyDown(key('m', { repeat: true }), h);
    expect(h.toggleMic).not.toHaveBeenCalled();
    routeKeyDown(key('M'), h);
    expect(h.toggleMic).toHaveBeenCalledTimes(1);
  });

  it('leaves modifier chords to the browser', () => {
    const h = handlers();
    const e = key(' ', { metaKey: true });
    routeKeyDown(e, h);
    expect(h.playPause).not.toHaveBeenCalled();
    expect(e.preventDefault).not.toHaveBeenCalled();
  });

  it('unbound keys are not claimed', () => {
    const h = handlers();
    const e = key('q');
    routeKeyDown(e, h);
    expect(e.preventDefault).not.toHaveBeenCalled();
  });

  it('mpv extras route with their direction', () => {
    const h = handlers();
    routeKeyDown(key('['), h);
    routeKeyDown(key(']'), h);
    routeKeyDown(key('z'), h);
    routeKeyDown(key('x'), h);
    routeKeyDown(key('j'), h);
    routeKeyDown(key('#'), h);
    expect(h.speedStep.mock.calls).toEqual([[-1], [1]]);
    expect(h.subDelay.mock.calls).toEqual([[-0.1], [0.1]]);
    expect(h.cycleSubs).toHaveBeenCalledTimes(1);
    expect(h.cycleAudio).toHaveBeenCalledTimes(1);
  });
});

describe('routeKeyUp', () => {
  it('releases push-to-talk and keeps Space away from the focused button', () => {
    const h = handlers();
    routeKeyUp(key('v'), h);
    expect(h.pttUp).toHaveBeenCalledTimes(1);
    const e = key(' ');
    routeKeyUp(e, h);
    expect(e.preventDefault).toHaveBeenCalled();
  });
});
