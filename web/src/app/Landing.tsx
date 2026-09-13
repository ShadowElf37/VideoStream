import { ArrowRight, Check, Copy, Crown, Link as LinkIcon, MonitorPlay, Users } from 'lucide-react';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { api } from '@/lib/api';
import { parseRoomLink } from '@/lib/links';
import type { CreateRoomResponse } from '@/proto/messages';
import { Button } from '@/ui/Button';
import { Field, Input } from '@/ui/Field';
import { Logo } from '@/ui/Logo';

export function Landing() {
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreateRoomResponse | null>(null);
  const [paste, setPaste] = useState('');
  const pasted = parseRoomLink(paste);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    setError(null);
    try {
      setCreated(await api.createRoom({ name: name.trim() || undefined, password: password || undefined }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  const enterAsHost = () => {
    if (!created) return;
    const parsed = parseRoomLink(created.hostLink);
    if (parsed) navigate(parsed.path);
    else window.location.href = created.hostLink;
  };

  return (
    <div className="min-h-full flex flex-col bg-[radial-gradient(ellipse_at_top,rgba(245,185,66,0.10),transparent_55%)]">
      <header className="flex items-center gap-2.5 px-6 py-5">
        <Logo size={30} />
        <span className="font-semibold tracking-tight text-[15px]">VideoStream</span>
      </header>

      <main className="flex-1 flex items-center justify-center px-6 pb-12">
        <div className="w-full max-w-4xl grid gap-8 md:grid-cols-[1.1fr_1fr] items-center">
          <div>
            <h1 className="text-4xl md:text-5xl font-semibold tracking-tight leading-[1.05]">
              Movie night,
              <br />
              <span className="text-accent">without the compromise.</span>
            </h1>
            <p className="mt-4 text-muted text-[15px] max-w-md">
              Stream a file straight from your machine into a private room. Friends join with a link and get the movie, voice chat, and their own mic / deafen / movie-audio controls.
            </p>
            <ul className="mt-6 space-y-2 text-sm text-muted">
              <li className="flex items-center gap-2">
                <MonitorPlay className="size-4 text-accent" /> mpv does the playing: any file, embedded subtitles, chapters.
              </li>
              <li className="flex items-center gap-2">
                <Users className="size-4 text-accent" /> Voices and movie audio are separate: deafen the chatter, keep the film.
              </li>
              <li className="flex items-center gap-2">
                <Crown className="size-4 text-accent" /> The host controls playback from the browser like a remote.
              </li>
            </ul>
          </div>

          <div className="anim-pop glass-strong rounded-2xl p-6 space-y-6">
            {!created ? (
              <form onSubmit={create} className="space-y-4">
                <h2 className="text-lg font-semibold tracking-tight">Create a room</h2>
                <Field label="Room name" hint="Optional">
                  <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Friday night" maxLength={60} autoFocus />
                </Field>
                <Field label="Password" hint="Optional. The links already carry a secret key.">
                  <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••" autoComplete="new-password" />
                </Field>
                {error && <p className="text-danger text-[13px]">{error}</p>}
                <Button type="submit" variant="primary" size="lg" className="w-full" loading={creating}>
                  Create a room <ArrowRight className="size-4" />
                </Button>
              </form>
            ) : (
              <div className="space-y-4">
                <div>
                  <h2 className="text-lg font-semibold tracking-tight">{created.name || 'Your room'} is ready</h2>
                  <p className="text-muted text-[13px] mt-0.5">Three links, three jobs. Keep the host and projector ones to yourself.</p>
                </div>
                <LinkRow label="Invite" hint="Send this to friends" value={created.inviteLink} />
                <LinkRow label="Host" hint="Your remote control" value={created.hostLink} />
                <LinkRow label="Projector" hint="For the projector CLI" value={created.projectorLink} mono />
                <pre className="text-[11.5px] font-mono text-muted bg-panel border border-hairline rounded-xl px-3 py-2 overflow-x-auto">
                  projector --room &quot;{created.projectorLink}&quot; ~/Movies/film.mkv
                </pre>
                <div className="flex gap-2">
                  <Button variant="ghost" onClick={() => setCreated(null)}>
                    New room
                  </Button>
                  <Button variant="primary" size="lg" className="flex-1" onClick={enterAsHost}>
                    <Crown className="size-4" /> Enter as host
                  </Button>
                </div>
              </div>
            )}

            <div className="border-t border-hairline pt-5">
              <Field label="Have a link? Paste it">
                <form
                  className="flex gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (pasted) navigate(pasted.path);
                  }}
                >
                  <div className="relative flex-1">
                    <LinkIcon className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted" />
                    <Input value={paste} onChange={(e) => setPaste(e.target.value)} placeholder="https://…/r/abc?k=…" className="pl-9" />
                  </div>
                  <Button type="submit" disabled={!pasted}>
                    Go
                  </Button>
                </form>
              </Field>
              {paste && !pasted && <p className="text-xs text-danger mt-1.5">That doesn't look like a room link.</p>}
            </div>
          </div>
        </div>
      </main>

      <footer className="px-6 py-4 text-xs text-muted">Self-hosted. Your files never leave your relay.</footer>
    </div>
  );
}

function LinkRow({ label, hint, value, mono }: { label: string; hint: string; value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      window.prompt('Copy this link', value);
    }
  };
  return (
    <div className="flex items-center gap-2">
      <div className="w-[92px] shrink-0">
        <div className="text-[13px] font-medium">{label}</div>
        <div className="text-[11px] text-muted leading-tight">{hint}</div>
      </div>
      <input readOnly value={value} onFocus={(e) => e.currentTarget.select()} className={`flex-1 min-w-0 h-9 px-2.5 rounded-lg bg-panel border border-hairline text-[12px] ${mono ? 'font-mono' : ''}`} aria-label={`${label} link`} />
      <Button size="sm" onClick={copy} aria-label={`Copy ${label} link`} className="w-[88px]">
        {copied ? (
          <>
            <Check className="size-3.5 text-ok" /> Copied
          </>
        ) : (
          <>
            <Copy className="size-3.5" /> Copy
          </>
        )}
      </Button>
    </div>
  );
}
