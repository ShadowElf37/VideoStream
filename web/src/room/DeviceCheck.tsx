import { Headphones, Info, Mic, MicOff, Volume2 } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { getAudioContext, unlockAudio } from '@/audio/context';
import { deviceLabel, listDevices, onDeviceChange, openMicPreview, stopStream, type DeviceLists } from '@/audio/devices';
import { createMeter } from '@/audio/meter';
import { playTestTone } from '@/audio/tone';
import { cn } from '@/lib/cn';
import { isFirefox, isSafari, supportsSinkId } from '@/lib/platform';
import { usePrefs } from '@/state/prefs';
import { Button } from '@/ui/Button';
import { Field, Select } from '@/ui/Field';
import { Switch } from '@/ui/Switch';

/** Step 2: mic / speaker check. The Join click also unlocks audio. */
export function DeviceCheck({ onJoin, onBack, busy }: { onJoin: () => void; onBack: () => void; busy: boolean }) {
  const prefs = usePrefs();
  const [devices, setDevices] = useState<DeviceLists>({ mics: [], speakers: [] });
  const [level, setLevel] = useState(0);
  const [micError, setMicError] = useState<string | null>(null);
  const [toneBusy, setToneBusy] = useState(false);
  const streamRef = useRef<MediaStream | null>(null);

  // Open a preview mic (re-opened when device / NS change) and drive the meter.
  useEffect(() => {
    let cancelled = false;
    let raf = 0;
    let meter: ReturnType<typeof createMeter> | null = null;
    setMicError(null);
    (async () => {
      try {
        const stream = await openMicPreview({
          deviceId: prefs.micDeviceId || undefined,
          echoCancellation: prefs.echoCancellation,
          noiseSuppression: prefs.noiseSuppression,
          autoGainControl: prefs.autoGainControl,
        });
        if (cancelled) {
          stopStream(stream);
          return;
        }
        streamRef.current = stream;
        setDevices(await listDevices());
        const ctx = getAudioContext();
        if (ctx.state !== 'running') await ctx.resume().catch(() => undefined);
        meter = createMeter(ctx, stream);
        const tick = () => {
          setLevel(meter?.read() ?? 0);
          raf = requestAnimationFrame(tick);
        };
        tick();
      } catch (e) {
        if (!cancelled) setMicError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
      cancelAnimationFrame(raf);
      meter?.stop();
      stopStream(streamRef.current);
      streamRef.current = null;
    };
  }, [prefs.micDeviceId, prefs.noiseSuppression, prefs.echoCancellation, prefs.autoGainControl]);

  useEffect(() => onDeviceChange(() => void listDevices().then(setDevices)), []);

  const bars = 18;
  const lit = Math.round(level * bars);

  return (
    <div className="space-y-5">
      <Field label="Microphone">
        <Select value={prefs.micDeviceId} onChange={(e) => prefs.set('micDeviceId', e.target.value)}>
          <option value="">System default</option>
          {devices.mics.map((d, i) => (
            <option key={d.deviceId} value={d.deviceId}>
              {deviceLabel(d, i)}
            </option>
          ))}
        </Select>
      </Field>

      <div className="flex items-center gap-3">
        <span className={cn('size-9 rounded-xl inline-flex items-center justify-center', micError ? 'bg-danger/15 text-danger' : 'bg-panel text-muted')}>
          {micError ? <MicOff className="size-4" /> : <Mic className="size-4" />}
        </span>
        <div className="flex-1">
          <div className="flex gap-[3px] h-4 items-end" aria-label="Microphone level" role="meter" aria-valuenow={Math.round(level * 100)} aria-valuemin={0} aria-valuemax={100}>
            {Array.from({ length: bars }, (_, i) => (
              <span
                key={i}
                className={cn('flex-1 rounded-sm transition-colors duration-75', i < lit ? (i > bars * 0.8 ? 'bg-danger' : i > bars * 0.55 ? 'bg-accent' : 'bg-ok') : 'bg-hairline-strong')}
                style={{ height: `${40 + (i / bars) * 60}%` }}
              />
            ))}
          </div>
          <div className="text-xs text-muted mt-1">{micError ? `Mic unavailable: ${micError}` : 'Say something — the bars should move.'}</div>
        </div>
      </div>

      {supportsSinkId ? (
        <Field label="Speaker" group>
          <div className="flex gap-2">
            <Select value={prefs.speakerDeviceId} onChange={(e) => prefs.set('speakerDeviceId', e.target.value)} className="flex-1">
              <option value="">System default</option>
              {devices.speakers.map((d, i) => (
                <option key={d.deviceId} value={d.deviceId}>
                  {deviceLabel(d, i)}
                </option>
              ))}
            </Select>
            <Button
              loading={toneBusy}
              onClick={async () => {
                setToneBusy(true);
                await unlockAudio();
                await playTestTone(prefs.speakerDeviceId || undefined).catch(() => undefined);
                setToneBusy(false);
              }}
            >
              <Volume2 className="size-4" /> Test
            </Button>
          </div>
        </Field>
      ) : null}

      <div className="rounded-xl bg-panel border border-hairline px-3 divide-y divide-hairline">
        <Switch label="Noise suppression" hint="Browser-side; helps with fans and keyboards." checked={prefs.noiseSuppression} onCheckedChange={(v) => prefs.set('noiseSuppression', v)} />
        <Switch label="Join muted" checked={prefs.joinMuted} onCheckedChange={(v) => prefs.set('joinMuted', v)} />
      </div>

      <div className="space-y-2 text-xs text-muted">
        <p className="flex items-start gap-2">
          <Headphones className="size-4 shrink-0 text-accent" />
          Headphones are strongly recommended: they keep the movie out of everyone else's ears.
        </p>
        <p className={cn('flex items-start gap-2', isSafari && 'text-warn')}>
          <Info className="size-4 shrink-0" />
          {isSafari
            ? 'Safari works, but Chrome or Firefox give noticeably smoother playback (they let us buffer the stream).'
            : isFirefox
              ? 'Firefox: good choice. Chrome is equally smooth.'
              : 'Chrome and Firefox give the smoothest playback.'}
        </p>
      </div>

      <div className="flex gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy}>
          Back
        </Button>
        <Button variant="primary" size="lg" className="flex-1" loading={busy} onClick={onJoin}>
          Join the room
        </Button>
      </div>
    </div>
  );
}
