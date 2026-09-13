// Shared message contract. Keep in sync with messages.go.

export const Topics = {
  chat: 'chat',
  typing: 'typing',
  react: 'react',
  presence: 'presence',
  settings: 'settings',
  mpvCmd: 'mpv.cmd',
  mpvReply: 'mpv.reply',
  mpvState: 'mpv.state',
  playback: 'playback',
  mpvEvent: 'mpv.event',
} as const;
export type Topic = (typeof Topics)[keyof typeof Topics];

export type Role = 'host' | 'viewer' | 'projector';

export interface ParticipantMetadata {
  role: Role;
  color: string;
}

export interface RoomSettings {
  anyoneCanPause: boolean;
  deafenImpliesMute: boolean;
  maxPreset: QualityPreset;
}

export type QualityPreset = '1080p-high' | '1080p' | '720p' | '540p';

export interface ChatAuthor {
  identity: string;
  name: string;
  color: string;
}

export interface ChatMessage {
  id: string;
  roomId: string;
  from: ChatAuthor;
  text: string;
  ts: number; // unix ms
  kind: 'user' | 'system';
}

export interface TypingMessage {
  typing: boolean;
}

export interface ReactMessage {
  emoji: string;
}

export interface PresenceMessage {
  micMuted: boolean;
  deafened: boolean;
  ptt: boolean;
}

export interface MpvCommand {
  id: number;
  cmd: unknown[];
}

export interface MpvReply {
  id: number;
  ok: boolean;
  data?: unknown;
  error?: string;
}

export type MpvTrackType = 'video' | 'audio' | 'sub';

export interface MpvTrack {
  id: number;
  type: MpvTrackType;
  lang?: string;
  title?: string;
  codec?: string;
  selected: boolean;
  default?: boolean;
  forced?: boolean;
  external?: boolean;
}

export interface MpvChapter {
  title: string;
  time: number; // seconds
}

export interface MpvState {
  seq: number;
  idle: boolean;
  pause: boolean;
  timePos: number; // seconds
  duration: number; // seconds
  speed: number;
  chapter: number;
  chapters: MpvChapter[];
  tracks: MpvTrack[];
  mediaTitle: string;
  path: string;
  subDelay: number;
  audioDelay: number;
  volume: number; // mpv volume 0..130
  subVisibility: boolean;
  encoder: string;
  preset: QualityPreset;
  bitrateKbps: number;
  fps: number;
  width: number;
  height: number;
  lateMs: number;
  pli: number;
  nack: number;
}

export type MpvEventType =
  | 'file-loaded'
  | 'seek'
  | 'pause'
  | 'unpause'
  | 'end-file'
  | 'error'
  | 'track-changed'
  | 'quality-changed';

export interface MpvEvent {
  type: MpvEventType;
  text: string; // human readable, shown as a system line in chat
  ts: number; // unix ms
  data?: Record<string, unknown>;
}

export interface FsEntry {
  name: string;
  path: string;
  dir: boolean;
  size?: number;
  mtime?: number; // unix ms
}

export interface FsList {
  dir: string;
  roots: string[];
  entries: FsEntry[];
}

// HTTP API shapes

export interface CreateRoomRequest {
  name?: string;
  password?: string;
}

export interface CreateRoomResponse {
  id: string;
  name: string;
  inviteLink: string;
  hostLink: string;
  projectorLink: string;
}

export interface RoomInfo {
  id: string;
  name: string;
  hasPassword: boolean;
  settings: RoomSettings;
}

export interface TokenRequest {
  name: string;
  inviteKey?: string;
  hostSecret?: string;
  projectorKey?: string;
  password?: string;
}

export interface TokenResponse {
  token: string;
  url: string;
  identity: string;
  role: Role;
  color: string;
  session: string;
  settings: RoomSettings;
}

export interface ChatHistoryResponse {
  messages: ChatMessage[];
}

/**
 * Where a room is in its film, for the file-on-server mode.
 *
 * An anchor, not a position: "media time anchorPosMs was true at server time
 * anchorAtMs, advancing at rate". A client reconstructs
 *
 *   target = anchorPosMs + (clientNow + offset - anchorAtMs) * rate
 *
 * so every broadcast is self-sufficient and a lost one costs nothing.
 */
export interface PlaybackState {
  seq: number;
  idle: boolean;
  mediaId: string;
  title: string;
  /** Signed and time-limited; clients never construct one. */
  url: string;
  durationMs: number;
  paused: boolean;
  anchorPosMs: number;
  anchorAtMs: number;
  rate: number;
  /** Changes on every discontinuity: licence for a client to jump. */
  gen: number;
  /** The director's clock when this was built. */
  serverNowMs: number;
  /** The anchor evaluated at serverNowMs, for convenience. */
  posMs: number;
  queue: string[];
}

/** One title in the pushed library. */
export interface MediaMeta {
  id: string;
  title: string;
  source?: string;
  durationMs: number;
  width: number;
  height: number;
  fpsNum: number;
  fpsDen: number;
  videoCodec: string;
  audioCodec: string;
  sizeBytes: number;
  audioTrack?: number;
  subTrack?: number;
  chapters?: Array<{ startMs: number; title?: string }>;
  pushedAt: string;
}
