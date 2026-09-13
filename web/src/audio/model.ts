/**
 * The separated audio controls as a pure reducer. Every gain the app applies
 * is derived from this state, so mic / deafen / movie stay independent by
 * construction and can be re-applied idempotently after reconnects.
 */

export interface AudioState {
  micMuted: boolean;
  deafened: boolean;
  deafenImpliesMute: boolean;
  micMutedBeforeDeafen: boolean;
  ptt: boolean;
  pttActive: boolean;
  movieMuted: boolean;
  /** 0..1.5 (values above 1 boost via the Web Audio gain node). */
  movieVolume: number;
  /** 0 (off), -6 or -12 dB applied to the movie while someone talks. */
  duckDb: number;
  ducking: boolean;
  /** Per-identity voice volume 0..2, absent = 1. */
  voiceVolumes: Record<string, number>;
}

export type AudioAction =
  | { type: 'toggleMic' }
  | { type: 'setMicMuted'; muted: boolean }
  | { type: 'toggleDeafen' }
  | { type: 'setDeafened'; deafened: boolean }
  | { type: 'setDeafenImpliesMute'; value: boolean }
  | { type: 'setPtt'; enabled: boolean }
  | { type: 'pttDown' }
  | { type: 'pttUp' }
  | { type: 'toggleMovieMuted' }
  | { type: 'setMovieMuted'; muted: boolean }
  | { type: 'setMovieVolume'; volume: number }
  | { type: 'setDuckDb'; db: number }
  | { type: 'setDucking'; ducking: boolean }
  | { type: 'setVoiceVolume'; identity: string; volume: number };

export const MOVIE_VOLUME_MAX = 1.5;
export const VOICE_VOLUME_MAX = 2;

export const initialAudioState: AudioState = {
  micMuted: false,
  deafened: false,
  deafenImpliesMute: false,
  micMutedBeforeDeafen: false,
  ptt: false,
  pttActive: false,
  movieMuted: false,
  movieVolume: 1,
  duckDb: 0,
  ducking: false,
  voiceVolumes: {},
};

const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, Number.isFinite(n) ? n : min));

export function dbToGain(db: number): number {
  return Math.pow(10, db / 20);
}

function deafen(s: AudioState, deafened: boolean): AudioState {
  if (deafened === s.deafened) return s;
  if (!s.deafenImpliesMute) return { ...s, deafened };
  // Discord semantics: deafening also mutes; undeafening restores what the
  // mic was before, unless the user explicitly changed it in between.
  return deafened
    ? { ...s, deafened, micMutedBeforeDeafen: s.micMuted, micMuted: true }
    : { ...s, deafened, micMuted: s.micMutedBeforeDeafen };
}

export function reduce(s: AudioState, a: AudioAction): AudioState {
  switch (a.type) {
    case 'toggleMic':
      return reduce(s, { type: 'setMicMuted', muted: !s.micMuted });
    case 'setMicMuted': {
      if (a.muted === s.micMuted) return s;
      // Unmuting while deafened-with-implied-mute means "I want to talk again": undeafen too.
      if (!a.muted && s.deafened && s.deafenImpliesMute) {
        return { ...s, deafened: false, micMuted: false, micMutedBeforeDeafen: false };
      }
      return { ...s, micMuted: a.muted, micMutedBeforeDeafen: s.deafened ? s.micMutedBeforeDeafen : a.muted };
    }
    case 'toggleDeafen':
      return deafen(s, !s.deafened);
    case 'setDeafened':
      return deafen(s, a.deafened);
    case 'setDeafenImpliesMute': {
      if (a.value === s.deafenImpliesMute) return s;
      const next = { ...s, deafenImpliesMute: a.value };
      if (a.value && s.deafened && !s.micMuted) {
        // Turning the option on while deafened: apply the implication now.
        return { ...next, micMutedBeforeDeafen: false, micMuted: true };
      }
      return next;
    }
    case 'setPtt':
      return a.enabled === s.ptt ? s : { ...s, ptt: a.enabled, pttActive: false };
    case 'pttDown':
      return s.ptt && !s.pttActive ? { ...s, pttActive: true } : s;
    case 'pttUp':
      return s.pttActive ? { ...s, pttActive: false } : s;
    case 'toggleMovieMuted':
      return { ...s, movieMuted: !s.movieMuted };
    case 'setMovieMuted':
      return a.muted === s.movieMuted ? s : { ...s, movieMuted: a.muted };
    case 'setMovieVolume': {
      const volume = clamp(a.volume, 0, MOVIE_VOLUME_MAX);
      // Dragging the slider up from zero un-mutes; that is what people expect.
      const movieMuted = volume > 0 && s.movieMuted ? false : s.movieMuted;
      return volume === s.movieVolume && movieMuted === s.movieMuted ? s : { ...s, movieVolume: volume, movieMuted };
    }
    case 'setDuckDb':
      return { ...s, duckDb: clamp(a.db, -24, 0) };
    case 'setDucking':
      return a.ducking === s.ducking ? s : { ...s, ducking: a.ducking };
    case 'setVoiceVolume': {
      const volume = clamp(a.volume, 0, VOICE_VOLUME_MAX);
      if ((s.voiceVolumes[a.identity] ?? 1) === volume) return s;
      return { ...s, voiceVolumes: { ...s.voiceVolumes, [a.identity]: volume } };
    }
    default:
      return s;
  }
}

// Derived values ------------------------------------------------------------

export function micEnabled(s: AudioState): boolean {
  return !(s.micMuted || (s.deafened && s.deafenImpliesMute)) && (!s.ptt || s.pttActive);
}

export function voiceGain(s: AudioState, identity: string): number {
  return s.deafened ? 0 : (s.voiceVolumes[identity] ?? 1);
}

export function movieGain(s: AudioState): number {
  if (s.movieMuted) return 0;
  return s.movieVolume * (s.ducking && s.duckDb < 0 ? dbToGain(s.duckDb) : 1);
}

/** What the presence broadcast should carry. */
export function presenceOf(s: AudioState) {
  return { micMuted: !micEnabled(s) && !(s.ptt && !s.pttActive && !s.micMuted), deafened: s.deafened, ptt: s.ptt };
}
