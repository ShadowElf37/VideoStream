import { Room, RoomEvent, type RemoteParticipant } from 'livekit-client';
import { createContext, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { create } from 'zustand';
import { decode, publish } from '@/lib/data';
import { Topics, type FsList, type MpvCommand, type MpvEvent, type MpvReply, type MpvState } from '@/proto/messages';
import { isProjector, PROJECTOR_IDENTITY } from '@/room/identity';

/** Latest projector state as broadcast on `mpv.state`, plus event fan-out. */
interface MpvStore {
  state: MpvState | null;
  /** performance.now() when `state` arrived, to interpolate the position between updates. */
  receivedAt: number;
  lastEvent: MpvEvent | null;
  eventSeq: number;
  projectorOnline: boolean;
  setState: (s: MpvState) => void;
  setEvent: (e: MpvEvent) => void;
  setOnline: (v: boolean) => void;
  reset: () => void;
}

export const useMpvStore = create<MpvStore>()((set) => ({
  state: null,
  receivedAt: 0,
  lastEvent: null,
  eventSeq: 0,
  projectorOnline: false,
  setState: (s) =>
    set((cur) => (cur.state && cur.state.seq > s.seq ? cur : { state: s, receivedAt: performance.now() })),
  setEvent: (e) => set((cur) => ({ lastEvent: e, eventSeq: cur.eventSeq + 1 })),
  setOnline: (projectorOnline) => set({ projectorOnline }),
  reset: () => set({ state: null, receivedAt: 0, lastEvent: null, eventSeq: 0, projectorOnline: false }),
}));

export class MpvError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MpvError';
  }
}

export interface MpvClient {
  send: (cmd: unknown[], opts?: { timeoutMs?: number }) => Promise<MpvReply>;
  fsList: (dir: string) => Promise<FsList>;
}

const DEFAULT_TIMEOUT = 8000;

/**
 * Wires the data-channel topics for the projector: commands out (reliable, to
 * the projector identity, with an incrementing id), replies / state / events in.
 */
export function useMpvPlumbing(room: Room | null, connected: boolean): MpvClient {
  const nextId = useRef(1);
  const pending = useRef(new Map<number, { resolve: (r: MpvReply) => void; timer: ReturnType<typeof setTimeout> }>());
  const store = useMpvStore;

  useEffect(() => {
    if (!room) return;
    const pend = pending.current;
    const onData = (payload: Uint8Array, participant?: RemoteParticipant, _kind?: unknown, topic?: string) => {
      switch (topic) {
        case Topics.mpvState: {
          const s = decode<MpvState>(payload);
          if (s) store.getState().setState(s);
          break;
        }
        case Topics.mpvEvent: {
          const e = decode<MpvEvent>(payload);
          if (e) store.getState().setEvent(e);
          break;
        }
        case Topics.mpvReply: {
          const r = decode<MpvReply>(payload);
          if (!r) break;
          const p = pend.get(r.id);
          if (p) {
            clearTimeout(p.timer);
            pend.delete(r.id);
            p.resolve(r);
          }
          break;
        }
        default:
          break;
      }
      void participant;
    };
    const refreshOnline = () => {
      const online = [...room.remoteParticipants.values()].some(isProjector);
      store.getState().setOnline(online);
    };
    room.on(RoomEvent.DataReceived, onData);
    room.on(RoomEvent.ParticipantConnected, refreshOnline);
    room.on(RoomEvent.ParticipantDisconnected, refreshOnline);
    room.on(RoomEvent.Connected, refreshOnline);
    room.on(RoomEvent.Reconnected, refreshOnline);
    refreshOnline();
    return () => {
      room.off(RoomEvent.DataReceived, onData);
      room.off(RoomEvent.ParticipantConnected, refreshOnline);
      room.off(RoomEvent.ParticipantDisconnected, refreshOnline);
      room.off(RoomEvent.Connected, refreshOnline);
      room.off(RoomEvent.Reconnected, refreshOnline);
      for (const p of pend.values()) {
        clearTimeout(p.timer);
        p.resolve({ id: 0, ok: false, error: 'disconnected' });
      }
      pend.clear();
    };
  }, [room, store]);

  const client = useMemo<MpvClient>(() => {
    const sendOnce = (cmd: unknown[], opts: { timeoutMs?: number } = {}): Promise<MpvReply> => {
      if (!room) return Promise.resolve({ id: 0, ok: false, error: 'not connected' });
      const id = nextId.current++;
      const msg: MpvCommand = { id, cmd };
      return new Promise<MpvReply>((resolve) => {
        const timer = setTimeout(() => {
          pending.current.delete(id);
          resolve({ id, ok: false, error: 'timeout: projector did not reply' });
        }, opts.timeoutMs ?? DEFAULT_TIMEOUT);
        pending.current.set(id, { resolve, timer });
        void publish(room, Topics.mpvCmd, msg, { reliable: true, to: [PROJECTOR_IDENTITY] });
      });
    };

    // The projector cannot always tell who sent a command. LiveKit withholds
    // the participant roster from it (its token cannot subscribe, and that is
    // deliberate), so it identifies senders only from what LiveKit attaches to
    // each packet — which is missing for the first packet or two after a join.
    // The result was that the first thing a host did on entering a room came
    // back "host role required", most visibly an empty Queue tab.
    //
    // Retrying once fixes it because by then the projector has seen us. This
    // lives here rather than in the projector because the projector has no way
    // to resolve the sender on its own; only a later packet helps.
    const send = async (cmd: unknown[], opts: { timeoutMs?: number } = {}): Promise<MpvReply> => {
      const first = await sendOnce(cmd, opts);
      if (first.ok || first.error !== 'host role required') return first;
      await new Promise((r) => setTimeout(r, 400));
      return sendOnce(cmd, opts);
    };
    const fsList = async (dir: string): Promise<FsList> => {
      const r = await send(['vs/fs.list', dir], { timeoutMs: 15000 });
      if (!r.ok) throw new MpvError(r.error ?? 'listing failed');
      return r.data as FsList;
    };
    return { send, fsList };
  }, [room]);

  // Ask for a full state once we are in, so the host bar is populated before
  // the next 4 Hz tick (only the host is authorised to send commands).
  useEffect(() => {
    if (!room || !connected) return;
    void client.send(['vs/state']).then((r) => {
      if (r.ok && r.data && typeof r.data === 'object') store.getState().setState(r.data as MpvState);
    });
  }, [room, connected, client, store]);

  return client;
}

export const MpvContext = createContext<MpvClient | null>(null);

export function useMpv(): MpvClient {
  const c = useContext(MpvContext);
  if (!c) throw new Error('useMpv outside MpvContext');
  return c;
}

/**
 * Playback position interpolated between `mpv.state` updates, so the bar
 * moves smoothly rather than stepping 4× per second.
 */
export function useSmoothTimePos(intervalMs = 100): number {
  const state = useMpvStore((s) => s.state);
  const receivedAt = useMpvStore((s) => s.receivedAt);
  const [pos, setPos] = useState(state?.timePos ?? 0);
  useEffect(() => {
    if (!state) {
      setPos(0);
      return;
    }
    const compute = () => {
      if (state.pause || state.idle) return state.timePos;
      const elapsed = (performance.now() - receivedAt) / 1000;
      return Math.min(state.duration || Infinity, state.timePos + elapsed * (state.speed || 1));
    };
    setPos(compute());
    if (state.pause || state.idle) return;
    const t = setInterval(() => setPos(compute()), intervalMs);
    return () => clearInterval(t);
  }, [state, receivedAt, intervalMs]);
  return pos;
}
