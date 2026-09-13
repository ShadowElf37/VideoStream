export interface DeviceLists {
  mics: MediaDeviceInfo[];
  speakers: MediaDeviceInfo[];
}

/** Enumerate devices; labels are only populated after a permission grant. */
export async function listDevices(): Promise<DeviceLists> {
  if (!navigator.mediaDevices?.enumerateDevices) return { mics: [], speakers: [] };
  const all = await navigator.mediaDevices.enumerateDevices();
  return {
    mics: all.filter((d) => d.kind === 'audioinput'),
    speakers: all.filter((d) => d.kind === 'audiooutput'),
  };
}

export function deviceLabel(d: MediaDeviceInfo, index: number): string {
  if (d.label) return d.label;
  if (d.deviceId === 'default') return 'System default';
  return `${d.kind === 'audioinput' ? 'Microphone' : 'Speaker'} ${index + 1}`;
}

export interface MicConstraints {
  deviceId?: string;
  echoCancellation: boolean;
  noiseSuppression: boolean;
  autoGainControl: boolean;
}

export function micConstraints(c: MicConstraints): MediaTrackConstraints {
  return {
    deviceId: c.deviceId ? { ideal: c.deviceId } : undefined,
    echoCancellation: c.echoCancellation,
    noiseSuppression: c.noiseSuppression,
    autoGainControl: c.autoGainControl,
    channelCount: 1,
  };
}

/** Open a preview mic stream for the device check. Caller must stop the tracks. */
export async function openMicPreview(c: MicConstraints): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({ audio: micConstraints(c), video: false });
}

export function stopStream(stream: MediaStream | null | undefined) {
  stream?.getTracks().forEach((t) => t.stop());
}

/** Subscribe to device hot-plug; returns an unsubscribe. */
export function onDeviceChange(cb: () => void): () => void {
  const md = navigator.mediaDevices;
  if (!md?.addEventListener) return () => {};
  md.addEventListener('devicechange', cb);
  return () => md.removeEventListener('devicechange', cb);
}
