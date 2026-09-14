import type {
  ChatHistoryResponse,
  ChatMessage,
  Links,
  MediaMeta,
  PlaybackReady,
  PlaybackState,
  RoomInfo,
  RoomSettings,
  TokenRequest,
  TokenResponse,
} from '@/proto/messages';

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
  get isAuth() {
    return this.status === 401 || this.status === 403;
  }
}

async function request<T>(path: string, init: RequestInit & { session?: string } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (init.body !== undefined) headers['Content-Type'] = 'application/json';
  if (init.session) headers.Authorization = `Bearer ${init.session}`;
  const res = await fetch(path, { ...init, headers: { ...headers, ...(init.headers as Record<string, string>) } });
  if (!res.ok) {
    let msg = res.statusText || `HTTP ${res.status}`;
    try {
      const text = await res.text();
      try {
        const j = JSON.parse(text) as { error?: string; message?: string };
        msg = j.error ?? j.message ?? text ?? msg;
      } catch {
        if (text) msg = text;
      }
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// There is one room, so nothing here takes a room id. The door is
// `getRoom` (what is this key worth?) and `getToken` (let me in); everything
// after that carries the session the token endpoint handed back.
export const api = {
  getRoom: (key?: string) => request<RoomInfo>(key ? `/api/room?key=${encodeURIComponent(key)}` : '/api/room'),

  getToken: (body: TokenRequest) =>
    request<TokenResponse>('/api/room/token', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  getLinks: (session: string) => request<Links>('/api/room/links', { session }),

  /** Forget this device: clears the remembered-access cookie. */
  logout: () => request<void>('/api/room/logout', { method: 'POST' }),

  rotateLinks: (session: string) => request<Links>('/api/room/links/rotate', { method: 'POST', session }),

  getChat: (session: string, opts: { before?: number; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.before) q.set('before', String(opts.before));
    q.set('limit', String(opts.limit ?? 100));
    return request<ChatHistoryResponse>(`/api/room/chat?${q}`, { session });
  },

  postChat: (session: string, text: string) =>
    request<ChatMessage>('/api/room/chat', {
      method: 'POST',
      body: JSON.stringify({ text }),
      session,
    }),

  // The pushed library and the transport. Playback commands are plain HTTP:
  // the session already carries the role, so there is no participant to
  // identify and no data-channel race to lose.
  listMedia: (session: string) =>
    request<{ items: Array<MediaMeta & { url: string }>; freeBytes: number }>('/api/media', { session }),

  deleteMedia: (session: string, id: string) =>
    request<void>(`/api/media/${encodeURIComponent(id)}`, { method: 'DELETE', session }),

  getPlayback: (session: string) => request<PlaybackState>('/api/room/playback', { session }),

  playback: (session: string, body: { action: string; mediaId?: string; posMs?: number; relative?: boolean }) =>
    request<PlaybackState>('/api/room/playback', {
      method: 'POST',
      body: JSON.stringify(body),
      session,
    }),

  /** Tell the director whether this client could start now; see waitForEveryone. */
  playbackReady: (session: string, body: PlaybackReady) =>
    request<void>('/api/room/playback/ready', {
      method: 'POST',
      body: JSON.stringify(body),
      session,
    }),

  serverTime: () => request<{ nowMs: number }>('/api/time'),

  patchSettings: (session: string, patch: Partial<RoomSettings>) =>
    request<RoomSettings>('/api/room/settings', {
      method: 'PATCH',
      body: JSON.stringify(patch),
      session,
    }),
};
