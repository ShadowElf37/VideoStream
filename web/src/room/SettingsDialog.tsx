import { LocalAudioTrack, Room, Track } from 'livekit-client';
import { Volume2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { setContextSink } from '@/audio/context';
import { deviceLabel, listDevices, onDeviceChange, type DeviceLists } from '@/audio/devices';
import { playTestTone } from '@/audio/tone';
import { useAudio } from '@/audio/useAudioModel';
import { api } from '@/lib/api';
import { isSafari, supportsJitterBufferTarget, supportsSinkId } from '@/lib/platform';
import type { QualityPreset, RoomSettings } from '@/proto/messages';
import { applyTheme, usePrefs, type DuckDb, type QualityPref, type Theme } from '@/state/prefs';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Dialog } from '@/ui/Dialog';
import { Field, Select } from '@/ui/Field';
import { Kbd } from '@/ui/Kbd';
import { Slider } from '@/ui/Slider';
import { Switch } from '@/ui/Switch';
import { PRESETS } from './HostBar';
import { SHORTCUTS } from './useKeyboard';

export function SettingsDialog({ open, onOpenChange, room }: { open: boolean; onOpenChange: (o: boolean) => void; room: Room }) {
  const prefs = usePrefs();
  const { state, dispatch } = useAudio();
  const role = useSession((s) => s.role);
  const settings = useSession((s) => s.settings);
  const token = useSession((s) => s.token);
  const roomId = useSession((s) => s.roomId);
  const [devices, setDevices] = useState<DeviceLists>({ mics: [], speakers: [] });
  const [toneBusy, setToneBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    const refresh = () => void listDevices().then(setDevices);
    refresh();
    return onDeviceChange(refresh);
  }, [open]);

  const micTrack = (): LocalAudioTrack | undefined => {
    const t = room.localParticipant.getTrackPublication(Track.Source.Microphone)?.track;
    return t instanceof LocalAudioTrack ? t : undefined;
  };

  const reacquireMic = async (patch: Partial<{ noiseSuppression: boolean; echoCancellation: boolean; autoGainControl: boolean }>) => {
    prefs.patch(patch);
    const p = { ...usePrefs.getState(), ...patch };
    const opts = { echoCancellation: p.echoCancellation, noiseSuppression: p.noiseSuppression, autoGainControl: p.autoGainControl };
    room.options.audioCaptureDefaults = { ...room.options.audioCaptureDefaults, ...opts };
    try {
      await micTrack()?.restartTrack(opts);
    } catch (e) {
      useSession.getState().toast('Could not re-open the microphone', 'warn');
      console.warn(e);
    }
  };

  const setMic = async (id: string) => {
    prefs.set('micDeviceId', id);
    try {
      await room.switchActiveDevice('audioinput', id);
    } catch (e) {
      useSession.getState().toast('Could not switch microphone', 'warn');
      console.warn(e);
    }
  };
  const setSpeaker = async (id: string) => {
    prefs.set('speakerDeviceId', id);
    try {
      await room.switchActiveDevice('audiooutput', id);
      await setContextSink(id);
    } catch (e) {
      useSession.getState().toast('Could not switch speaker', 'warn');
      console.warn(e);
    }
  };

  const patchRoom = async (patch: Partial<RoomSettings>) => {
    if (!token) return;
    try {
      const next = await api.patchSettings(roomId, token.session, patch);
      useSession.getState().setSettings(next);
    } catch (e) {
      useSession.getState().toast('Could not update room settings', 'error');
      console.warn(e);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Settings" description="Everything here is remembered on this device.">
      <div className="space-y-6">
        <Section title="Devices">
          <Field label="Microphone">
            <Select value={prefs.micDeviceId} onChange={(e) => void setMic(e.target.value)}>
              <option value="">System default</option>
              {devices.mics.map((d, i) => (
                <option key={d.deviceId} value={d.deviceId}>
                  {deviceLabel(d, i)}
                </option>
              ))}
            </Select>
          </Field>
          {supportsSinkId ? (
            <Field label="Speaker" group>
              <div className="flex gap-2">
                <Select value={prefs.speakerDeviceId} onChange={(e) => void setSpeaker(e.target.value)} className="flex-1">
                  <option value="">System default</option>
                  {devices.speakers.map((d, i) => (
                    <option key={d.deviceId} value={d.deviceId}>
                      {deviceLabel(d, i)}
                    </option>
                  ))}
                </Select>
                <Button
                  size="md"
                  loading={toneBusy}
                  onClick={async () => {
                    setToneBusy(true);
                    await playTestTone(prefs.speakerDeviceId || undefined).catch(() => undefined);
                    setToneBusy(false);
                  }}
                >
                  <Volume2 className="size-4" /> Test
                </Button>
              </div>
            </Field>
          ) : (
            <p className="text-xs text-muted">Speaker selection isn't available in this browser; it uses the system output.</p>
          )}
        </Section>

        <Section title="Microphone processing" hint="Changing these re-opens the mic.">
          <Switch label="Noise suppression" checked={prefs.noiseSuppression} onCheckedChange={(v) => void reacquireMic({ noiseSuppression: v })} />
          <Switch label="Echo cancellation" hint="Keeps the movie out of your mic when on speakers." checked={prefs.echoCancellation} onCheckedChange={(v) => void reacquireMic({ echoCancellation: v })} />
          <Switch label="Auto gain control" checked={prefs.autoGainControl} onCheckedChange={(v) => void reacquireMic({ autoGainControl: v })} />
        </Section>

        <Section title="Voice">
          <Switch
            label="Deafen also mutes my mic"
            hint="Off = deafen and mute are fully independent."
            checked={state.deafenImpliesMute}
            onCheckedChange={(v) => dispatch({ type: 'setDeafenImpliesMute', value: v })}
          />
          <Switch label="Push to talk" hint="Hold V to talk." checked={state.ptt} onCheckedChange={(v) => dispatch({ type: 'setPtt', enabled: v })} />
          <Field label="Duck the movie when someone talks" className="pt-1">
            <Select value={String(state.duckDb)} onChange={(e) => dispatch({ type: 'setDuckDb', db: Number(e.target.value) as DuckDb })}>
              <option value="0">Off</option>
              <option value="-6">−6 dB (gentle)</option>
              <option value="-12">−12 dB (strong)</option>
            </Select>
          </Field>
        </Section>

        <Section title="Movie playback">
          {supportsJitterBufferTarget ? (
            <Field
              label={`Smoothness · ${prefs.smoothnessSec.toFixed(1)} s buffer`}
              hint="How far behind the projector your picture runs. Higher rides out shaky Wi-Fi; lower makes pause and seek feel immediate, because what you see is closer to live. Applies to video and audio together."
            >
              <Slider label="Smoothness" min={0.2} max={2.5} step={0.1} value={prefs.smoothnessSec} onChange={(v) => prefs.set('smoothnessSec', +v.toFixed(1))} ticks={[1.5]} accent />
            </Field>
          ) : (
            <p className="text-xs text-muted">
              {isSafari ? 'Safari' : 'This browser'} can't set a playback buffer target, so it runs on default buffers. Chrome or Firefox give the smoothest playback.
            </p>
          )}
          <Field label="Quality preference" hint="Only matters when the projector publishes more than one layer.">
            <Select value={prefs.qualityPref} onChange={(e) => prefs.set('qualityPref', e.target.value as QualityPref)}>
              <option value="auto">Auto</option>
              <option value="high">Highest available</option>
              <option value="low">Lowest (save bandwidth)</option>
            </Select>
          </Field>
          <Switch label="Stats overlay" hint="Bitrate, fps, jitter, loss and the projector's encoder state." checked={prefs.statsOverlay} onCheckedChange={(v) => prefs.set('statsOverlay', v)} />
        </Section>

        <Section title="Appearance & notifications">
          <Field label="Theme">
            <Select
              value={prefs.theme}
              onChange={(e) => {
                const t = e.target.value as Theme;
                prefs.set('theme', t);
                applyTheme(t);
              }}
            >
              <option value="dark">Dark (cinema)</option>
              <option value="light">Light</option>
            </Select>
          </Field>
          <Switch label="Sound on new message" hint="Only while this tab is in the background." checked={prefs.notificationSounds} onCheckedChange={(v) => prefs.set('notificationSounds', v)} />
        </Section>

        {role === 'host' && settings && (
          <Section title="Room (host)" hint="Applies to everyone.">
            <Switch label="Anyone can pause" hint="Otherwise viewers can only ask." checked={settings.anyoneCanPause} onCheckedChange={(v) => void patchRoom({ anyoneCanPause: v })} />
            <Switch label="Default: deafen implies mute" hint="Suggested default for new joiners; everyone can override." checked={settings.deafenImpliesMute} onCheckedChange={(v) => void patchRoom({ deafenImpliesMute: v })} />
            <Field label="Maximum quality preset">
              <Select value={settings.maxPreset} onChange={(e) => void patchRoom({ maxPreset: e.target.value as QualityPreset })}>
                {PRESETS.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </Select>
            </Field>
          </Section>
        )}

        <Section title="Keyboard shortcuts">
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13px]">
            {SHORTCUTS.map(([k, d]) => (
              <span key={k} className="contents">
                <dt>
                  <Kbd>{k}</Kbd>
                </dt>
                <dd className="text-muted">{d}</dd>
              </span>
            ))}
          </dl>
        </Section>
      </div>
    </Dialog>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="text-[11px] uppercase tracking-wider text-muted mb-2">
        {title}
        {hint && <span className="normal-case tracking-normal ml-2 opacity-80">· {hint}</span>}
      </h3>
      <div className="space-y-3">{children}</div>
    </section>
  );
}
