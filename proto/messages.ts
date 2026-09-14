// Shared message contract. Keep in sync with messages.go.

/** The one room. The site is a single door into a single LiveKit room. */
export const ROOM_ID = 'main';

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
  /**
   * Hold playback at every discontinuity until each client that is still
   * reporting says it has buffered enough to start. Off by default: it trades
   * a few seconds at the top of a scene for nobody scrambling to catch up.
   */
  waitForEveryone: boolean;
}

export type QualityPreset = '1080p-high' | '1080p' | '720p' | '540p';

export interface ChatAuthor {
  identity: string;
  name: string;
  color: string;
}

export interface ChatMessage {
  id: string;
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

/**
 * What a visitor already holds: the role their link or their remembered-device
 * cookie grants, or 'none' — then only the room password gets them in, as host.
 */
export type Access = 'host' | 'viewer' | 'none';

/** GET /api/room: enough to render the door, no more. */
export interface RoomInfo {
  /** The best of the key in the link and the cookie on this device. */
  access: Access;
  /** People in the room (projector excluded); 0 when access is 'none'. */
  occupants: number;
}

/** The link to send to friends. No host link: hosts get in with the password
 *  and stay in with the cookie it sets. Rotates once the room has stood empty
 *  for a while, and on demand by a host. */
export interface Links {
  viewer: string;
}

/** One of: a viewer key from a link, the room password (grants host), or the
 *  cookie from an earlier join. The best of what is presented wins. */
export interface TokenRequest {
  name: string;
  key?: string;
  password?: string;
  /** Join as the projector; needs host access (the password). */
  projector?: boolean;
}

export interface TokenResponse {
  token: string;
  url: string;
  identity: string;
  role: Role;
  color: string;
  session: string;
  settings: RoomSettings;
  /** The current viewer link, for the address bar and the Invite dialog. */
  links: Links;
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
  /**
   * True while waitForEveryone is parking the room at a discontinuity until
   * the slow clients catch up. The room reads as paused as well — holding is
   * the reason, not a second kind of pause.
   */
  holding: boolean;
  /** Who is still buffering, for the card that says so. */
  waitingFor?: string[];
}

/**
 * A client telling the director whether it could start now.
 *
 * `gen` matters as much as `ready`: "I am buffered" is only an answer to the
 * question the director is currently asking, and a report for a position the
 * room has already left says nothing about the one it is waiting at.
 */
export interface PlaybackReady {
  gen: number;
  bufferedAheadMs: number;
  ready: boolean;
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
