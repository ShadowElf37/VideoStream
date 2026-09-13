import { RoomContext } from '@livekit/components-react';
import type { Room } from 'livekit-client';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import { AudioModelContext, useAudioModel } from '@/audio/useAudioModel';
import { MpvContext, useMpvPlumbing, useMpvStore } from '@/host/useMpv';
import { api, ApiError } from '@/lib/api';
import { cn } from '@/lib/cn';
import type { RoomInfo } from '@/proto/messages';
import { useChat } from '@/state/chat';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Logo } from '@/ui/Logo';
import { Spinner } from '@/ui/Spinner';
import { AudioRenderer } from './AudioRenderer';
import { Banners } from './Banners';
import { DeviceCheck } from './DeviceCheck';
import { Dock } from './Dock';
import { JoinCard } from './JoinCard';
import { SettingsDialog } from './SettingsDialog';
import { Sidebar, type SidebarMode } from './Sidebar';
import { Stage } from './Stage';
import { useAutoHide, useFullscreen, useIsNarrow } from './hooks';
import { useGlobalKeys } from './useKeyboard';
import { useRoomConnection } from './useRoomConnection';
import { useRoomEvents } from './useRoomEvents';

export function RoomPage() {
  const { id = '' } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const inviteKey = params.get('k') ?? undefined;
  const hostSecret = params.get('h') ?? undefined;
  const [info, setInfo] = useState<RoomInfo | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [step, setStep] = useState<'who' | 'devices'>('who');
  const [who, setWho] = useState<{ name: string; password: string } | null>(null);
  const [joining, setJoining] = useState(false);
  const phase = useSession((s) => s.phase);
  const conn = useRoomConnection(id);

  useEffect(() => {
    useSession.getState().setCredentials({ inviteKey, hostSecret });
  }, [inviteKey, hostSecret]);

  useEffect(() => {
    let cancelled = false;
    setInfo(null);
    setLoadError(null);
    api
      .getRoom(id)
      .then((r) => {
        if (cancelled) return;
        setInfo(r);
        useSession.getState().setRoom(id, r);
      })
      .catch((e) => {
        if (cancelled) return;
        setLoadError(e instanceof ApiError && e.status === 404 ? 'This room does not exist (or has expired).' : e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  useEffect(() => {
    document.title = info ? `${info.name || 'Room'} · VideoStream` : 'VideoStream';
    return () => {
      document.title = 'VideoStream';
    };
  }, [info]);

  const join = async () => {
    if (!who) return;
    setJoining(true);
    try {
      await conn.connect({
        name: who.name,
        password: who.password || undefined,
        micDeviceId: usePrefs.getState().micDeviceId || undefined,
        joinMuted: usePrefs.getState().joinMuted,
      });
    } catch {
      /* phase/error are in the session store */
    } finally {
      setJoining(false);
    }
  };

  const leave = async () => {
    await conn.leave();
    useMpvStore.getState().reset();
    navigate('/');
  };

  // Once a Room exists we stay in the room view; reconnects swap the Room underneath.
  const inRoom = conn.room !== null;

  if (loadError) {
    return (
      <Shell>
        <h1 className="text-lg font-semibold">Can't open this room</h1>
        <p className="text-muted text-sm mt-1">{loadError}</p>
        <Button className="mt-5" onClick={() => navigate('/')}>
          Back to start
        </Button>
      </Shell>
    );
  }
  if (!info) {
    return (
      <Shell>
        <div className="flex items-center gap-3 text-muted">
          <Spinner size={20} /> Loading room…
        </div>
      </Shell>
    );
  }
  if (!inRoom) {
    const failed = phase === 'failed';
    return (
      <Shell title={info.name || 'Watch party'} subtitle={step === 'who' ? 'Who are you?' : 'Quick sound check'}>
        {failed && <div className="mb-4 rounded-xl bg-danger/10 border border-danger/30 text-danger text-[13px] px-3 py-2">{useSession.getState().error}</div>}
        {step === 'who' ? (
          <JoinCard
            room={info}
            isHost={!!hostSecret}
            onNext={(name, password) => {
              setWho({ name, password });
              setStep('devices');
            }}
          />
        ) : (
          <DeviceCheck onJoin={() => void join()} onBack={() => setStep('who')} busy={joining || phase === 'connecting'} />
        )}
      </Shell>
    );
  }

  return <RoomView room={conn.room!} connected={conn.connected} onLeave={() => void leave()} onReconnect={conn.reconnect} />;
}

function Shell({ title, subtitle, children }: { title?: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div className="min-h-full flex items-center justify-center p-6 bg-[radial-gradient(ellipse_at_top,rgba(245,185,66,0.08),transparent_60%)]">
      <div className="anim-pop w-full max-w-md glass-strong rounded-2xl p-6">
        <Link to="/" className="inline-flex items-center gap-2 text-muted hover:text-text text-xs mb-4">
          <Logo size={20} /> VideoStream
        </Link>
        {title && <h1 className="text-xl font-semibold tracking-tight">{title}</h1>}
        {subtitle && <p className="text-muted text-sm mt-0.5 mb-5">{subtitle}</p>}
        {children}
      </div>
    </div>
  );
}

function RoomView({ room, connected, onLeave, onReconnect }: { room: Room; connected: boolean; onLeave: () => void; onReconnect: () => Promise<void> }) {
  const audio = useAudioModel(room, connected);
  const mpv = useMpvPlumbing(room, connected);
  useRoomEvents(room);

  const videoRef = useRef<HTMLVideoElement>(null);
  const fs = useFullscreen();
  const narrow = useIsNarrow();
  const sidebarOpen = usePrefs((s) => s.sidebarOpen);
  const setPref = usePrefs((s) => s.set);
  const unread = useChat((s) => s.unread);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [reactionsOpen, setReactionsOpen] = useState(false);
  const dockHide = useAutoHide(2500, fs.active);

  useEffect(() => useSession.getState().setFullscreen(fs.active), [fs.active]);

  const stacked = narrow && !fs.active;
  const mode: SidebarMode = fs.active ? 'drawer' : stacked ? 'stacked' : 'inline';
  const openQueue = useCallback(() => {
    setPref('sidebarTab', 'queue');
    setPref('sidebarOpen', true);
  }, [setPref]);

  const { dispatch } = audio;
  const keys = useMemo(
    () => ({
      toggleMic: () => dispatch({ type: 'toggleMic' }),
      toggleDeafen: () => dispatch({ type: 'toggleDeafen' }),
      toggleMovieMuted: () => dispatch({ type: 'toggleMovieMuted' }),
      toggleReactions: () => setReactionsOpen((o) => !o),
      toggleFullscreen: () => void fs.toggle(),
      toggleSidebar: () => setPref('sidebarOpen', !usePrefs.getState().sidebarOpen),
      pttDown: () => dispatch({ type: 'pttDown' }),
      pttUp: () => dispatch({ type: 'pttUp' }),
      openHelp: () => setSettingsOpen(true),
    }),
    [dispatch, fs, setPref],
  );
  useGlobalKeys(keys, !settingsOpen);

  const pip = async () => {
    const v = videoRef.current;
    if (!v) return;
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await v.requestPictureInPicture();
    } catch (e) {
      useSession.getState().toast('Picture-in-picture unavailable', 'warn');
      console.warn(e);
    }
  };

  return (
    <RoomContext.Provider value={room}>
      <AudioModelContext.Provider value={audio}>
        <MpvContext.Provider value={mpv}>
          <div className={cn('relative h-full flex flex-col bg-ground text-text', fs.active && 'bg-black')}>
            <AudioRenderer />
            <Banners room={room} onReconnect={onReconnect} onLeave={onLeave} />
            <div className={cn('flex-1 min-h-0 flex', stacked && 'flex-col')}>
              <main className={cn('min-w-0 min-h-0 relative', stacked && sidebarOpen ? 'aspect-video flex-none w-full' : 'flex-1')}>
                <Stage onOpenQueue={openQueue} onToggleFullscreen={() => void fs.toggle()} videoRef={videoRef} />
              </main>
              <Sidebar open={sidebarOpen} mode={mode} onClose={() => setPref('sidebarOpen', false)} />
            </div>
            <Dock
              visible={dockHide.visible}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setPref('sidebarOpen', !sidebarOpen)}
              isFullscreen={fs.active}
              onToggleFullscreen={() => void fs.toggle()}
              onOpenSettings={() => setSettingsOpen(true)}
              onLeave={onLeave}
              onPiP={pip}
              reactionsOpen={reactionsOpen}
              setReactionsOpen={setReactionsOpen}
              unread={unread}
            />
            <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} room={room} />
          </div>
        </MpvContext.Provider>
      </AudioModelContext.Provider>
    </RoomContext.Provider>
  );
}
