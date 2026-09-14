import {
  ChevronDown,
  ChevronRight,
  Clapperboard,
  Folder,
  FolderUp,
  Globe,
  ListPlus,
  ListVideo,
  LoaderCircle,
  Play,
  Radio,
  Search,
  Trash2,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useMpv, useMpvStore } from '@/host/useMpv';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { formatBytes, formatTime } from '@/lib/format';
import { usePlaybackStore } from '@/movie/store';
import type { FsEntry, FsList, MediaMeta } from '@/proto/messages';
import { usePrefs } from '@/state/prefs';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Input } from '@/ui/Field';
import { Tooltip } from '@/ui/Tooltip';

const MEDIA_RE = /\.(mkv|mp4|m4v|mov|avi|webm|ts|m2ts|wmv|flv|mpg|mpeg|ogv|mp3|flac|m4a|ogg|opus|wav|aac)$/i;

type Title = MediaMeta & { url: string };

/**
 * The Library: what is on the server, Plex-style. This is the app's primary
 * surface — titles are pushed ahead of time, anyone can browse them, and the
 * host plays, queues or deletes.
 *
 * The desktop projector is a separate mode behind a button at the bottom,
 * because it is a different way of watching (a live stream from someone's
 * machine, with everything that implies about buffering and delay) and
 * mixing its file browser into the library made both harder to read.
 */
export function LibraryTab({ active }: { active: boolean }) {
  const role = useSession((s) => s.role);
  const isHost = role === 'host';
  const playback = usePlaybackStore((s) => s.state);
  const [library, setLibrary] = useState<Title[]>([]);
  const [freeBytes, setFreeBytes] = useState(0);
  const [hasLibrary, setHasLibrary] = useState(true);
  const [filter, setFilter] = useState('');

  const refresh = async () => {
    const session = useSession.getState().token?.session;
    if (!session) return;
    try {
      const r = await api.listMedia(session);
      setLibrary(r.items);
      setFreeBytes(r.freeBytes);
      setHasLibrary(true);
    } catch {
      // A deployment without a library is a normal state, not an error to
      // shout about; the section simply says so.
      setLibrary([]);
      setHasLibrary(false);
    }
  };

  useEffect(() => {
    if (active) void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  const play = async (m: MediaMeta, mode: 'replace' | 'append') => {
    const session = useSession.getState().token?.session;
    if (!session) return;
    try {
      await api.playback(session, { action: mode === 'append' ? 'enqueue' : 'load', mediaId: m.id });
      useSession.getState().toast(mode === 'append' ? `Queued ${m.title}` : `Playing ${m.title}`, 'info', 2500);
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    }
  };

  const remove = async (m: MediaMeta) => {
    if (!window.confirm(`Delete ${m.title} from the server? This cannot be undone.`)) return;
    const session = useSession.getState().token?.session;
    if (!session) return;
    try {
      await api.deleteMedia(session, m.id);
      useSession.getState().toast(`Deleted ${m.title}`, 'info', 2500);
      void refresh();
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    }
  };

  const byId = useMemo(() => new Map(library.map((m) => [m.id, m])), [library]);
  const nowPlaying = playback && !playback.idle ? playback : null;
  const upNext = (nowPlaying?.queue ?? []).map((id) => byId.get(id)?.title ?? id);
  const titles = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return [...library]
      .filter((m) => (q ? m.title.toLowerCase().includes(q) : true))
      .sort((a, b) => a.title.localeCompare(b.title, undefined, { numeric: true }));
  }, [library, filter]);

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="p-2 border-b border-hairline">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted" />
          <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Search the library" className="pl-8 h-9" aria-label="Search the library" />
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        {nowPlaying && (
          <section className="px-2 pt-2 pb-1 border-b border-hairline">
            <div className="px-2 text-[11px] uppercase tracking-wider text-muted">Now playing</div>
            <div className="px-2 py-1.5 flex items-center gap-2 text-[13px]">
              <Clapperboard className="size-4 text-accent shrink-0" />
              <span className="truncate">{nowPlaying.title}</span>
              <span className="ml-auto text-[11px] text-muted font-mono shrink-0">{formatTime(nowPlaying.durationMs / 1000)}</span>
            </div>
            {upNext.length > 0 && (
              <>
                <div className="px-2 pt-1 text-[11px] uppercase tracking-wider text-muted">Up next</div>
                <ol className="px-2 pb-1">
                  {upNext.map((t, i) => (
                    <li key={`${t}-${i}`} className="flex items-center gap-2 h-7 text-[12.5px] text-muted">
                      <span className="w-4 text-right font-mono text-[11px]">{i + 1}</span>
                      <span className="truncate">{t}</span>
                    </li>
                  ))}
                </ol>
              </>
            )}
          </section>
        )}

        <section className="p-2">
          <div className="flex items-center gap-2 px-2 py-1 text-[11px] uppercase tracking-wider text-muted">
            <span className="flex-1">{library.length === 1 ? '1 title' : `${library.length} titles`} on the server</span>
            {hasLibrary && <span className="font-mono normal-case tracking-normal">{formatBytes(freeBytes)} free</span>}
          </div>
          {!hasLibrary && <p className="px-2 py-3 text-[13px] text-muted">This server has no media library.</p>}
          {hasLibrary && library.length === 0 && (
            <div className="px-2 py-3 text-[13px] text-muted space-y-1.5">
              <p>Nothing on the server yet.</p>
              {isHost && (
                <p className="text-[12px]">
                  From the machine holding the files:{' '}
                  <code className="font-mono text-text/80">make push FILE=~/Videos/ep01.mkv HOST=ubuntu@your-server</code>
                </p>
              )}
            </div>
          )}
          {hasLibrary && library.length > 0 && titles.length === 0 && <p className="px-2 py-3 text-[13px] text-muted">Nothing matches.</p>}
          <ul className="space-y-0.5">
            {titles.map((m) => (
              <TitleRow key={m.id} m={m} isHost={isHost} playing={nowPlaying?.mediaId === m.id} onPlay={() => void play(m, 'replace')} onQueue={() => void play(m, 'append')} onDelete={() => void remove(m)} />
            ))}
          </ul>
        </section>

        <ProjectorSection isHost={isHost} hostedActive={!!nowPlaying} />
      </div>
    </div>
  );
}

