import { describe, expect, it } from 'vitest';
import { isReady, reportKey } from './ready';
import { START_BUFFER_MS } from './sync';

describe('isReady', () => {
  it('is the start gate: enough buffered to play through', () => {
    expect(isReady(0, 0, 600_000)).toBe(false);
    expect(isReady(START_BUFFER_MS - 1, 0, 600_000)).toBe(false);
    expect(isReady(START_BUFFER_MS, 0, 600_000)).toBe(true);
  });

  it('counts buffered-to-the-end as ready', () => {
    // The last two seconds of a film can never have three ahead of them; the
    // room would otherwise hold at the credits until the timeout.
    expect(isReady(2000, 598_000, 600_000)).toBe(true);
    expect(isReady(2000, 500_000, 600_000)).toBe(false);
  });

  it('does not treat an unknown duration as the end', () => {
    expect(isReady(0, 0, 0)).toBe(false);
  });
});

describe('reportKey', () => {
  it('changes on a flip and on a new generation, and not otherwise', () => {
    expect(reportKey(4, true)).toBe(reportKey(4, true));
    expect(reportKey(4, true)).not.toBe(reportKey(4, false));
    expect(reportKey(4, true)).not.toBe(reportKey(5, true));
  });
});
