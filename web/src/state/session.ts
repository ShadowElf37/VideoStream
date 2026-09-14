import { create } from 'zustand';
import type { Access, Links, PresenceMessage, Role, RoomSettings, TokenResponse } from '@/proto/messages';

export type ConnPhase = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected' | 'failed';

/** What we present at the door. After a successful join only the key is
 *  kept (our own link), so a reconnect never re-sends the password. */
export interface Credentials {
  key?: string;
  password?: string;
}

export interface Toast {
  id: number;
  text: string;
  kind?: 'info' | 'warn' | 'error';
  ttl?: number;
}

export interface Reaction {
  id: number;
  emoji: string;
  from: string;
  x: number;
}

/** A viewer asking for a pause. Shown as its own loud banner, not as a
 *  floating emoji among the reactions — it is a request aimed at someone,
 *  and it was too easy to miss drifting past with the hearts. */
export interface PauseRequest {
  id: number;
  from: string;
}

interface SessionStore {
  /** What the door said our key is worth, before we joined. */
  access: Access | null;
  /** Display name the local user joined with. */
  name: string;
  token: TokenResponse | null;
  role: Role | null;
  settings: RoomSettings | null;
  /** The current links; the host one is only present for hosts. */
  links: Links | null;
  credentials: Credentials;
  /** Set when a reconnect was refused because our key was rotated away. */
  linkExpired: boolean;
  phase: ConnPhase;
  error: string | null;
  presence: Record<string, PresenceMessage>;
  toasts: Toast[];
  reactions: Reaction[];
  pauseRequest: PauseRequest | null;
  isFullscreen: boolean;
  audioBlocked: boolean;
  inviteOpen: boolean;

  setAccess: (a: Access | null) => void;
  setName: (name: string) => void;
  setToken: (token: TokenResponse | null) => void;
  setSettings: (s: RoomSettings) => void;
  setLinks: (l: Links | null) => void;
  setCredentials: (c: Credentials) => void;
  setLinkExpired: (v: boolean) => void;
  setPhase: (p: ConnPhase, error?: string | null) => void;
  setPresence: (identity: string, p: PresenceMessage) => void;
  removePresence: (identity: string) => void;
  toast: (text: string, kind?: Toast['kind'], ttl?: number) => void;
  dismissToast: (id: number) => void;
  addReaction: (emoji: string, from: string) => void;
  removeReaction: (id: number) => void;
  requestPause: (from: string) => void;
  clearPauseRequest: (id: number) => void;
  setFullscreen: (v: boolean) => void;
  setAudioBlocked: (v: boolean) => void;
  setInviteOpen: (v: boolean) => void;
  reset: () => void;
}

let seq = 1;

export const useSession = create<SessionStore>()((set) => ({
  access: null,
  name: '',
  token: null,
  role: null,
  settings: null,
  links: null,
  credentials: {},
  linkExpired: false,
  phase: 'idle',
  error: null,
  presence: {},
  toasts: [],
  reactions: [],
  pauseRequest: null,
  isFullscreen: false,
  audioBlocked: false,
  inviteOpen: false,

  setAccess: (access) => set({ access }),
  setName: (name) => set({ name }),
  setToken: (token) => set({ token, role: token?.role ?? null, settings: token?.settings ?? null, links: token?.links ?? null }),
  setSettings: (settings) => set({ settings }),
  setLinks: (links) => set({ links }),
  setCredentials: (credentials) => set({ credentials }),
  setLinkExpired: (linkExpired) => set({ linkExpired }),
  setPhase: (phase, error = null) => set({ phase, error }),
  setPresence: (identity, p) => set((s) => ({ presence: { ...s.presence, [identity]: p } })),
  removePresence: (identity) =>
    set((s) => {
      const { [identity]: _gone, ...rest } = s.presence;
      return { presence: rest };
    }),
  toast: (text, kind = 'info', ttl = 4000) =>
    set((s) => ({ toasts: [...s.toasts.slice(-3), { id: seq++, text, kind, ttl }] })),
  dismissToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
  addReaction: (emoji, from) =>
    set((s) => ({
      reactions: [...s.reactions.slice(-24), { id: seq++, emoji, from, x: 8 + Math.random() * 84 }],
    })),
  removeReaction: (id) => set((s) => ({ reactions: s.reactions.filter((r) => r.id !== id) })),
  requestPause: (from) => set({ pauseRequest: { id: seq++, from } }),
  // Guarded by id so a later request's timer cannot clear an earlier one's banner.
  clearPauseRequest: (id) => set((s) => (s.pauseRequest?.id === id ? { pauseRequest: null } : {})),
  setFullscreen: (isFullscreen) => set({ isFullscreen }),
  setAudioBlocked: (audioBlocked) => set({ audioBlocked }),
  setInviteOpen: (inviteOpen) => set({ inviteOpen }),
  reset: () =>
    set({
      token: null,
      role: null,
      links: null,
      phase: 'idle',
      error: null,
      presence: {},
      toasts: [],
      reactions: [],
      pauseRequest: null,
      audioBlocked: false,
      inviteOpen: false,
    }),
}));
