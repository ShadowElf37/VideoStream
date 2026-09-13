import { describe, expect, it } from 'vitest';
import {
  dbToGain,
  initialAudioState as init,
  micEnabled,
  movieGain,
  presenceOf,
  reduce,
  voiceGain,
  type AudioAction,
  type AudioState,
} from './model';

const run = (actions: AudioAction[], start: AudioState = init) => actions.reduce(reduce, start);

describe('mic', () => {
  it('toggles independently of everything else', () => {
    const s = run([{ type: 'toggleMic' }]);
    expect(s.micMuted).toBe(true);
    expect(micEnabled(s)).toBe(false);
    expect(movieGain(s)).toBe(1);
    expect(voiceGain(s, 'x')).toBe(1);
    expect(micEnabled(run([{ type: 'toggleMic' }], s))).toBe(true);
  });
  it('returns the same object for no-op actions', () => {
    expect(reduce(init, { type: 'setMicMuted', muted: false })).toBe(init);
    expect(reduce(init, { type: 'setDucking', ducking: false })).toBe(init);
  });
});

describe('deafen', () => {
  it('silences voices but not the movie', () => {
    const s = run([{ type: 'setVoiceVolume', identity: 'bob', volume: 1.5 }, { type: 'toggleDeafen' }]);
    expect(voiceGain(s, 'bob')).toBe(0);
    expect(voiceGain(s, 'alice')).toBe(0);
    expect(movieGain(s)).toBe(1);
    expect(micEnabled(s)).toBe(true);
    const back = reduce(s, { type: 'toggleDeafen' });
    expect(voiceGain(back, 'bob')).toBe(1.5);
  });

  it('with deafenImpliesMute mutes the mic and restores the prior state on undeafen', () => {
    const on = run([{ type: 'setDeafenImpliesMute', value: true }]);
    const deaf = reduce(on, { type: 'toggleDeafen' });
    expect(deaf.micMuted).toBe(true);
    expect(micEnabled(deaf)).toBe(false);
    const undeaf = reduce(deaf, { type: 'toggleDeafen' });
    expect(undeaf.micMuted).toBe(false);
    expect(micEnabled(undeaf)).toBe(true);

    // was muted before deafening: stays muted after
    const mutedFirst = run([{ type: 'toggleMic' }, { type: 'toggleDeafen' }, { type: 'toggleDeafen' }], on);
    expect(mutedFirst.micMuted).toBe(true);
    expect(mutedFirst.deafened).toBe(false);
  });

  it('unmuting while deafened (implied mute) undeafens', () => {
    const s = run([{ type: 'setDeafenImpliesMute', value: true }, { type: 'toggleDeafen' }, { type: 'toggleMic' }]);
    expect(s.deafened).toBe(false);
    expect(s.micMuted).toBe(false);
    expect(micEnabled(s)).toBe(true);
  });

  it('turning the option on while already deafened applies the mute', () => {
    const s = run([{ type: 'toggleDeafen' }, { type: 'setDeafenImpliesMute', value: true }]);
    expect(s.micMuted).toBe(true);
    expect(reduce(s, { type: 'toggleDeafen' }).micMuted).toBe(false);
  });

  it('without the option, deafen never touches micMuted', () => {
    const s = run([{ type: 'toggleDeafen' }]);
    expect(s.micMuted).toBe(false);
    expect(micEnabled(s)).toBe(true);
  });
});

