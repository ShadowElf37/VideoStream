import type { Room } from 'livekit-client';
import type { Topic } from '@/proto/messages';

const enc = new TextEncoder();
const dec = new TextDecoder();

export function encode(msg: unknown): Uint8Array {
  return enc.encode(JSON.stringify(msg));
}

export function decode<T>(payload: Uint8Array): T | null {
  try {
    return JSON.parse(dec.decode(payload)) as T;
  } catch {
    return null;
  }
}

export interface PublishOpts {
  reliable?: boolean;
  to?: string[];
}

/** Publish a JSON message on a topic. Swallows errors when not connected. */
export async function publish(room: Room, topic: Topic, msg: unknown, opts: PublishOpts = {}): Promise<void> {
  try {
    await room.localParticipant.publishData(encode(msg), {
      topic,
      reliable: opts.reliable ?? true,
      destinationIdentities: opts.to,
    });
  } catch (e) {
    console.warn(`publishData(${topic}) failed`, e);
  }
}
