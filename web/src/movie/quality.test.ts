import { describe, expect, it } from 'vitest';
import { AUTO, levelIndex, levelName, qualityLabel, type Level } from './quality';
import { resolveRemembered } from './source';

const levels: Level[] = [
  { index: 0, name: '1080p', height: 1080, kbps: 5000 },
  { index: 1, name: '720p', height: 720, kbps: 3000 },
];

describe('levelName', () => {
  it('matches the names vspush writes, so the menu and the files agree', () => {
    expect(levelName(1080, 0)).toBe('1080p');
    expect(levelName(720, 1)).toBe('720p');
  });

  it('falls back to a position when the height is unknown', () => {
    expect(levelName(0, 2)).toBe('level 3');
  });
});

describe('levelIndex', () => {
  it('maps a name onto the player numbering, and Auto onto -1', () => {
    expect(levelIndex(levels, AUTO)).toBe(-1);
    expect(levelIndex(levels, '720p')).toBe(1);
  });

  it('treats a name this title does not have as Auto', () => {
    // A 720p source has no 1080p rendition; the remembered choice must not
    // select something that is not there.
    expect(levelIndex(levels, '2160p')).toBe(-1);
  });
});

describe('resolveRemembered', () => {
  it('keeps a choice the title can honour and drops one it cannot', () => {
    expect(resolveRemembered(levels, '720p')).toBe('720p');
    expect(resolveRemembered(levels, AUTO)).toBe(AUTO);
    expect(resolveRemembered(levels, '2160p')).toBe(AUTO);
    expect(resolveRemembered([], '720p')).toBe(AUTO);
  });
});

describe('qualityLabel', () => {
  it('says what Auto resolved to, which is the thing a viewer cannot check', () => {
    expect(qualityLabel(AUTO, levels, 1)).toBe('Auto · 720p');
    expect(qualityLabel(AUTO, levels, -1)).toBe('Auto');
  });

  it('shows a manual choice as itself', () => {
    expect(qualityLabel('1080p', levels, 1)).toBe('1080p');
  });
});
