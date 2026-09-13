import { getAudioContext } from './context';

/**
 * Play a short two-note test tone on a chosen output device. Uses a
 * MediaStreamDestination + <audio> element because `HTMLMediaElement.setSinkId`
 * is the widely supported way to pick a speaker (Chrome, Edge, Firefox).
 */
export async function playTestTone(deviceId?: string): Promise<void> {
  const ctx = getAudioContext();
  if (ctx.state !== 'running') await ctx.resume();
  const dest = ctx.createMediaStreamDestination();
  const gain = ctx.createGain();
  gain.gain.value = 0.0001;
  gain.connect(dest);

  const now = ctx.currentTime;
  const notes = [
    { f: 523.25, t: 0 },
    { f: 783.99, t: 0.28 },
  ];
  for (const n of notes) {
    const osc = ctx.createOscillator();
    osc.type = 'sine';
    osc.frequency.value = n.f;
    osc.connect(gain);
    osc.start(now + n.t);
    osc.stop(now + n.t + 0.5);
  }
  gain.gain.setValueAtTime(0.0001, now);
  gain.gain.exponentialRampToValueAtTime(0.25, now + 0.03);
  gain.gain.setValueAtTime(0.25, now + 0.6);
  gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.85);

  const el = new Audio();
  el.srcObject = dest.stream;
  const sinkable = el as HTMLAudioElement & { setSinkId?: (id: string) => Promise<void> };
  if (deviceId && typeof sinkable.setSinkId === 'function') {
    try {
      await sinkable.setSinkId(deviceId === 'default' ? '' : deviceId);
    } catch (e) {
      console.warn('setSinkId failed', e);
    }
  }
  await el.play();
  await new Promise((r) => setTimeout(r, 950));
  el.pause();
  el.srcObject = null;
  gain.disconnect();
}
