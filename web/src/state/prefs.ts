import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export type Theme = 'dark' | 'light';
export type DuckDb = 0 | -6 | -12;
export type SidebarTab = 'chat' | 'people' | 'library';

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
  statsOverlay: boolean;
  theme: Theme;
  notificationSounds: boolean;
  movieVolume: number;
  movieMuted: boolean;
  voiceVolumes: Record<string, number>;
  sidebarOpen: boolean;
  sidebarWidth: number;
  sidebarTab: SidebarTab;
  /** Host-local: the desktop projector's panel is shown and the stage
   *  expects the live track. Off, the app is the server library, Plex-style. */
  projectorMode: boolean;
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
  // The receive buffer. It is deliberately large: it is what rides out shaky
  // Wi-Fi. Control latency is NOT solved by shrinking it — clients act on the
  // pause command directly instead, so a deep buffer costs nothing in
  // responsiveness.
  smoothnessSec: 1.5,
  statsOverlay: false,
  theme: 'dark',
  notificationSounds: true,
  movieVolume: 1,
  movieMuted: false,
  voiceVolumes: {},
  sidebarOpen: true,
  sidebarWidth: 340,
  sidebarTab: 'chat',
  projectorMode: false,
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
      version: 2,
      migrate: (persisted, version) => {
        const s = persisted as Record<string, unknown>;
        // v1 called the library tab "queue".
        if (version < 2 && s.sidebarTab === 'queue') s.sidebarTab = 'library';
        return s as unknown as Prefs;
      },
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
