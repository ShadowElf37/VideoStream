import { useEffect, useState } from 'react';

/**
 * The element's buffered ranges, as plain numbers the seek bar can draw.
 *
 * `TimeRanges` is a live object that fires no events of its own, so this polls
 * — but only commits a new array when the ranges actually change. Without that
 * check the seek bar re-renders at the poll rate forever, for nothing.
 */
export function useBufferedRanges(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  enabled: boolean,
): Array<[number, number]> {
  const [ranges, setRanges] = useState<Array<[number, number]>>([]);

  useEffect(() => {
    if (!enabled) {
      setRanges([]);
      return;
    }
    let signature = '';
    const read = () => {
      const el = videoRef.current;
      if (!el) return;
      const next: Array<[number, number]> = [];
      for (let i = 0; i < el.buffered.length; i++) {
        next.push([el.buffered.start(i), el.buffered.end(i)]);
      }
      // Rounded: sub-second churn at the buffer's leading edge is not worth a
      // repaint.
      const sig = next.map(([a, b]) => `${a.toFixed(1)}-${b.toFixed(1)}`).join(',');
      if (sig !== signature) {
        signature = sig;
        setRanges(next);
      }
    };
    read();
    const id = setInterval(read, 500);
    const el = videoRef.current;
    el?.addEventListener('progress', read);
    return () => {
      clearInterval(id);
      el?.removeEventListener('progress', read);
    };
  }, [videoRef, enabled]);

  return ranges;
}
