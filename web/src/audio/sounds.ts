import { getAudioContext } from './context';

/** Soft notification "tick" synthesised in-page: no asset, no fetch. */
export function playNotification(volume = 0.12): void {
  try {
    const ctx = getAudioContext();
    if (ctx.state !== 'running') return;
    const now = ctx.currentTime;
    const gain = ctx.createGain();
    gain.connect(ctx.destination);
    gain.gain.setValueAtTime(0.0001, now);
    gain.gain.exponentialRampToValueAtTime(volume, now + 0.01);
    gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.22);
    const osc = ctx.createOscillator();
    osc.type = 'triangle';
    osc.frequency.setValueAtTime(880, now);
    osc.frequency.exponentialRampToValueAtTime(1320, now + 0.08);
    osc.connect(gain);
    osc.start(now);
    osc.stop(now + 0.25);
  } catch {
    /* audio not available */
  }
}