function TitleRow({
  m,
  isHost,
  playing,
  onPlay,
  onQueue,
  onDelete,
}: {
  m: Title;
  isHost: boolean;
  playing: boolean;
  onPlay: () => void;
  onQueue: () => void;
  onDelete: () => void;
}) {
  const meta = [
    formatTime(m.durationMs / 1000),
    m.height ? `${m.height}p` : null,
    m.videoCodec?.toUpperCase(),
    m.chapters?.length ? `${m.chapters.length} chapters` : null,
  ]
    .filter(Boolean)
    .join(' · ');
  return (
    <li className={cn('group rounded-xl px-2 py-1.5 hover:bg-hover/60', playing && 'bg-accent/10')}>
      <div className="flex items-center gap-3">
        <button onClick={isHost ? onPlay : undefined} disabled={!isHost} className="flex-1 min-w-0 flex items-center gap-3 text-left disabled:cursor-default" title={m.title}>
          <span className={cn('size-10 shrink-0 rounded-lg inline-flex items-center justify-center', playing ? 'bg-accent/20 text-accent' : 'bg-panel text-info')}>
            <Clapperboard className="size-5" />
          </span>
          <span className="min-w-0">
            <span className="block text-[13px] font-medium truncate">{m.title}</span>
            <span className="block text-[11px] text-muted truncate">{meta}</span>
          </span>
        </button>
        {isHost && (
          <span className="inline-flex gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
            <Tooltip label="Play now">
              <button onClick={onPlay} aria-label={`Play ${m.title}`} className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
                <Play className="size-3.5" />
              </button>
            </Tooltip>
            <Tooltip label="Add to up next">
              <button onClick={onQueue} aria-label={`Queue ${m.title}`} className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-text hover:bg-active">
                <ListPlus className="size-3.5" />
              </button>
            </Tooltip>
            <Tooltip label="Delete from the server">
              <button onClick={onDelete} aria-label={`Delete ${m.title}`} className="size-7 rounded-md inline-flex items-center justify-center text-muted hover:text-danger hover:bg-active">
                <Trash2 className="size-3.5" />
              </button>
            </Tooltip>
          </span>
        )}
      </div>
    </li>
  );
}

/**
 * The desktop projector, as a mode you switch into. Collapsed it is one
 * status line; opened (host only) it is the file browser and URL box for the
 * mpv on someone's machine, and the stage expects the live track.
 */
