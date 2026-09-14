import { PENDING_MS, useSyncStats } from './syncStats';

/**
 * Where this machine's picture is, versus where the room says it should be.
 *
 * After a host seek there is a genuine second or two in which those are
 * different places — the browser has to fetch and decode before it can show
 * the new one. A bar that draws only the room's position claims we are there
 * when we are not; one that draws only ours hides the fact that everyone else
 * has already moved. So the transport bars draw both, but only while the gap
 * is large enough to see.
 */
export interface RoomLag {
  /** Seconds, this machine's playhead; null when it has nothing to say. */
  position: number | null;
  pending: boolean;
}

export function useRoomLag(hosted: boolean): RoomLag {
  const stats = useSyncStats((s) => s.stats);
  if (!hosted || !stats) return { position: null, pending: false };
  return {
    position: stats.positionMs / 1000,
    pending: Math.abs(stats.errorMs) > PENDING_MS,
  };
}
