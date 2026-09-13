/** Seconds → "m:ss" or "h:mm:ss". Negative / NaN clamp to 0:00. */
export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0;
  const s = Math.floor(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
  return `${h > 0 ? `${h}:` : ''}${mm}:${String(sec).padStart(2, '0')}`;
}

/** Signed seconds with one decimal, for delays: "+0.1 s", "−0.05 s". */
export function formatDelay(seconds: number, digits = 2): string {
  const rounded = Number(seconds.toFixed(digits));
  const sign = rounded > 0 ? '+' : rounded < 0 ? '−' : '';
  return `${sign}${Math.abs(rounded).toFixed(digits)} s`;
}

/** Relative timestamp for chat: "now", "3m", "2h", "Yesterday 14:02", "Mar 3". */
export function relativeTime(ts: number, now: number = Date.now()): string {
  const diff = now - ts;
  if (diff < 45_000) return 'now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
  const d = new Date(ts);
  const n = new Date(now);
  const sameDay = d.toDateString() === n.toDateString();
  const hhmm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  if (sameDay) return hhmm;
  const yesterday = new Date(n);
  yesterday.setDate(n.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return `Yesterday ${hhmm}`;
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${hhmm}`;
}

/** Full timestamp for tooltips. */
export function absoluteTime(ts: number): string {
  return new Date(ts).toLocaleString();
}

/** Initials for an avatar: "Ada Lovelace" → "AL", "bob" → "B", "" → "?". */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const first = parts[0]!;
  if (parts.length === 1) return Array.from(first)[0]!.toUpperCase();
  const last = parts[parts.length - 1]!;
  return (Array.from(first)[0]! + Array.from(last)[0]!).toUpperCase();
}

/** Bytes → "1.2 GB". */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/** kbps → "8.0 Mbps" / "640 kbps". */
export function formatBitrate(kbps: number): string {
  if (!Number.isFinite(kbps) || kbps <= 0) return '—';
  return kbps >= 1000 ? `${(kbps / 1000).toFixed(1)} Mbps` : `${Math.round(kbps)} kbps`;
}

export function clamp(n: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, n));
}

/** Label for an mpv track: "English (Full) · SRT". */
export function trackLabel(t: { id: number; lang?: string; title?: string; codec?: string }): string {
  const bits: string[] = [];
  if (t.title) bits.push(t.title);
  if (t.lang) bits.push(t.lang.toUpperCase());
  if (bits.length === 0) bits.push(`Track ${t.id}`);
  const s = bits.join(' · ');
  return t.codec ? `${s} · ${t.codec}` : s;
}
