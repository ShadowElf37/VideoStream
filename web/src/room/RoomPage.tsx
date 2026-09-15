import { RoomContext } from '@livekit/components-react';
import type { Room } from 'livekit-client';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router';
import { AudioModelContext, useAudioModel } from '@/audio/useAudioModel';
import { MpvContext, useMpvPlumbing, useMpvStore } from '@/host/useMpv';
import { api } from '@/lib/api';
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
import { InviteDialog } from './InviteDialog';
import { JoinCard } from './JoinCard';
import { RoomKeys } from './RoomKeys';
import { SettingsDialog } from './SettingsDialog';
import { Sidebar, type SidebarMode } from './Sidebar';
import { Stage } from './Stage';
import { useAutoHide, useFullscreen, useIsNarrow } from './hooks';
import { useRoomConnection } from './useRoomConnection';
import { useRoomEvents } from './useRoomEvents';

/**
 * The site is one room, and this page is its door and its inside. A visitor
 * arrives with a key in the URL, a cookie from last time, or nothing; the
 * door asks for exactly what is missing, and the room view takes over once
 * a LiveKit Room exists.
 */
export function RoomPage() {
  const [params] = useSearchParams();
  const key = params.get('k') ?? undefined;
  const [info, setInfo] = useState<RoomInfo | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [step, setStep] = useState<'who' | 'devices'>('who');
  const [who, setWho] = useState<{ name: string; password: string } | null>(null);
  const [joining, setJoining] = useState(false);
  const phase = useSession((s) => s.phase);
  const linkExpired = useSession((s) => s.linkExpired);
  const conn = useRoomConnection();

  useEffect(() => {
    useSession.getState().setCredentials({ key });
  }, [key]);

  // What does the visitor already hold? Asked on arrival, and again whenever
  // we land back at the door (the cookie may now say more than the URL).
  const askDoor = useCallback(() => {
    setLoadError(null);
    api
      .getRoom(key)
      .then((r) => {
        setInfo(r);
        useSession.getState().setAccess(r.access);
      })
      .catch((e) => setLoadError(e instanceof Error ? e.message : String(e)));
  }, [key]);
  useEffect(() => {
    setInfo(null);
    askDoor();
  }, [askDoor]);
  useEffect(() => {
    if (linkExpired) {
      setStep('who');
      askDoor();
    }
  }, [linkExpired, askDoor]);

  useEffect(() => {
    document.title = 'VideoStream';
  }, []);

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
    setStep('who');
    askDoor();
  };

  // Once a Room exists we stay in the room view; reconnects swap the Room underneath.
  const inRoom = conn.room !== null;

  if (loadError) {
    return (
      <Shell>
        <h1 className="text-lg font-semibold">Can't reach the room</h1>
        <p className="text-muted text-sm mt-1">{loadError}</p>
        <Button className="mt-5" onClick={askDoor}>
          Try again
        </Button>
      </Shell>
    );
  }
  if (!info) {
    return (
      <Shell>
        <div className="flex items-center gap-3 text-muted">
          <Spinner size={20} /> One moment…
        </div>
      </Shell>
    );
  }
  if (!inRoom) {
    const failed = phase === 'failed';
    const error = useSession.getState().error;
    return (
      <Shell title="Movie night" subtitle={step === 'who' ? 'Who are you?' : 'Quick sound check'}>
        {failed && !linkExpired && <div className="mb-4 rounded-xl bg-danger/10 border border-danger/30 text-danger text-[13px] px-3 py-2">{error}</div>}
        {step === 'who' ? (
          <JoinCard
            access={info.access}
            hadKey={!!key}
            occupants={info.occupants}
            notice={linkExpired ? 'You were disconnected and the invite link has changed in the meantime.' : null}
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
        <div className="inline-flex items-center gap-2 text-muted text-xs mb-4">
          <Logo size={20} /> VideoStream
        </div>
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
  const inviteOpen = useSession((s) => s.inviteOpen);
  const setInviteOpen = useSession((s) => s.setInviteOpen);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [reactionsOpen, setReactionsOpen] = useState(false);
  const dockHide = useAutoHide(2500, fs.active);

  useEffect(() => useSession.getState().setFullscreen(fs.active), [fs.active]);

  const stacked = narrow && !fs.active;
  const mode: SidebarMode = fs.active ? 'drawer' : stacked ? 'stacked' : 'inline';
  const openLibrary = useCallback(() => {
    setPref('sidebarTab', 'library');
    setPref('sidebarOpen', true);
  }, [setPref]);

  const toggleReactions = useCallback(() => setReactionsOpen((o) => !o), []);
  const toggleFullscreen = useCallback(() => void fs.toggle(), [fs]);
  const openHelp = useCallback(() => setSettingsOpen(true), []);

  return (
    <RoomContext.Provider value={room}>
      <AudioModelContext.Provider value={audio}>
        <MpvContext.Provider value={mpv}>
          <div className={cn('relative h-full flex flex-col bg-ground text-text', fs.active && 'bg-black')}>
            <AudioRenderer />
            <RoomKeys enabled={!settingsOpen && !inviteOpen} toggleReactions={toggleReactions} toggleFullscreen={toggleFullscreen} openHelp={openHelp} />
            <Banners room={room} onReconnect={onReconnect} onLeave={onLeave} />
            <div className={cn('flex-1 min-h-0 flex', stacked && 'flex-col')}>
              <main className={cn('min-w-0 min-h-0 relative', stacked && sidebarOpen ? 'aspect-video flex-none w-full' : 'flex-1')}>
                <Stage onOpenLibrary={openLibrary} onToggleFullscreen={() => void fs.toggle()} videoRef={videoRef} />
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
              onOpenInvite={() => setInviteOpen(true)}
              onLeave={onLeave}
              reactionsOpen={reactionsOpen}
              setReactionsOpen={setReactionsOpen}
              unread={unread}
            />
            <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} room={room} />
            <InviteDialog open={inviteOpen} onOpenChange={setInviteOpen} />
          </div>
        </MpvContext.Provider>
      </AudioModelContext.Provider>
    </RoomContext.Provider>
  );
}
