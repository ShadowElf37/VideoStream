import { ListVideo, MessageSquare, Users, X } from 'lucide-react';
import { useCallback, useEffect, useRef } from 'react';
import { cn } from '@/lib/cn';
import { clamp } from '@/lib/format';
import { useChat } from '@/state/chat';
import { usePrefs, type SidebarTab } from '@/state/prefs';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/ui/Tabs';
import { ChatTab } from './ChatTab';
import { PeopleTab } from './PeopleTab';
import { QueueTab } from './QueueTab';

/** inline: desktop column · drawer: overlay in fullscreen · stacked: bottom sheet under the stage on narrow screens */
export type SidebarMode = 'inline' | 'drawer' | 'stacked';

const MIN_W = 280;
const MAX_W = 600;

export function Sidebar({ open, mode, onClose }: { open: boolean; mode: SidebarMode; onClose: () => void }) {
  const width = usePrefs((s) => s.sidebarWidth);
  const tab = usePrefs((s) => s.sidebarTab);
  const setPref = usePrefs((s) => s.set);
  const unread = useChat((s) => s.unread);
  const clearUnread = useChat((s) => s.clearUnread);
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open && tab === 'chat') clearUnread();
  }, [open, tab, clearUnread]);

  // Resize by dragging the left edge (inline / drawer modes).
  const onResizeStart = useCallback(
    (e: React.PointerEvent) => {
      const startX = e.clientX;
      const startW = width;
      const target = e.currentTarget as HTMLElement;
      target.setPointerCapture(e.pointerId);
      const move = (ev: PointerEvent) => setPref('sidebarWidth', clamp(startW + (startX - ev.clientX), MIN_W, MAX_W));
      const up = () => {
        target.removeEventListener('pointermove', move);
        target.removeEventListener('pointerup', up);
      };
      target.addEventListener('pointermove', move);
      target.addEventListener('pointerup', up);
    },
    [width, setPref],
  );

  const body = (
    <Tabs value={tab} onValueChange={(v) => setPref('sidebarTab', v as SidebarTab)} className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-2 p-2 pl-3 border-b border-hairline">
        <TabsList className="flex-1">
          <TabsTrigger value="chat" badge={tab !== 'chat' ? unread : 0}>
            <MessageSquare className="size-4" /> Chat
          </TabsTrigger>
          <TabsTrigger value="people">
            <Users className="size-4" /> People
          </TabsTrigger>
          <TabsTrigger value="queue">
            <ListVideo className="size-4" /> Queue
          </TabsTrigger>
        </TabsList>
        {mode !== 'inline' && (
          <button onClick={onClose} aria-label="Close sidebar" className="size-8 rounded-lg inline-flex items-center justify-center text-muted hover:text-text hover:bg-hover">
            <X className="size-4" />
          </button>
        )}
      </div>
      <TabsContent value="chat" className="flex-1 min-h-0 outline-none data-[state=inactive]:hidden">
        <ChatTab active={open && tab === 'chat'} />
      </TabsContent>
      <TabsContent value="people" className="flex-1 min-h-0 overflow-y-auto outline-none data-[state=inactive]:hidden">
        <PeopleTab />
      </TabsContent>
      <TabsContent value="queue" className="flex-1 min-h-0 outline-none data-[state=inactive]:hidden">
        <QueueTab active={open && tab === 'queue'} />
      </TabsContent>
    </Tabs>
  );

  if (mode === 'stacked') {
    if (!open) return null;
    return (
      <div className="anim-fade-in flex-1 min-h-0 w-full border-t border-hairline bg-ground rounded-t-2xl flex flex-col" aria-label="Sidebar">
        <div className="mx-auto mt-2 h-1 w-10 rounded-full bg-hairline-strong" />
        <div className="flex-1 min-h-0">{body}</div>
      </div>
    );
  }

  if (mode === 'drawer') {
    return (
      <>
        <div
          className={cn('fixed inset-0 z-30 bg-black/30 transition-opacity duration-200', open ? 'opacity-100' : 'opacity-0 pointer-events-none')}
          onClick={onClose}
          aria-hidden
        />
        <aside
          ref={panelRef}
          className={cn(
            'fixed top-0 right-0 bottom-0 z-40 glass-strong border-l flex transition-transform duration-250 ease-out',
            open ? 'translate-x-0' : 'translate-x-full pointer-events-none',
          )}
          style={{ width: `min(${width}px, 92vw)` }}
          aria-hidden={!open}
        >
          <ResizeHandle onPointerDown={onResizeStart} />
          <div className="flex-1 min-w-0 h-full">{body}</div>
        </aside>
      </>
    );
  }

  return (
    <aside
      ref={panelRef}
      className={cn('relative shrink-0 h-full border-l border-hairline bg-ground flex transition-[width] duration-200 ease-out overflow-hidden', !open && 'w-0 border-l-0')}
      style={{ width: open ? width : 0 }}
      aria-hidden={!open}
    >
      <ResizeHandle onPointerDown={onResizeStart} />
      <div className="flex-1 min-w-0 h-full" style={{ minWidth: MIN_W }}>
        {body}
      </div>
    </aside>
  );
}

function ResizeHandle({ onPointerDown }: { onPointerDown: (e: React.PointerEvent) => void }) {
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize sidebar"
      onPointerDown={onPointerDown}
      className="absolute left-0 top-0 bottom-0 w-1.5 -ml-0.5 cursor-col-resize z-10 hover:bg-accent/40 active:bg-accent/60 transition-colors touch-none"
    />
  );
}
