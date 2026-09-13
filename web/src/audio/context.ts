/**
 * One AudioContext for the whole page. It is created (and resumed) on the
 * Join click so Safari's autoplay policy is satisfied once, and it is handed
 * to LiveKit (`webAudioMix`) so every remote track sits behind our gain nodes.
 */
let ctx: AudioContext | null = null;

export function getAudioContext(): AudioContext {
  if (!ctx) {
    const Ctor = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    ctx = new Ctor({ latencyHint: 'playback' });
  }
  return ctx;
}

/** Resume the shared context from a user gesture. Safe to call repeatedly. */
export async function unlockAudio(): Promise<AudioContext> {
  const c = getAudioContext();
  if (c.state !== 'running') {
    try {
      await c.resume();
    } catch (e) {
      console.warn('AudioContext.resume failed', e);
    }
  }
  return c;
}

/** Route the shared context to a specific output device when the browser allows it. */
export async function setContextSink(deviceId: string): Promise<boolean> {
  const c = getAudioContext() as AudioContext & { setSinkId?: (id: string) => Promise<void> };
  if (typeof c.setSinkId !== 'function') return false;
  try {
    await c.setSinkId(deviceId === 'default' ? '' : deviceId);
    return true;
  } catch (e) {
    console.warn('AudioContext.setSinkId failed', e);
    return false;
  }
}
