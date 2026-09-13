/**
 * A tiny RMS level meter over an AnalyserNode. `read()` returns 0..1 with a
 * gentle log curve so quiet speech is visible.
 */
export interface Meter {
  read: () => number;
  stop: () => void;
}

export function createMeter(ctx: AudioContext, stream: MediaStream): Meter {
  const source = ctx.createMediaStreamSource(stream);
  const analyser = ctx.createAnalyser();
  analyser.fftSize = 1024;
  analyser.smoothingTimeConstant = 0.5;
  source.connect(analyser);
  const buf = new Float32Array(analyser.fftSize);
  let peak = 0;

  return {
    read() {
      analyser.getFloatTimeDomainData(buf);
      let sum = 0;
      for (let i = 0; i < buf.length; i++) sum += buf[i]! * buf[i]!;
      const rms = Math.sqrt(sum / buf.length);
      // -60 dBFS .. 0 dBFS mapped to 0..1
      const db = 20 * Math.log10(Math.max(rms, 1e-6));
      const lvl = Math.min(1, Math.max(0, (db + 60) / 60));
      // fast attack, slow release so the bar doesn't flicker
      peak = lvl > peak ? lvl : peak * 0.85;
      return peak;
    },
    stop() {
      try {
        source.disconnect();
        analyser.disconnect();
      } catch {
        /* already gone */
      }
    },
  };
}
