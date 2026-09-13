import { ChevronRight, Clapperboard, Folder, FolderUp, Globe, ListPlus, ListVideo, LoaderCircle, Play, Search, Server, Trash2 } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { formatBytes, formatTime } from '@/lib/format';
import type { FsEntry, FsList, MediaMeta } from '@/proto/messages';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Input } from '@/ui/Field';
import { Tooltip } from '@/ui/Tooltip';

// `vsm` is the pre-encoded format the server-side projector plays; without it
// pushed files are listed by the projector and then hidden by this filter.
const MEDIA_RE = /\.(vsm|mkv|mp4|m4v|mov|avi|webm|ts|m2ts|wmv|flv|mpg|mpeg|ogv|mp3|flac|m4a|ogg|opus|wav|aac)$/i;

/**
 * The library, and the projector's filesystem behind it.
 *
 * Hosted media is the primary list: titles pushed to the server, which anyone
 * can see and the host can play, queue or delete. The filesystem browser below
 * it only appears when a desktop projector is connected, since that is the
 * only thing it can drive.
 */
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
  const [library, setLibrary] = useState<Array<MediaMeta & { url: string }>>([]);
  const [freeBytes, setFreeBytes] = useState(0);

  const refreshLibrary = async () => {
    const session = useSession.getState().token?.session;
    if (!session) return;
    try {
      const r = await api.listMedia(session);
      setLibrary(r.items);
      setFreeBytes(r.freeBytes);
    } catch {
      // A deployment without a library is a normal state, not an error to
      // shout about; the section simply does not appear.
      setLibrary([]);
    }
  };

  useEffect(() => {
    if (active) void refreshLibrary();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  const playHosted = async (m: MediaMeta, mode: 'replace' | 'append') => {
    const session = useSession.getState().token?.session;
    if (!session || !roomId) return;
    try {
      await api.playback(roomId, session, { action: mode === 'append' ? 'enqueue' : 'load', mediaId: m.id });
      useSession.getState().toast(mode === 'append' ? `Queued ${m.title}` : `Playing ${m.title}`, 'info', 2500);
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    }
  };

  const deleteHosted = async (m: MediaMeta) => {
    if (!window.confirm(`Delete ${m.title} from the server? This cannot be undone.`)) return;
    const session = useSession.getState().token?.session;
    if (!session) return;
    try {
      await api.deleteMedia(session, m.id);
      useSession.getState().toast(`Deleted ${m.title}`, 'info', 2500);
      void refreshLibrary();
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    }
  };

  const isHost = role === 'host';
  const roomId = useSession((s) => s.roomId);

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
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted" />
          <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter this folder" className="pl-8 h-9" aria-label="Filter files" />
        </div>
      </div>

      {library.length > 0 && (
        <div className="border-b border-hairline">
          <div className="flex items-center gap-2 px-2 py-1.5 text-[11px] uppercase tracking-wide text-muted">
            <Server className="size-3.5 shrink-0" />
            <span className="flex-1">On the server</span>
            <span className="font-mono normal-case tracking-normal">{formatBytes(freeBytes)} free</span>
          </div>
          <div className="pb-1">
            {library.map((m) => (
              <div key={m.id} className="group flex items-center gap-2 px-2 h-9 rounded-lg hover:bg-hover">
                <button
                  onClick={() => void playHosted(m, 'replace')}
                  disabled={!isHost}
                  className="flex-1 min-w-0 flex items-center gap-2 text-left disabled:cursor-default"
                  title={m.title}
                >
                  <Clapperboard className="size-4 shrink-0 text-info" />
                  <span className="text-[13px] truncate">{m.title}</span>
                  <span className="ml-auto text-[11px] text-muted font-mono shrink-0">
                    {formatTime(m.durationMs / 1000)}
                  </span>
                </button>
                {isHost && (
                  <span className="inline-flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
                    <Tooltip label="Play now">
                      <button onClick={() => void playHosted(m, 'replace')} aria-label="Play now" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
                        <Play className="size-3.5" />
                      </button>
                    </Tooltip>
                    <Tooltip label="Append to playlist">
                      <button onClick={() => void playHosted(m, 'append')} aria-label="Append to playlist" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
                        <ListPlus className="size-3.5" />
                      </button>
                    </Tooltip>
                    <Tooltip label="Delete from the server">
                      <button onClick={() => void deleteHosted(m)} aria-label="Delete from the server" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-danger hover:bg-active">
                        <Trash2 className="size-3.5" />
                      </button>
                    </Tooltip>
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

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
        {!online && library.length === 0 && (
          <div className="p-4 text-sm text-muted text-center space-y-2">
            <p>Nothing to play yet.</p>
            <p className="text-[12px]">
              Push something to the server with <code className="font-mono">make push</code>, or start the
              desktop projector on the machine holding the files.
            </p>
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
          <EntryRow
            key={e.path}
            e={e}
            onOpen={() => (e.dir ? void browse(e.path) : void load(e.path, 'replace'))}
            onAppend={() => void load(e.path, 'append')}
          />
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

function EntryRow({
  e,
  onOpen,
  onAppend,
  onDelete,
}: {
  e: FsEntry;
  onOpen: () => void;
  onAppend: () => void;
  // Only offered for server-hosted media: deleting off someone's own desktop
  // from a web page is not a thing this should do.
  onDelete?: () => void;
}) {
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
          {onDelete && (
            <Tooltip label="Delete from the server">
              <button onClick={onDelete} aria-label="Delete from the server" className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-danger hover:bg-active">
                <Trash2 className="size-3.5" />
              </button>
            </Tooltip>
          )}
        </span>
      )}
    </div>
  );
}
