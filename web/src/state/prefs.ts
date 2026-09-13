import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type Theme = 'dark' | 'light';
export type QualityPref = 'auto' | 'high' | 'low';
export type DuckDb = 0 | -6 | -12;
export type SidebarTab = 'chat' | 'people' | 'queue';

export interface Prefs {
  name: string;
  micDeviceId: string;
  speakerDeviceId: string;
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
  joinMuted: boolean;
  deafenImpliesMute: boolean;
  ptt: boolean;
  duckDb: DuckDb;
  smoothnessSec: number;
  qualityPref: QualityPref;
  statsOverlay: boolean;
  theme: Theme;
  notificationSounds: boolean;
  movieVolume: number;
  movieMuted: boolean;
  voiceVolumes: Record<string, number>;
  sidebarOpen: boolean;
  sidebarWidth: number;
  sidebarTab: SidebarTab;
}

interface PrefsStore extends Prefs {
  set: <K extends keyof Prefs>(key: K, value: Prefs[K]) => void;
  patch: (p: Partial<Prefs>) => void;
  setVoiceVolume: (identity: string, volume: number) => void;
}

export const DEFAULT_PREFS: Prefs = {
  name: '',
  micDeviceId: '',
  speakerDeviceId: '',
  noiseSuppression: true,
  echoCancellation: true,
  autoGainControl: true,
  joinMuted: false,
  deafenImpliesMute: false,
  ptt: false,
  duckDb: 0,
  // The receive buffer, and therefore how late every reaction looks: the
  // picture on screen is this far behind the projector, so at 1.5s a pause
  // took a second and a half to appear to have done anything (measured:
  // 1555 ms, 37 frames after the click). 0.5s still absorbs far more jitter
  // than WebRTC's ~50 ms default and makes the controls feel connected to
  // the film. Raise it in Settings if a viewer stutters.
  smoothnessSec: 0.5,
  qualityPref: 'auto',
  statsOverlay: false,
  theme: 'dark',
  notificationSounds: true,
  movieVolume: 1,
  movieMuted: false,
  voiceVolumes: {},
  sidebarOpen: true,
  sidebarWidth: 340,
  sidebarTab: 'chat',
};

export const usePrefs = create<PrefsStore>()(
  persist(
    (set) => ({
      ...DEFAULT_PREFS,
      set: (key, value) => set({ [key]: value } as Partial<Prefs>),
      patch: (p) => set(p),
      setVoiceVolume: (identity, volume) =>
        set((s) => ({ voiceVolumes: { ...s.voiceVolumes, [identity]: volume } })),
    }),
    {
      name: 'vs.prefs',
      version: 1,
      partialize: (s) => {
        const { set: _s, patch: _p, setVoiceVolume: _v, ...rest } = s;
        return rest;
      },
    },
  ),
);

/** Keep <html data-theme> in sync with the preference. */
export function applyTheme(theme: Theme) {
  if (typeof document === 'undefined') return;
  document.documentElement.dataset.theme = theme;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'light' ? '#f4f4f6' : '#0b0b0d');
}
