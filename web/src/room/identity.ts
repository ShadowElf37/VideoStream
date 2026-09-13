import type { Participant } from 'livekit-client';
import type { ParticipantMetadata, Role } from '@/proto/messages';

export const PROJECTOR_IDENTITY = 'projector';
export const MOVIE_VIDEO_NAME = 'movie.video';
export const MOVIE_AUDIO_NAME = 'movie.audio';

export function parseMetadata(raw: string | undefined): Partial<ParticipantMetadata> {
  if (!raw) return {};
  try {
    return JSON.parse(raw) as ParticipantMetadata;
  } catch {
    return {};
  }
}

export function roleOf(p: Participant): Role | undefined {
  return parseMetadata(p.metadata).role;
}

export function isProjector(p: Participant): boolean {
  return p.identity === PROJECTOR_IDENTITY || roleOf(p) === 'projector';
}

export function isHost(p: Participant): boolean {
  return roleOf(p) === 'host';
}

export function displayName(p: Participant): string {
  return p.name?.trim() || p.identity;
}
