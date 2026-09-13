import { create } from 'zustand';
import type { PresenceMessage, Role, RoomInfo, RoomSettings, TokenResponse } from '@/proto/messages';

export type ConnPhase = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected' | 'failed';

export interface Credentials {
  inviteKey?: string;
  hostSecret?: string;
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

interface SessionStore {
  roomId: string;
  room: RoomInfo | null;
  token: TokenResponse | null;
  role: Role | null;
  settings: RoomSettings | null;
  credentials: Credentials;
  phase: ConnPhase;
  error: string | null;
  presence: Record<string, PresenceMessage>;
  toasts: Toast[];
  reactions: Reaction[];
  isFullscreen: boolean;
  audioBlocked: boolean;

  setRoom: (roomId: string, room: RoomInfo | null) => void;
  setToken: (token: TokenResponse | null) => void;
  setSettings: (s: RoomSettings) => void;
  setCredentials: (c: Credentials) => void;
  setPhase: (p: ConnPhase, error?: string | null) => void;
  setPresence: (identity: string, p: PresenceMessage) => void;
  removePresence: (identity: string) => void;
  toast: (text: string, kind?: Toast['kind'], ttl?: number) => void;
  dismissToast: (id: number) => void;
  addReaction: (emoji: string, from: string) => void;
  pruneReactions: (before: number) => void;
  setFullscreen: (v: boolean) => void;
  setAudioBlocked: (v: boolean) => void;
  reset: () => void;
}

let seq = 1;

export const useSession = create<SessionStore>()((set) => ({
  roomId: '',
  room: null,
  token: null,
  role: null,
  settings: null,
  credentials: {},
  phase: 'idle',
  error: null,
  presence: {},
  toasts: [],
  reactions: [],
  isFullscreen: false,
  audioBlocked: false,

  setRoom: (roomId, room) => set({ roomId, room, settings: room?.settings ?? null }),
  setToken: (token) => set({ token, role: token?.role ?? null, settings: token?.settings ?? null }),
  setSettings: (settings) => set({ settings }),
  setCredentials: (credentials) => set({ credentials }),
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
  pruneReactions: (before) => set((s) => ({ reactions: s.reactions.filter((r) => r.id >= before) })),
  setFullscreen: (isFullscreen) => set({ isFullscreen }),
  setAudioBlocked: (audioBlocked) => set({ audioBlocked }),
  reset: () =>
    set({
      token: null,
      role: null,
      phase: 'idle',
      error: null,
      presence: {},
      toasts: [],
      reactions: [],
      audioBlocked: false,
    }),
}));
