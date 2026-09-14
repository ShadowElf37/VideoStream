import { create } from 'zustand';
import type { ChatMessage } from '@/proto/messages';

export interface LocalMessage extends ChatMessage {
  pending?: boolean;
  failed?: boolean;
  /** True for lines synthesised locally (mpv events), never sent to the server. */
  local?: boolean;
}

interface ChatStore {
  messages: LocalMessage[];
  typing: Record<string, number>; // identity -> expiry ts
  unread: number;
  /** identity of the local user, to suppress self-typing and unread counts */
  self: string;
  setSelf: (identity: string) => void;
  setHistory: (msgs: ChatMessage[]) => void;
  add: (msg: LocalMessage, opts?: { countUnread?: boolean }) => void;
  resolvePending: (tempId: string, real: ChatMessage) => void;
  markFailed: (tempId: string) => void;
  addSystem: (text: string, ts?: number) => void;
  setTyping: (identity: string, typing: boolean) => void;
  pruneTyping: () => void;
  clearUnread: () => void;
  reset: () => void;
}

let localSeq = 1;

export const useChat = create<ChatStore>()((set) => ({
  messages: [],
  typing: {},
  unread: 0,
  self: '',
  setSelf: (self) => set({ self }),
  setHistory: (msgs) =>
    set((s) => {
      const known = new Set(msgs.map((m) => m.id));
      const keep = s.messages.filter((m) => m.local || m.pending || !known.has(m.id));
      return { messages: [...msgs, ...keep].sort((a, b) => a.ts - b.ts) };
    }),
  add: (msg, opts) =>
    set((s) => {
      if (s.messages.some((m) => m.id === msg.id)) return s;
      const countUnread = opts?.countUnread ?? (msg.kind === 'user' && msg.from.identity !== s.self);
      return { messages: [...s.messages, msg], unread: countUnread ? s.unread + 1 : s.unread };
    }),
  resolvePending: (tempId, real) =>
    set((s) => {
      if (s.messages.some((m) => m.id === real.id)) {
        return { messages: s.messages.filter((m) => m.id !== tempId) };
      }
      return { messages: s.messages.map((m) => (m.id === tempId ? { ...real } : m)) };
    }),
  markFailed: (tempId) =>
    set((s) => ({ messages: s.messages.map((m) => (m.id === tempId ? { ...m, pending: false, failed: true } : m)) })),
  addSystem: (text, ts = Date.now()) =>
    set((s) => ({
      messages: [
        ...s.messages,
        {
          id: `local-${localSeq++}`,
          from: { identity: 'system', name: 'System', color: '#9a9aa5' },
          text,
          ts,
          kind: 'system',
          local: true,
        },
      ],
    })),
  setTyping: (identity, typing) =>
    set((s) => {
      if (identity === s.self) return s;
      const next = { ...s.typing };
      if (typing) next[identity] = Date.now() + 4000;
      else delete next[identity];
      return { typing: next };
    }),
  pruneTyping: () =>
    set((s) => {
      const now = Date.now();
      const entries = Object.entries(s.typing).filter(([, exp]) => exp > now);
      if (entries.length === Object.keys(s.typing).length) return s;
      return { typing: Object.fromEntries(entries) };
    }),
  clearUnread: () => set({ unread: 0 }),
  reset: () => set({ messages: [], typing: {}, unread: 0 }),
}));
