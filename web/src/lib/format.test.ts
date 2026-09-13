import { describe, expect, it } from 'vitest';
import { formatBytes, formatDelay, formatTime, initials, relativeTime, trackLabel } from './format';

describe('formatTime', () => {
  it('formats minutes and seconds', () => {
    expect(formatTime(0)).toBe('0:00');
    expect(formatTime(5)).toBe('0:05');
    expect(formatTime(65)).toBe('1:05');
    expect(formatTime(754.9)).toBe('12:34');
  });
  it('formats hours with zero-padded minutes', () => {
    expect(formatTime(3600)).toBe('1:00:00');
    expect(formatTime(3661)).toBe('1:01:01');
    expect(formatTime(7322)).toBe('2:02:02');
  });
  it('clamps garbage to 0:00', () => {
    expect(formatTime(-3)).toBe('0:00');
    expect(formatTime(NaN)).toBe('0:00');
    expect(formatTime(Infinity)).toBe('0:00');
  });
});

describe('formatDelay', () => {
  it('signs and rounds', () => {
    expect(formatDelay(0.1)).toBe('+0.10 s');
    expect(formatDelay(-0.05)).toBe('−0.05 s');
    expect(formatDelay(0)).toBe('0.00 s');
    expect(formatDelay(0.30000000004, 1)).toBe('+0.3 s');
  });
});

describe('relativeTime', () => {
  const now = new Date(2026, 2, 10, 15, 0, 0).getTime();
  it('says now for fresh messages', () => {
    expect(relativeTime(now - 10_000, now)).toBe('now');
  });
  it('uses minutes under an hour', () => {
    expect(relativeTime(now - 5 * 60_000, now)).toBe('5m');
    expect(relativeTime(now - 59 * 60_000, now)).toBe('59m');
  });
  it('uses clock time same day and Yesterday prefix', () => {
    expect(relativeTime(now - 2 * 3_600_000, now)).toBe('13:00');
    expect(relativeTime(now - 24 * 3_600_000, now)).toBe('Yesterday 15:00');
  });
  it('falls back to a date for older', () => {
    const older = new Date(2026, 2, 5, 15, 0, 0).getTime();
    expect(relativeTime(older, now)).toMatch(/Mar 5 15:00/);
  });
});

describe('initials', () => {
  it('handles one and two names', () => {
    expect(initials('bob')).toBe('B');
    expect(initials('Ada Lovelace')).toBe('AL');
    expect(initials('  jean luc picard ')).toBe('JP');
    expect(initials('')).toBe('?');
  });
  it('handles multi-byte characters', () => {
    expect(initials('Émile Zola')).toBe('ÉZ');
    expect(initials('🦊 fox')).toBe('🦊F');
  });
});

describe('formatBytes', () => {
  it('scales', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(3 * 1024 ** 3)).toBe('3.0 GB');
    expect(formatBytes(-1)).toBe('');
  });
});

describe('trackLabel', () => {
  it('composes title, lang and codec', () => {
    expect(trackLabel({ id: 2, lang: 'en', title: 'Full', codec: 'ass' })).toBe('Full · EN · ass');
    expect(trackLabel({ id: 3 })).toBe('Track 3');
    expect(trackLabel({ id: 1, lang: 'ja' })).toBe('JA');
  });
});
