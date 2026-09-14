import { describe, expect, it } from 'vitest';
import {
  DRIFT_BAD_MS,
  DRIFT_WARN_MS,
  driftTone,
  formatAhead,
  formatCorrection,
  formatDrift,
  formatRate,
} from './syncStats';

describe('driftTone', () => {
  it('is ok inside the deadband and bad past a jump', () => {
    expect(driftTone(0)).toBe('ok');
    expect(driftTone(-99)).toBe('ok');
    expect(driftTone(DRIFT_WARN_MS)).toBe('warn');
    expect(driftTone(-250)).toBe('warn');
    expect(driftTone(DRIFT_BAD_MS)).toBe('bad');
    expect(driftTone(-1200)).toBe('bad');
  });

  it('treats a non-finite reading as bad rather than as fine', () => {
    expect(driftTone(NaN)).toBe('bad');
  });
});

describe('formatDrift', () => {
  it('always carries the sign', () => {
    expect(formatDrift(123.4)).toBe('+123 ms');
    expect(formatDrift(-42.6)).toBe('−43 ms');
    expect(formatDrift(0)).toBe('0 ms');
    expect(formatDrift(-0.2)).toBe('0 ms');
  });
});

describe('formatAhead', () => {
  it('gains precision as the buffer shallows', () => {
    expect(formatAhead(0)).toBe('0.0 s');
    expect(formatAhead(2400)).toBe('2.4 s');
    expect(formatAhead(31_500)).toBe('32 s');
    expect(formatAhead(-1)).toBe('—');
  });
});

describe('formatRate', () => {
  it('shows enough digits to catch a stale nudge', () => {
    expect(formatRate(1)).toBe('1.0000');
    expect(formatRate(1.003)).toBe('1.0030');
  });
});

describe('formatCorrection', () => {
  it('reads as what happened and how long ago', () => {
    expect(formatCorrection(null, 1000)).toBe('none');
    expect(formatCorrection({ kind: 'seek', reason: 'drift', at: 1000 }, 4000)).toBe('seek · drift · 3s ago');
    expect(formatCorrection({ kind: 'rate', reason: '+1.9%', at: 1000 }, 1000)).toBe('rate · +1.9% · 0s ago');
  });
});
