import { describe, expect, it } from 'vitest';
import { isImageUrl, mentionsName, parseRoomLink, tokenize } from './links';

describe('tokenize', () => {
  it('returns plain text untouched', () => {
    expect(tokenize('hello there')).toEqual([{ type: 'text', value: 'hello there' }]);
  });
  it('extracts URLs and trims trailing punctuation', () => {
    expect(tokenize('see https://example.com/a?b=1, ok')).toEqual([
      { type: 'text', value: 'see ' },
      { type: 'link', value: 'https://example.com/a?b=1', href: 'https://example.com/a?b=1' },
      { type: 'text', value: ', ok' },
    ]);
  });
  it('keeps balanced parentheses in URLs', () => {
    const t = tokenize('https://en.wikipedia.org/wiki/Foo_(bar)');
    expect(t[0]).toMatchObject({ type: 'link', href: 'https://en.wikipedia.org/wiki/Foo_(bar)' });
    const t2 = tokenize('(https://example.com)');
    expect(t2).toEqual([
      { type: 'text', value: '(' },
      { type: 'link', value: 'https://example.com', href: 'https://example.com' },
      { type: 'text', value: ')' },
    ]);
  });
  it('detects mentions of known names only, longest first', () => {
    const names = ['Jean', 'Jean Luc', 'Ada'];
    expect(tokenize('hi @Jean Luc and @ada and @nobody', names)).toEqual([
      { type: 'text', value: 'hi ' },
      { type: 'mention', value: '@Jean Luc', name: 'Jean Luc' },
      { type: 'text', value: ' and ' },
      { type: 'mention', value: '@ada', name: 'Ada' },
      { type: 'text', value: ' and @nobody' },
    ]);
  });
  it('does not treat emails as mentions', () => {
    expect(tokenize('mail me@ada.dev', ['ada'])).toEqual([{ type: 'text', value: 'mail me@ada.dev' }]);
  });
});

describe('mentionsName', () => {
  it('matches case-insensitively', () => {
    expect(mentionsName('yo @Bob!', 'bob')).toBe(true);
    expect(mentionsName('bob is here', 'bob')).toBe(false);
  });
});

describe('isImageUrl', () => {
  it('matches image extensions', () => {
    expect(isImageUrl('https://x.com/a.png')).toBe(true);
    expect(isImageUrl('https://x.com/a.GIF?x=1')).toBe(true);
    expect(isImageUrl('https://x.com/a.html')).toBe(false);
    expect(isImageUrl('not a url')).toBe(false);
  });
});

describe('parseRoomLink', () => {
  it('parses absolute links', () => {
    expect(parseRoomLink('https://watch.example.tld/r/abc123?k=inv')).toEqual({
      path: '/r/abc123?k=inv',
      roomId: 'abc123',
      kind: 'invite',
    });
    expect(parseRoomLink('https://watch.example.tld/r/abc?h=sec')?.kind).toBe('host');
    expect(parseRoomLink('watch.example.tld/r/abc?p=proj')?.kind).toBe('projector');
  });
  it('parses bare paths and rejects junk', () => {
    expect(parseRoomLink('/r/xyz')).toEqual({ path: '/r/xyz', roomId: 'xyz', kind: 'plain' });
    expect(parseRoomLink('https://example.com/other')).toBeNull();
    expect(parseRoomLink('')).toBeNull();
  });
});
