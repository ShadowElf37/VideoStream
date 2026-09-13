import { useRoomContext } from '@livekit/components-react';
import {
  Film,
  HeadphoneOff,
  Headphones,
  LogOut,
  Maximize,
  Mic,
  MicOff,
  MicVocal,
  Minimize,
  PanelRight,
  PanelRightClose,
  PictureInPicture2,
  Settings,
  Sticker,
  Volume2,
  VolumeX,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { useAudio } from '@/audio/useAudioModel';
import { MOVIE_VOLUME_MAX } from '@/audio/model';
import { cn } from '@/lib/cn';
import { publish } from '@/lib/data';
import { supportsPiP } from '@/lib/platform';
import { Topics } from '@/proto/messages';
import { useSession } from '@/state/session';
import { Popover } from '@/ui/Popover';
import { Slider } from '@/ui/Slider';
import { Tooltip } from '@/ui/Tooltip';

export const REACTION_EMOJI = ['😂', '❤️', '👏', '😮', '🔥', '😭', '👀', '🍿'];

export interface DockProps {
  visible: boolean;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  isFullscreen: boolean;
  onToggleFullscreen: () => void;
  onOpenSettings: () => void;
  onLeave: () => void;
  onPiP?: () => void;
  reactionsOpen: boolean;
  setReactionsOpen: (o: boolean) => void;
  unread: number;
}

export function Dock(p: DockProps) {
  const room = useRoomContext();
  const { state, dispatch, micEnabled } = useAudio();
  const micVisualMuted = !micEnabled;

  const react = async (emoji: string) => {
    p.setReactionsOpen(false);
    useSession.getState().addReaction(emoji, 'you');
    await publish(room, Topics.react, { emoji }, { reliable: false });
  };

  return (
    <div
      className={cn(
        'shrink-0 flex items-center justify-center-safe gap-1 px-3 py-2 overflow-x-auto [&>*]:shrink-0 border-t border-hairline bg-ground/90 backdrop-blur-md transition-[opacity,transform] duration-200 ease-out',
        p.isFullscreen && 'absolute inset-x-0 bottom-0 z-40 border-t-0 bg-gradient-to-t from-black/80 to-transparent',
        p.isFullscreen && !p.visible && 'opacity-0 translate-y-2 pointer-events-none',
      )}
      role="toolbar"
      aria-label="Controls"
    >
      {/* The three audio toggles: distinct icon + colour + label. */}
      <DockButton
        label={micVisualMuted ? 'Unmute mic' : 'Mute mic'}
        kbd="M"
        caption={state.ptt && !state.micMuted ? (state.pttActive ? 'Talking' : 'Hold V') : micVisualMuted ? 'Muted' : 'Mic'}
        active={state.micMuted}
        tone="danger"
        onClick={() => dispatch({ type: 'toggleMic' })}
      >
        {micVisualMuted ? <MicOff /> : <Mic />}
      </DockButton>
      <DockButton
        label={state.deafened ? 'Undeafen (hear voices)' : 'Deafen (silence voices, keep the movie)'}
        kbd="D"
        caption={state.deafened ? 'Deafened' : 'Voices'}
        active={state.deafened}
        tone="warn"
        onClick={() => dispatch({ type: 'toggleDeafen' })}
      >
        {state.deafened ? <HeadphoneOff /> : <Headphones />}
      </DockButton>
      <span className="inline-flex items-end">
        <DockButton
          label={state.movieMuted ? 'Unmute movie' : 'Mute movie audio (voices stay)'}
          kbd="S"
          caption={state.movieMuted ? 'Movie off' : `Movie ${Math.round(state.movieVolume * 100)}%`}
          active={state.movieMuted}
          tone="info"
          onClick={() => dispatch({ type: 'toggleMovieMuted' })}
          className="rounded-r-none"
        >
          {state.movieMuted ? <VolumeX /> : <Film />}
        </DockButton>
        <Popover
          side="top"
          className="w-14 h-44 flex flex-col items-center gap-2 py-3"
          trigger={
            <button
              aria-label="Movie volume"
              className={cn(
                'h-[62px] w-6 -ml-px rounded-r-xl border-l border-hairline text-muted hover:text-text hover:bg-hover inline-flex flex-col items-center justify-center gap-1',
                state.movieMuted && 'text-info',
              )}
            >
              <Volume2 className="size-3.5" />
              {/* Mirrors the caption every other dock button carries. Without
                  it this icon centres in the full height while the rest centre
                  above their labels, and it sits visibly lower than the row. */}
              <span className="text-[10.5px] leading-none" aria-hidden="true">
                &nbsp;
              </span>
            </button>
          }
        >
          <span className="text-[11px] font-mono text-muted">{Math.round(state.movieVolume * 100)}%</span>
          <Slider
            label="Movie volume"
            orientation="vertical"
            min={0}
            max={MOVIE_VOLUME_MAX}
            step={0.05}
            value={state.movieVolume}
            ticks={[1]}
            onChange={(v) => dispatch({ type: 'setMovieVolume', volume: v })}
            accent
          />
        </Popover>
      </span>

      <Divider />

      <Popover
        open={p.reactionsOpen}
        onOpenChange={p.setReactionsOpen}
        className="grid grid-cols-4 gap-1 p-2"
        trigger={
          <DockButtonBase label="Reactions" kbd="R" caption="React" active={p.reactionsOpen}>
            <Sticker />
          </DockButtonBase>
        }
      >
        {REACTION_EMOJI.map((e) => (
          <button key={e} onClick={() => void react(e)} className="size-10 rounded-lg text-2xl hover:bg-hover active:scale-95 transition-transform" aria-label={`React ${e}`}>
            {e}
          </button>
        ))}
      </Popover>

      <DockButton
        label={state.ptt ? 'Push-to-talk on (hold V to talk)' : 'Enable push-to-talk'}
        kbd="V"
        caption={state.ptt ? 'PTT on' : 'PTT'}
        active={state.ptt}
        tone="accent"
        onClick={() => dispatch({ type: 'setPtt', enabled: !state.ptt })}
      >
        <MicVocal />
      </DockButton>

      <Divider />

      {supportsPiP && p.onPiP && (
        <DockButton label="Picture in picture" caption="PiP" onClick={p.onPiP}>
          <PictureInPicture2 />
        </DockButton>
      )}
      <DockButton label={p.isFullscreen ? 'Exit fullscreen' : 'Fullscreen'} kbd="F" caption={p.isFullscreen ? 'Exit' : 'Full'} onClick={p.onToggleFullscreen}>
        {p.isFullscreen ? <Minimize /> : <Maximize />}
      </DockButton>
      <DockButton label={p.sidebarOpen ? 'Hide sidebar' : 'Show sidebar'} kbd="C" caption="Sidebar" onClick={p.onToggleSidebar} badge={p.sidebarOpen ? 0 : p.unread}>
        {p.sidebarOpen ? <PanelRightClose /> : <PanelRight />}
      </DockButton>
      <DockButton label="Settings" caption="Settings" onClick={p.onOpenSettings}>
        <Settings />
      </DockButton>

      <Divider />

      <DockButton label="Leave the room" caption="Leave" onClick={p.onLeave} className="hover:text-danger">
        <LogOut />
      </DockButton>
    </div>
  );
}

function Divider() {
  return <span className="mx-1 h-8 w-px bg-hairline" aria-hidden />;
}

const tones = {
  default: 'bg-active text-text',
  danger: 'bg-danger/20 text-danger ring-1 ring-danger/40',
  warn: 'bg-warn/20 text-warn ring-1 ring-warn/40',
  info: 'bg-info/20 text-info ring-1 ring-info/40',
  accent: 'bg-accent/20 text-accent ring-1 ring-accent/40',
};

interface DockButtonProps {
  label: string;
  kbd?: string;
  caption: string;
  active?: boolean;
  tone?: keyof typeof tones;
  onClick?: () => void;
  className?: string;
  badge?: number;
  children: ReactNode;
}

function DockButtonBase({ label, kbd, caption, active, tone = 'default', onClick, className, badge, children, ...rest }: DockButtonProps & Record<string, unknown>) {
  return (
    <Tooltip label={label} kbd={kbd}>
      <button
        aria-label={label}
        aria-pressed={active}
        onClick={onClick}
        className={cn(
          'relative h-[62px] min-w-[56px] sm:min-w-[64px] px-2 rounded-xl flex flex-col items-center justify-center gap-1 text-muted hover:text-text hover:bg-hover active:bg-active transition-[background,color] duration-150 [&>svg]:size-[22px]',
          active && tones[tone],
          className,
        )}
        {...rest}
      >
        {children}
        <span className="text-[10.5px] font-medium leading-none tracking-wide">{caption}</span>
        {badge ? (
          <span className="absolute top-1.5 right-1.5 min-w-[16px] h-4 px-1 rounded-full bg-accent text-accent-ink text-[10px] font-semibold inline-flex items-center justify-center leading-none">
            {badge > 99 ? '99+' : badge}
          </span>
        ) : null}
      </button>
    </Tooltip>
  );
}

const DockButton = DockButtonBase;
