import { describe, expect, it } from 'vitest';
import type { PlaybackIntent } from '@/proto/messages';
import { describeIntent, intentSystemLine, intentToast, SMALL_SEEK_MS } from './intent';

function intent(p: Partial<PlaybackIntent>): PlaybackIntent {
  return {
    seq: 1,
    action: 'seek',
    actor: { identity: 'u_alice', name: 'Alice', color: '#8ab4f8' },
    fromMs: 0,
    toMs: 0,
    ts: 1_700_000_000_000,
    ...p,
  };
}

describe('describeIntent', () => {
  it('gives a small seek a corner chip naming the actor', () => {
    const d = describeIntent(intent({ fromMs: 60_000, toMs: 50_000 }));
    expect(d).toEqual({ kind: 'chip', text: '⏪ 10 s · Alice' });
    expect(describeIntent(intent({ fromMs: 50_000, toMs: 60_000 }))).toEqual({
      kind: 'chip',
      text: '⏩ 10 s · Alice',
    });
  });

  it('drops the name when nobody is credited', () => {
    const d = describeIntent(intent({ fromMs: 60_000, toMs: 55_000, actor: { identity: '', name: '', color: '' } }));
    expect(d).toEqual({ kind: 'chip', text: '⏪ 5 s' });
  });

  it('sends a jump to the middle of the screen with where it landed', () => {
    const d = describeIntent(intent({ fromMs: 0, toMs: 4_350_000 }));
    expect(d).toEqual({ kind: 'glyph', forward: true, time: '1:12:30', who: 'Alice' });
  });

  it('puts the boundary on the chip side', () => {
    expect(describeIntent(intent({ fromMs: 0, toMs: SMALL_SEEK_MS })).kind).toBe('chip');
    expect(describeIntent(intent({ fromMs: 0, toMs: SMALL_SEEK_MS + 1 })).kind).toBe('glyph');
  });

  it('never reports a seek as zero seconds', () => {
    // A 400 ms nudge is still a seek somebody did; "0 s" reads as a bug.
    const d = describeIntent(intent({ fromMs: 0, toMs: 400 }));
    expect(d).toEqual({ kind: 'chip', text: '⏩ 1 s · Alice' });
  });

  it('turns a load into a card naming the film', () => {
    const d = describeIntent(intent({ action: 'load', title: 'Blade Runner', mediaId: 'blade_runner' }));
    expect(d).toEqual({ kind: 'loading', title: 'Blade Runner', who: 'Alice' });
  });

  it('has nothing to draw for a pause, a play or nothing at all', () => {
    expect(describeIntent(intent({ action: 'pause' })).kind).toBe('none');
    expect(describeIntent(intent({ action: 'play' })).kind).toBe('none');
    expect(describeIntent(null).kind).toBe('none');
  });
});

describe('intentToast', () => {
  it('names the actor for pause and play', () => {
    expect(intentToast(intent({ action: 'pause' }), 'u_bob')).toBe('Alice paused');
    expect(intentToast(intent({ action: 'play' }), 'u_bob')).toBe('Alice resumed');
    expect(intentToast(intent({ action: 'stop' }), 'u_bob')).toBe('Alice stopped playback');
  });

  it('says nothing to the person who pressed the button', () => {
    expect(intentToast(intent({ action: 'pause' }), 'u_alice')).toBeNull();
  });

  it('says nothing for a seek, which has its own echo', () => {
    expect(intentToast(intent({ action: 'seek', toMs: 500_000 }), 'u_bob')).toBeNull();
  });

  it('says nothing when there is no actor to name', () => {
    expect(intentToast(intent({ action: 'pause', actor: { identity: '', name: '', color: '' } }), 'u_bob')).toBeNull();
  });
});

describe('intentSystemLine', () => {
  it('names the actor and the time', () => {
    expect(intentSystemLine(intent({ action: 'pause', toMs: 754_000 }))).toBe('Alice paused at 12:34');
    expect(intentSystemLine(intent({ action: 'play', toMs: 754_000 }))).toBe('Alice resumed at 12:34');
    expect(intentSystemLine(intent({ action: 'seek', fromMs: 0, toMs: 4_350_000 }))).toBe('Alice skipped to 1:12:30');
    expect(intentSystemLine(intent({ action: 'seek', fromMs: 60_000, toMs: 50_000 }))).toBe('Alice skipped back 10 s');
    expect(intentSystemLine(intent({ action: 'load', title: 'Blade Runner' }))).toBe('Alice started Blade Runner');
  });

  it('stays neutral when the server did it', () => {
    const server = { identity: '', name: '', color: '' };
    expect(intentSystemLine(intent({ action: 'load', title: 'Aliens', actor: server }))).toBe('Now playing: Aliens');
    expect(intentSystemLine(intent({ action: 'pause', toMs: 0, actor: server }))).toBe('Paused at 0:00');
  });
});