function ProjectorSection({ isHost, hostedActive }: { isHost: boolean; hostedActive: boolean }) {
  const online = useMpvStore((s) => s.projectorOnline);
  const now = useMpvStore((s) => s.state);
  const projectorMode = usePrefs((s) => s.projectorMode);
  const setPref = usePrefs((s) => s.set);
  const status = !online ? 'desktop mpv · offline' : now && !now.idle ? `live · ${now.mediaTitle || now.path}` : 'desktop mpv · connected, idle';

  const toggle = async () => {
    if (!projectorMode && hostedActive) {
      if (!window.confirm('Switch to projector mode? The film playing from the server will stop for everyone.')) return;
      const session = useSession.getState().token?.session;
      if (session) await api.playback(session, { action: 'stop' }).catch(() => undefined);
    }
    setPref('projectorMode', !projectorMode);
  };

  return (
    <section className={cn('m-2 rounded-xl border border-hairline', projectorMode ? 'bg-panel' : 'bg-panel/50')}>
      <div className="flex items-center gap-3 px-3 py-2.5">
        <span className={cn('size-8 rounded-lg inline-flex items-center justify-center shrink-0', online ? 'bg-accent/15 text-accent' : 'bg-hover text-muted')}>
          <Radio className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium">Projector</div>
          <div className="text-[11px] text-muted truncate">{status}</div>
        </div>
        {isHost ? (
          <Button size="sm" variant={projectorMode ? 'primary' : 'subtle'} onClick={() => void toggle()} aria-pressed={projectorMode}>
            {projectorMode ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            {projectorMode ? 'Projector mode' : 'Projector mode'}
          </Button>
        ) : (
          <span className={cn('size-2 rounded-full', online ? (now && !now.idle ? 'bg-ok' : 'bg-accent') : 'bg-muted/40')} />
        )}
      </div>
      {isHost && projectorMode && <ProjectorPanel online={online} />}
    </section>
  );
}

function ProjectorPanel({ online }: { online: boolean }) {
  const mpv = useMpv();
  const now = useMpvStore((s) => s.state);
  const [listing, setListing] = useState<FsList | null>(null);
  const [dir, setDir] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('');
  const [url, setUrl] = useState('');
  const origin = typeof window !== 'undefined' ? window.location.origin : 'https://…';

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
    if (online && !listing && !loading) void browse('');
    if (!online) setListing(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online]);

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

  if (!online) {
    return (
      <div className="border-t border-hairline px-3 py-3 text-[12px] text-muted space-y-2">
        <p>Start the projector on the machine holding the files. It joins with the room password:</p>
        <pre className="text-[11.5px] font-mono bg-ground border border-hairline rounded-lg px-2.5 py-2 whitespace-pre-wrap break-all text-text/80">
          VS_PASSWORD=… projector --room {origin} --media-root ~/Videos ~/Videos/film.mkv
        </pre>
        <p>Live from a computer: viewers trail the projector by their buffer, and pause and seek take a moment to land.</p>
      </div>
    );
  }

  return (
    <div className="border-t border-hairline">
      <div className="p-2 space-y-2">
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
          <Button type="submit" size="sm" variant="primary" disabled={!url.trim()} className="h-9">
            Open
          </Button>
        </form>
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted" />
          <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter this folder" className="pl-8 h-9" aria-label="Filter files" />
        </div>
      </div>

      <div className="flex items-center gap-0.5 px-2 py-1.5 text-[12px] text-muted overflow-x-auto whitespace-nowrap border-y border-hairline">
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

      <div className="max-h-[40vh] overflow-y-auto p-1">
        {error && (
          <div className="m-2 p-3 rounded-xl bg-danger/10 border border-danger/30 text-danger text-[13px]">
            {error}
            <button className="ml-2 underline" onClick={() => void browse(dir)}>
              retry
            </button>
          </div>
        )}
        {listing && crumbs.length > 0 && (
          <button className="w-full flex items-center gap-2 px-2 h-9 rounded-lg text-[13px] text-muted hover:bg-hover" onClick={() => void browse(crumbs.length > 1 ? crumbs[crumbs.length - 2]!.path : '')}>
            <FolderUp className="size-4" /> ..
          </button>
        )}
        {!listing?.dir && (listing?.roots.length ?? 0) > 0 && !filter && <div className="px-2 pt-1 pb-1 text-[11px] uppercase tracking-wider text-muted">Media roots</div>}
        {entries.map((e) => (
          <EntryRow key={e.path} e={e} onOpen={() => (e.dir ? void browse(e.path) : void load(e.path, 'replace'))} onAppend={() => void load(e.path, 'append')} />
        ))}
        {listing && entries.length === 0 && !loading && <div className="p-4 text-sm text-muted text-center">Nothing here{filter ? ' matches' : ''}.</div>}
      </div>

      <div className="border-t border-hairline px-3 py-2 text-[12px] text-muted flex items-center gap-2">
        <ListVideo className="size-4 shrink-0" />
        <span className="truncate">{now && !now.idle ? now.mediaTitle || now.path : 'Nothing loaded in mpv.'}</span>
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
