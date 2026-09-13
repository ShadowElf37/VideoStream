import { ChevronRight, Clapperboard, Folder, FolderUp, Globe, ListPlus, ListVideo, LoaderCircle, Play, Search, Server } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { formatBytes } from '@/lib/format';
import type { FsEntry, FsList } from '@/proto/messages';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Input } from '@/ui/Field';
import { Tooltip } from '@/ui/Tooltip';

const MEDIA_RE = /\.(mkv|mp4|m4v|mov|avi|webm|ts|m2ts|wmv|flv|mpg|mpeg|ogv|mp3|flac|m4a|ogg|opus|wav|aac)$/i;

export function QueueTab({ active }: { active: boolean }) {
  const role = useSession((s) => s.role);
  const mpv = useMpv();
  const online = useMpvStore((s) => s.projectorOnline);
  const now = useMpvStore((s) => s.state);
  const [listing, setListing] = useState<FsList | null>(null);
  const [dir, setDir] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('');
  const [url, setUrl] = useState('');
  const [house, setHouse] = useState<{ active: boolean; available: boolean; elsewhere: boolean } | null>(null);
  const [houseBusy, setHouseBusy] = useState(false);

  const isHost = role === 'host';
  const roomId = useSession((s) => s.roomId);

  // Whether this deployment even has a server-side projector is a property of
  // the server, not of the client, so ask rather than assume.
  useEffect(() => {
    if (!active || !isHost || !roomId) return;
    void api
      .getHouseProjector(roomId)
      .then(setHouse)
      .catch(() => setHouse(null));
  }, [active, isHost, roomId, online]);

  const toggleHouse = async (on: boolean) => {
    const session = useSession.getState().token?.session;
    if (!session || !roomId) return;
    setHouseBusy(true);
    try {
      await api.setHouseProjector(roomId, session, on);
      setHouse(await api.getHouseProjector(roomId));
      useSession
        .getState()
        .toast(on ? 'Server projector joining…' : 'Server projector released', 'info', 3000);
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      setHouseBusy(false);
    }
  };

  const browse = async (path: string) => {
    setLoading(true);
    setError(null);
    try {
      const l = await mpv.fsList(path);
      setListing(l);
      setDir(l.dir);
      setFilter('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (active && isHost && online && !listing && !loading) void browse('');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, isHost, online]);

  const load = async (path: string, mode: 'replace' | 'append') => {
    const r = await mpv.send(['vs/load', path, mode]);
    const name = path.split(/[\\/]/).pop() || path;
    if (r.ok) useSession.getState().toast(mode === 'replace' ? `Loading ${name}` : `Queued ${name}`, 'info', 2500);
    else useSession.getState().toast(`Load failed: ${r.error ?? 'unknown error'}`, 'error');
  };

  const crumbs = useMemo(() => {
    if (!listing || !listing.dir) return [];
    const sep = listing.dir.includes('\\') && !listing.dir.includes('/') ? '\\' : '/';
    const root = listing.roots.find((r) => listing.dir.startsWith(r));
    const rest = root ? listing.dir.slice(root.length) : listing.dir;
    const parts = rest.split(sep).filter(Boolean);
    const out: Array<{ label: string; path: string }> = [];
    let acc = root ?? '';
    if (root) out.push({ label: root.split(/[\\/]/).filter(Boolean).pop() ?? root, path: root });
    for (const p of parts) {
      acc = acc.endsWith(sep) || acc === '' ? acc + p : acc + sep + p;
      out.push({ label: p, path: acc });
    }
    return out;
  }, [listing]);

  const entries = useMemo(() => {
    if (!listing) return [];
    const q = filter.trim().toLowerCase();
    return [...listing.entries]
      .filter((e) => (q ? e.name.toLowerCase().includes(q) : true))
      .sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : a.name.localeCompare(b.name, undefined, { numeric: true })));
  }, [listing, filter]);

  if (!isHost) {
    return (
      <div className="p-6 text-center text-muted text-sm">
        <ListVideo className="size-8 mx-auto mb-3 opacity-60" />
        Only the host picks what plays. Ask them nicely in chat.
        {now && !now.idle && <div className="mt-4 text-text text-[13px]">Now playing: {now.mediaTitle || now.path}</div>}
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="p-2 border-b border-hairline space-y-2">
        <form
          className="flex gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            const u = url.trim();
            if (!u) return;
            void load(u, 'replace');
            setUrl('');
          }}
        >
          <div className="relative flex-1">
            <Globe className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted" />
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="Open URL (YouTube etc. via yt-dlp)" className="pl-8 h-9" aria-label="Open URL" />
          </div>
          <Button type="submit" size="sm" variant="primary" disabled={!url.trim() || !online} className="h-9">
            Open
          </Button>
        </form>
        {house?.active && (
          <div className="flex items-center gap-2 text-[12px] text-muted">
            <Server className="size-3.5 shrink-0 text-accent" />
            <span className="flex-1">Streaming from the server</span>
            <button className="underline hover:text-text" disabled={houseBusy} onClick={() => void toggleHouse(false)}>
              release
            </button>
          </div>
        )}
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted" />
          <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter this folder" className="pl-8 h-9" aria-label="Filter files" />
        </div>
      </div>

      <div className="flex items-center gap-0.5 px-2 py-1.5 text-[12px] text-muted overflow-x-auto whitespace-nowrap border-b border-hairline">
        <button className={cn('px-1.5 h-6 rounded-md hover:bg-hover hover:text-text', !dir && 'text-text')} onClick={() => void browse('')}>
          Roots
        </button>
        {crumbs.map((c, i) => (
          <span key={c.path} className="inline-flex items-center">
            <ChevronRight className="size-3.5 opacity-60" />
            <button className={cn('px-1.5 h-6 rounded-md hover:bg-hover hover:text-text', i === crumbs.length - 1 && 'text-text')} onClick={() => void browse(c.path)}>
              {c.label}
            </button>
          </span>
        ))}
        <span className="flex-1" />
        {loading && <LoaderCircle className="size-4 anim-spin" />}
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto p-1">
        {!online && (
          <div className="p-4 text-sm text-muted text-center space-y-3">
            <p>Projector is offline.</p>
            {house?.available && !house.elsewhere && (
              <Button size="sm" variant="primary" disabled={houseBusy} onClick={() => void toggleHouse(true)}>
                <Server className="size-4" /> Use the server projector
              </Button>
            )}
            {house?.elsewhere && <p className="text-[12px]">The server projector is busy in another room.</p>}
            {!house?.available && <p className="text-[12px]">Start the projector on the machine with the files.</p>}
          </div>
        )}
        {error && (
          <div className="m-2 p-3 rounded-xl bg-danger/10 border border-danger/30 text-danger text-[13px]">
            {error}
            <button className="ml-2 underline" onClick={() => void browse(dir)}>
              retry
            </button>
          </div>
        )}
        {online && listing && crumbs.length > 0 && (
          <button className="w-full flex items-center gap-2 px-2 h-9 rounded-lg text-[13px] text-muted hover:bg-hover" onClick={() => void browse(crumbs.length > 1 ? crumbs[crumbs.length - 2]!.path : '')}>
            <FolderUp className="size-4" /> ..
          </button>
        )}
        {online && !listing?.dir && (listing?.roots.length ?? 0) > 0 && !filter && (
          <div className="px-2 pt-1 pb-1 text-[11px] uppercase tracking-wider text-muted">Media roots</div>
        )}
        {entries.map((e) => (
          <EntryRow key={e.path} e={e} onOpen={() => (e.dir ? void browse(e.path) : void load(e.path, 'replace'))} onAppend={() => void load(e.path, 'append')} />
        ))}
        {online && listing && entries.length === 0 && !loading && <div className="p-4 text-sm text-muted text-center">Nothing here{filter ? ' matches' : ''}.</div>}
      </div>

      <div className="border-t border-hairline p-3 text-[12px] text-muted">
        <div className="flex items-center gap-2 text-text text-[13px] font-medium mb-1">
          <ListVideo className="size-4" /> Playlist
        </div>
        {now && !now.idle ? (
          <div className="truncate">
            <Clapperboard className="inline size-3.5 mr-1 text-accent" />
            {now.mediaTitle || now.path}
          </div>
        ) : (
          <div>Nothing loaded.</div>
        )}
        <div className="mt-1 opacity-70">Full playlist view arrives with projector playlist support.</div>
      </div>
    </div>
  );
}