describe('push to talk', () => {
  it('disables the mic unless the key is held', () => {
    const s = run([{ type: 'setPtt', enabled: true }]);
    expect(micEnabled(s)).toBe(false);
    const held = reduce(s, { type: 'pttDown' });
    expect(micEnabled(held)).toBe(true);
    expect(micEnabled(reduce(held, { type: 'pttUp' }))).toBe(false);
  });
  it('respects mute while held', () => {
    const s = run([{ type: 'setPtt', enabled: true }, { type: 'toggleMic' }, { type: 'pttDown' }]);
    expect(micEnabled(s)).toBe(false);
  });
  it('ignores pttDown when ptt is off and clears pttActive when disabled', () => {
    expect(reduce(init, { type: 'pttDown' })).toBe(init);
    const s = run([{ type: 'setPtt', enabled: true }, { type: 'pttDown' }, { type: 'setPtt', enabled: false }]);
    expect(s.pttActive).toBe(false);
    expect(micEnabled(s)).toBe(true);
  });
});

describe('movie', () => {
  it('mute and volume are independent of voice', () => {
    const s = run([{ type: 'toggleMovieMuted' }]);
    expect(movieGain(s)).toBe(0);
    expect(voiceGain(s, 'a')).toBe(1);
    expect(micEnabled(s)).toBe(true);
  });
  it('clamps volume 0..1.5 and unmutes when raised from zero', () => {
    expect(reduce(init, { type: 'setMovieVolume', volume: 4 }).movieVolume).toBe(1.5);
    expect(reduce(init, { type: 'setMovieVolume', volume: -1 }).movieVolume).toBe(0);
    const s = run([{ type: 'setMovieMuted', muted: true }, { type: 'setMovieVolume', volume: 0.5 }]);
    expect(s.movieMuted).toBe(false);
    expect(movieGain(s)).toBe(0.5);
  });
  it('ducks by the configured dB while someone talks', () => {
    const s = run([{ type: 'setDuckDb', db: -6 }, { type: 'setDucking', ducking: true }]);
    expect(movieGain(s)).toBeCloseTo(dbToGain(-6), 6);
    expect(movieGain(s)).toBeCloseTo(0.501, 2);
    const off = reduce(s, { type: 'setDuckDb', db: 0 });
    expect(movieGain(off)).toBe(1);
    const twelve = run([{ type: 'setDuckDb', db: -12 }, { type: 'setMovieVolume', volume: 1.5 }], s);
    expect(movieGain(twelve)).toBeCloseTo(1.5 * dbToGain(-12), 6);
  });
  it('muted wins over ducking', () => {
    const s = run([{ type: 'setDuckDb', db: -12 }, { type: 'setDucking', ducking: true }, { type: 'toggleMovieMuted' }]);
    expect(movieGain(s)).toBe(0);
  });
});

describe('per-person volume', () => {
  it('boosts above 1 and clamps to 2', () => {
    const s = run([{ type: 'setVoiceVolume', identity: 'bob', volume: 3 }]);
    expect(voiceGain(s, 'bob')).toBe(2);
    expect(voiceGain(s, 'alice')).toBe(1);
  });
  it('mute-for-me is volume 0 and survives deafen cycles', () => {
    const s = run([
      { type: 'setVoiceVolume', identity: 'bob', volume: 0 },
      { type: 'toggleDeafen' },
      { type: 'toggleDeafen' },
    ]);
    expect(voiceGain(s, 'bob')).toBe(0);
  });
});

describe('presence', () => {
  it('reports what others should see', () => {
    expect(presenceOf(init)).toEqual({ micMuted: false, deafened: false, ptt: false });
    expect(presenceOf(run([{ type: 'toggleMic' }]))).toMatchObject({ micMuted: true });
    expect(presenceOf(run([{ type: 'setPtt', enabled: true }]))).toEqual({ micMuted: false, deafened: false, ptt: true });
    expect(
      presenceOf(run([{ type: 'setDeafenImpliesMute', value: true }, { type: 'toggleDeafen' }])),
    ).toEqual({ micMuted: true, deafened: true, ptt: false });
  });
});

describe('dbToGain', () => {
  it('converts decibels', () => {
    expect(dbToGain(0)).toBe(1);
    expect(dbToGain(-6)).toBeCloseTo(0.501, 3);
    expect(dbToGain(-20)).toBeCloseTo(0.1, 6);
  });
});
