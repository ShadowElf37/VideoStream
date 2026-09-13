import type {
  ChatHistoryResponse,
  ChatMessage,
  CreateRoomRequest,
  CreateRoomResponse,
  MediaMeta,
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

export const api = {
  createRoom: (body: CreateRoomRequest) =>
    request<CreateRoomResponse>('/api/rooms', { method: 'POST', body: JSON.stringify(body) }),

  getRoom: (id: string) => request<RoomInfo>(`/api/rooms/${encodeURIComponent(id)}`),

  getToken: (id: string, body: TokenRequest) =>
    request<TokenResponse>(`/api/rooms/${encodeURIComponent(id)}/token`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  getChat: (id: string, session: string, opts: { before?: number; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (opts.before) q.set('before', String(opts.before));
    q.set('limit', String(opts.limit ?? 100));
    return request<ChatHistoryResponse>(`/api/rooms/${encodeURIComponent(id)}/chat?${q}`, { session });
  },

  postChat: (id: string, session: string, text: string) =>
    request<ChatMessage>(`/api/rooms/${encodeURIComponent(id)}/chat`, {
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

  getPlayback: (id: string, session: string) =>
    request<PlaybackState>(`/api/rooms/${encodeURIComponent(id)}/playback`, { session }),

  playback: (
    id: string,
    session: string,
    body: { action: string; mediaId?: string; posMs?: number; relative?: boolean },
  ) =>
    request<PlaybackState>(`/api/rooms/${encodeURIComponent(id)}/playback`, {
      method: 'POST',
      body: JSON.stringify(body),
      session,
    }),

  serverTime: () => request<{ nowMs: number }>('/api/time'),

  patchSettings: (id: string, session: string, patch: Partial<RoomSettings>) =>
    request<RoomSettings>(`/api/rooms/${encodeURIComponent(id)}/settings`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
      session,
    }),
};
