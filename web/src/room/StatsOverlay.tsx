import type { RemoteAudioTrack, RemoteVideoTrack } from 'livekit-client';
import { useEffect, useState } from 'react';
import { useMpvStore } from '@/host/useMpv';
import { formatBitrate } from '@/lib/format';
import type { MovieTracks } from './useMovieTracks';

interface Sample {
  bytes: number;
  packets: number;
  lost: number;
  frames: number;
  t: number;
}

interface VideoStats {
  bitrateKbps: number;
  fps: number;
  width: number;
  height: number;
  jitterMs: number;
  lossPct: number;
  bufferMs: number;
  freezes: number;
  codec: string;
}

interface AudioStats {
  bitrateKbps: number;
  jitterMs: number;
  lossPct: number;
  bufferMs: number;
  concealed: number;
}

async function readStats(track: RemoteVideoTrack | RemoteAudioTrack | null, prev: Sample | null) {
  const receiver = track?.receiver;
  if (!receiver) return null;
  const report = await receiver.getStats();
  let inbound: Record<string, number | string> | null = null;
  let codec = '';
  const codecs = new Map<string, string>();
  report.forEach((s) => {
    const rec = s as unknown as Record<string, number | string>;
    if (s.type === 'inbound-rtp') inbound = rec;
    if (s.type === 'codec') codecs.set(String(rec.id), String(rec.mimeType ?? ''));
  });
  if (!inbound) return null;
  const i = inbound as Record<string, number | string>;
  codec = codecs.get(String(i.codecId)) ?? '';
  const now = performance.now();
  const sample: Sample = {
    bytes: Number(i.bytesReceived ?? 0),
    packets: Number(i.packetsReceived ?? 0),
    lost: Number(i.packetsLost ?? 0),
    frames: Number(i.framesDecoded ?? 0),
    t: now,
  };
  const dt = prev ? (now - prev.t) / 1000 : 0;
  const bitrateKbps = prev && dt > 0 ? ((sample.bytes - prev.bytes) * 8) / dt / 1000 : 0;
  const dPackets = prev ? sample.packets - prev.packets : 0;
  const dLost = prev ? sample.lost - prev.lost : 0;
  const lossPct = dPackets + dLost > 0 ? (dLost / (dPackets + dLost)) * 100 : 0;
  const jbDelay = Number(i.jitterBufferDelay ?? 0);
  const jbCount = Number(i.jitterBufferEmittedCount ?? 0);
  const bufferMs = jbCount > 0 ? (jbDelay / jbCount) * 1000 : 0;
  return {
    sample,
    bitrateKbps,
    lossPct,
    bufferMs,
    jitterMs: Number(i.jitter ?? 0) * 1000,
    fps: Number(i.framesPerSecond ?? (prev && dt > 0 ? (sample.frames - prev.frames) / dt : 0)),
    width: Number(i.frameWidth ?? 0),
    height: Number(i.frameHeight ?? 0),
    freezes: Number(i.freezeCount ?? 0),
    concealed: Number(i.concealedSamples ?? 0),
    codec: codec.replace(/^(video|audio)\//, ''),
  };
}

/** Developer-ish overlay: WebRTC receive stats for the movie plus the projector's encoder state. */
export function StatsOverlay({ movie }: { movie: MovieTracks }) {
  const mpv = useMpvStore((s) => s.state);
  const [v, setV] = useState<VideoStats | null>(null);
  const [a, setA] = useState<AudioStats | null>(null);

  useEffect(() => {
    let prevV: Sample | null = null;
    let prevA: Sample | null = null;
    let stop = false;
    const tick = async () => {
      const rv = await readStats(movie.video, prevV).catch(() => null);
      if (rv) {
        prevV = rv.sample;
        if (!stop) setV(rv);
      } else if (!stop) setV(null);
      const ra = await readStats(movie.audio, prevA).catch(() => null);
      if (ra) {
        prevA = ra.sample;
        if (!stop) setA(ra);
      } else if (!stop) setA(null);
    };
    void tick();
    const h = setInterval(() => void tick(), 1000);
    return () => {
      stop = true;
      clearInterval(h);
    };
  }, [movie.video, movie.audio]);

  return (
    <div className="pointer-events-none absolute top-3 left-3 z-20 glass-strong rounded-xl px-3 py-2 font-mono text-[11px] leading-[1.5] text-white/90 min-w-[210px]">
      <Row k="video" v={v ? `${v.width}×${v.height} ${v.fps.toFixed(0)}fps ${v.codec}` : '—'} />
      <Row k="bitrate" v={v ? formatBitrate(v.bitrateKbps) : '—'} />
      <Row k="jitter/loss" v={v ? `${v.jitterMs.toFixed(0)} ms · ${v.lossPct.toFixed(1)} %` : '—'} />
      <Row k="buffer" v={v ? `${v.bufferMs.toFixed(0)} ms · ${v.freezes} freezes` : '—'} />
      <Row k="audio" v={a ? `${formatBitrate(a.bitrateKbps)} · ${a.jitterMs.toFixed(0)} ms · ${a.lossPct.toFixed(1)} %` : '—'} />
      <div className="h-px bg-white/15 my-1" />
      <Row k="encoder" v={mpv ? `${mpv.encoder || '?'} · ${mpv.preset}` : '—'} />
      <Row k="source" v={mpv ? `${mpv.width}×${mpv.height} ${mpv.fps.toFixed(2)}fps ${formatBitrate(mpv.bitrateKbps)}` : '—'} />
      <Row k="late/pli/nack" v={mpv ? `${mpv.lateMs} ms · ${mpv.pli} · ${mpv.nack}` : '—'} />
    </div>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between gap-3">
      <span className="text-white/50">{k}</span>
      <span className="tabular-nums">{v}</span>
    </div>
  );
}
