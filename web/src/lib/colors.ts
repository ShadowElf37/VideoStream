const PALETTE = [
  '#f5b942', '#f28b6b', '#e86f9a', '#b58cf5', '#6b9cf5', '#4fc3d9', '#5cd18f', '#c4d95c', '#f5a76b', '#8fa6ff',
];

/** Deterministic fallback color for an identity when the token metadata has none. */
export function colorFor(identity: string): string {
  let h = 2166136261;
  for (let i = 0; i < identity.length; i++) {
    h ^= identity.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return PALETTE[Math.abs(h) % PALETTE.length]!;
}

/** Pick black or white text for a background color. */
export function inkFor(hex: string): string {
  const m = hex.replace('#', '');
  if (m.length !== 6) return '#0b0b0d';
  const r = parseInt(m.slice(0, 2), 16);
  const g = parseInt(m.slice(2, 4), 16);
  const b = parseInt(m.slice(4, 6), 16);
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return lum > 150 ? '#0b0b0d' : '#ffffff';
}
