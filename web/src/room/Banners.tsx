import type { Room } from 'livekit-client';
import { LoaderCircle, RefreshCw, Volume2, WifiOff } from 'lucide-react';
import { useState } from 'react';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Spinner } from '@/ui/Spinner';

/** Connection state banners and the reconnect card. */
export function Banners({ room, onReconnect, onLeave }: { room: Room | null; onReconnect: () => Promise<void>; onLeave: () => void }) {
  const phase = useSession((s) => s.phase);
  const error = useSession((s) => s.error);
  const audioBlocked = useSession((s) => s.audioBlocked);
  const [busy, setBusy] = useState(false);

  return (
    <>
      {phase === 'reconnecting' && (
        <div className="anim-fade-in shrink-0 flex items-center justify-center gap-2 h-9 text-[13px] bg-warn/15 text-warn border-b border-warn/30">
          <LoaderCircle className="size-4 anim-spin" /> Reconnecting…
        </div>
      )}
      {audioBlocked && phase === 'connected' && (
        <button
          className="anim-fade-in shrink-0 flex items-center justify-center gap-2 h-9 w-full text-[13px] bg-accent text-accent-ink font-medium"
          onClick={() => {
            void room?.startAudio().then(() => useSession.getState().setAudioBlocked(!room.canPlaybackAudio));
          }}
        >
          <Volume2 className="size-4" /> Click to enable audio
        </button>
      )}
      {phase === 'connecting' && (
        <div className="absolute inset-0 z-50 bg-black/60 backdrop-blur-sm flex items-center justify-center">
          <div className="anim-pop glass-strong rounded-2xl px-6 py-5 flex items-center gap-3 text-sm">
            <Spinner size={20} /> Reconnecting with a fresh token…
          </div>
        </div>
      )}
      {(phase === 'disconnected' || phase === 'failed') && (
        <div className="absolute inset-0 z-50 bg-black/70 backdrop-blur-sm flex items-center justify-center p-6">
          <div className="anim-pop glass-strong rounded-2xl p-6 w-full max-w-sm text-center">
            <div className="mx-auto size-12 rounded-xl bg-danger/15 text-danger flex items-center justify-center mb-3">
              <WifiOff className="size-6" />
            </div>
            <h2 className="text-lg font-semibold">{phase === 'failed' ? "Couldn't connect" : 'Disconnected'}</h2>
            <p className="text-muted text-sm mt-1">{error ?? 'The connection to the room was lost.'}</p>
            <div className="mt-5 flex gap-2 justify-center">
              <Button variant="ghost" onClick={onLeave}>
                Leave
              </Button>
              <Button
                variant="primary"
                loading={busy}
                onClick={async () => {
                  setBusy(true);
                  await onReconnect();
                  setBusy(false);
                }}
              >
                <RefreshCw className="size-4" /> Reconnect
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