function EntryRow({ e, onOpen, onAppend }: { e: FsEntry; onOpen: () => void; onAppend: () => void }) {
  const media = !e.dir && MEDIA_RE.test(e.name);
  return (
    <div className="group flex items-center gap-2 px-2 h-9 rounded-lg hover:bg-hover">
      <button onClick={onOpen} className="flex-1 min-w-0 flex items-center gap-2 text-left" title={e.path}>
        {e.dir ? <Folder className="size-4 text-accent shrink-0" /> : <Clapperboard className={cn('size-4 shrink-0', media ? 'text-info' : 'text-muted')} />}
        <span className={cn('text-[13px] truncate', !e.dir && !media && 'text-muted')}>{e.name}</span>
        {!e.dir && e.size !== undefined && <span className="ml-auto text-[11px] text-muted font-mono shrink-0">{formatBytes(e.size)}</span>}
      </button>
      {!e.dir && (
        <span className="inline-flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
          <Tooltip label="Play now">
            <button onClick={onOpen} aria-label="Play now" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
              <Play className="size-3.5" />
            </button>
          </Tooltip>
          <Tooltip label="Append to playlist">
            <button onClick={onAppend} aria-label="Append to playlist" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
              <ListPlus className="size-3.5" />
            </button>
          </Tooltip>
        </span>
      )}
    </div>
  );
}
