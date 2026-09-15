import { useRoomContext } from '@livekit/components-react';
import { useCallback, useState } from 'react';
import { usePlaybackStore } from '@/movie/store';
import { useTransport } from '@/movie/useTransport';
import { publish } from '@/lib/data';
import { Topics } from '@/proto/messages';
import { useSession } from '@/state/session';

const COOLDOWN_MS = 4000;
/** Shared across instances, so the button and Space cannot double up. */
let lastRequestAt = 0;

/**
 * A viewer's pause: the real thing when the room allows anyone to pause,
 * otherwise a raised hand the host sees. Rate-limited, because a request is
 * a reaction plus a banner, and Space held down would be a storm of them.
 */
export function useRequestPause(): { request: () => Promise<void>; cooldown: boolean } {
  const room = useRoomContext();
  const hosted = usePlaybackStore((s) => !!s.state && !s.state.idle && !!s.state.url);
  const transport = useTransport(hosted);
  const [cooldown, setCooldown] = useState(false);

  const request = useCallback(async () => {
    const now = Date.now();
    if (now - lastRequestAt < COOLDOWN_MS) return;
    lastRequestAt = now;
    setCooldown(true);
    setTimeout(() => setCooldown(false), COOLDOWN_MS);
    if (useSession.getState().settings?.anyoneCanPause) {
      // We can pause directly, so the paused stage is the indicator — asking
      // the room to pause as well would just be noise.
      await transport.togglePause();
      return;
    }
    await publish(room, Topics.react, { emoji: '⏸️' }, { reliable: false });
    useSession.getState().requestPause('You');
  }, [room, transport]);

  return { request, cooldown };
}
