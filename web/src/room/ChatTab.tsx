import { useParticipants, useRoomContext } from '@livekit/components-react';
import { ArrowDown, Send, Sticker } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '@/lib/api';
import { cn } from '@/lib/cn';
import { publish } from '@/lib/data';
import { absoluteTime, relativeTime } from '@/lib/format';
import { isImageUrl, tokenize } from '@/lib/links';
import { Topics } from '@/proto/messages';
import { useChat, type LocalMessage } from '@/state/chat';
import { useSession } from '@/state/session';
import { Popover } from '@/ui/Popover';
import { displayName, isProjector } from './identity';

const EMOJI = [
  '😀', '😂', '🥹', '😍', '🤔', '😮', '😴', '🙄', '😭', '🤣', '😅', '🥳',
  '👍', '👎', '👏', '🙏', '🔥', '💯', '❤️', '💀', '👀', '🍿', '🎬', '🎉',
  '😱', '🤯', '🫠', '🤝', '✨', '⭐', '☕', '🍕', '🐐', '🚀', '🤫', '😤',
];

const GROUP_WINDOW_MS = 2 * 60_000;

export function ChatTab({ active }: { active: boolean }) {
  const room = useRoomContext();
  const token = useSession((s) => s.token);
  const selfName = useSession((s) => s.name);
  const messages = useChat((s) => s.messages);
  const typing = useChat((s) => s.typing);
  const unread = useChat((s) => s.unread);
  const clearUnread = useChat((s) => s.clearUnread);
  const participants = useParticipants();
  const names = useMemo(() => participants.filter((p) => !isProjector(p)).map(displayName), [participants]);
  const nameOf = useCallback(
    (identity: string) => {
      const p = participants.find((x) => x.identity === identity);
      return p ? displayName(p) : identity;
    },
    [participants],
  );

  // History (once per session token).
  const loaded = useRef<string | null>(null);
  useEffect(() => {
    if (!token || loaded.current === token.session) return;
    loaded.current = token.session;
    api
      .getChat(token.session, { limit: 100 })
      .then((r) => useChat.getState().setHistory(r.messages))
      .catch((e) => console.warn('chat history failed', e));
  }, [token]);

  // Scrolling: stick to bottom unless the user scrolled up.
  const listRef = useRef<HTMLDivElement>(null);
  const [atBottom, setAtBottom] = useState(true);
  const onScroll = () => {
    const el = listRef.current;
    if (!el) return;
    setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
  };
  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    if (atBottom) el.scrollTop = el.scrollHeight;
  }, [messages, atBottom]);
  useEffect(() => {
    if (active && atBottom) clearUnread();
  }, [active, atBottom, messages, clearUnread]);
  const jump = () => {
    const el = listRef.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
    setAtBottom(true);
  };

  // Composer.
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const typingSentAt = useRef(0);
  const typingIdle = useRef<ReturnType<typeof setTimeout> | null>(null);

  const setTypingRemote = useCallback(
    (v: boolean) => {
      void publish(room, Topics.typing, { typing: v }, { reliable: false });
    },
    [room],
  );
  const onTyping = () => {
    const now = Date.now();
    if (now - typingSentAt.current > 2000) {
      typingSentAt.current = now;
      setTypingRemote(true);
    }
    if (typingIdle.current) clearTimeout(typingIdle.current);
    typingIdle.current = setTimeout(() => {
      typingSentAt.current = 0;
      setTypingRemote(false);
    }, 3000);
  };

  const send = async () => {
    const body = text.trim();
    if (!body || !token || sending) return;
    setText('');
    if (typingIdle.current) clearTimeout(typingIdle.current);
    typingSentAt.current = 0;
    setTypingRemote(false);
    const tempId = `pending-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const pending: LocalMessage = {
      id: tempId,
      from: { identity: token.identity, name: selfName || nameOf(token.identity), color: token.color },
      text: body,
      ts: Date.now(),
      kind: 'user',
      pending: true,
    };
    useChat.getState().add(pending, { countUnread: false });
    setAtBottom(true);
    setSending(true);
    try {
      const real = await api.postChat(token.session, body);
      useChat.getState().resolvePending(tempId, real);
    } catch (e) {
      useChat.getState().markFailed(tempId);
      useSession.getState().toast('Message failed to send', 'error');
      console.warn(e);
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  };

  // Mention autocomplete.
  const [mention, setMention] = useState<{ query: string; start: number; index: number } | null>(null);
  const mentionMatches = useMemo(() => {
    if (!mention) return [];
    const q = mention.query.toLowerCase();
    return names.filter((n) => n.toLowerCase().startsWith(q)).slice(0, 6);
  }, [mention, names]);
  const updateMention = (value: string, caret: number) => {
    const before = value.slice(0, caret);
    const m = before.match(/(?:^|\s)@([^\s@]*)$/);
    if (m) setMention({ query: m[1] ?? '', start: caret - (m[1]?.length ?? 0) - 1, index: 0 });
    else setMention(null);
  };
  const applyMention = (name: string) => {
    if (!mention) return;
    const caret = inputRef.current?.selectionStart ?? text.length;
    const next = `${text.slice(0, mention.start)}@${name} ${text.slice(caret)}`;
    setText(next);
    setMention(null);
    requestAnimationFrame(() => {
      const pos = mention.start + name.length + 2;
      inputRef.current?.setSelectionRange(pos, pos);
      inputRef.current?.focus();
    });
  };

  const insertEmoji = (e: string) => {
    const el = inputRef.current;
    const start = el?.selectionStart ?? text.length;
    const end = el?.selectionEnd ?? text.length;
    const next = text.slice(0, start) + e + text.slice(end);
    setText(next);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(start + e.length, start + e.length);
    });
  };

  const typers = Object.keys(typing).map(nameOf);

  return (
    <div className="flex flex-col h-full min-h-0">
      <div ref={listRef} onScroll={onScroll} className="flex-1 min-h-0 overflow-y-auto px-3 py-3 space-y-0.5">
        {messages.length === 0 && <div className="text-muted text-sm text-center py-8">No messages yet. Say hi 👋</div>}
        {messages.map((m, i) => {
          const prev = messages[i - 1];
          const grouped =
            !!prev && prev.kind === 'user' && m.kind === 'user' && prev.from.identity === m.from.identity && m.ts - prev.ts < GROUP_WINDOW_MS;
          return <MessageRow key={m.id} m={m} grouped={grouped} names={names} selfName={selfName} />;
        })}
      </div>

      <div className="relative">
        {!atBottom && (
          <button
            onClick={jump}
            className="anim-pop absolute -top-10 left-1/2 -translate-x-1/2 h-8 px-3 rounded-full bg-accent text-accent-ink text-[12px] font-semibold inline-flex items-center gap-1.5 shadow-lg"
          >
            <ArrowDown className="size-3.5" /> {unread > 0 ? `${unread} new` : 'Jump to newest'}
          </button>
        )}
        <div className="h-5 px-3 text-[11px] text-muted truncate">
          {typers.length > 0 && (
            <span className="anim-fade-in">
              {typers.length === 1 ? `${typers[0]} is typing…` : typers.length === 2 ? `${typers[0]} and ${typers[1]} are typing…` : 'Several people are typing…'}
            </span>
          )}
        </div>
      </div>

      <div className="relative border-t border-hairline p-2">
        {mention && mentionMatches.length > 0 && (
          <div className="glass-strong anim-pop absolute bottom-full left-2 mb-1 rounded-xl p-1 min-w-40 z-10">
            {mentionMatches.map((n, i) => (
              <button
                key={n}
                onMouseDown={(e) => {
                  e.preventDefault();
                  applyMention(n);
                }}
                className={cn('block w-full text-left h-8 px-2 rounded-lg text-[13px] hover:bg-hover', i === mention.index && 'bg-hover')}
              >
                @{n}
              </button>
            ))}
          </div>
        )}
        <div className="flex items-end gap-1 rounded-xl bg-panel border border-hairline focus-within:border-hairline-strong px-1.5 py-1">
          <Popover
            side="top"
            align="start"
            className="grid grid-cols-6 gap-0.5 p-1.5 w-[248px]"
            trigger={
              <button aria-label="Emoji" className="size-8 shrink-0 rounded-lg inline-flex items-center justify-center text-muted hover:text-text hover:bg-hover">
                <Sticker className="size-[18px]" />
              </button>
            }
          >
            {EMOJI.map((e) => (
              <button key={e} onClick={() => insertEmoji(e)} className="size-9 rounded-lg text-xl hover:bg-hover" aria-label={e}>
                {e}
              </button>
            ))}
          </Popover>
          <textarea
            ref={inputRef}
            value={text}
            rows={1}
            placeholder={token ? 'Message… (Enter to send)' : 'Connecting…'}
            disabled={!token}
            aria-label="Message"
            className="flex-1 min-w-0 max-h-32 resize-none bg-transparent px-1 py-1.5 text-sm outline-none placeholder:text-muted/70"
            onChange={(e) => {
              setText(e.target.value);
              onTyping();
              updateMention(e.target.value, e.target.selectionStart);
              e.target.style.height = 'auto';
              e.target.style.height = `${Math.min(128, e.target.scrollHeight)}px`;
            }}
            onKeyDown={(e) => {
              if (mention && mentionMatches.length > 0) {
                if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
                  e.preventDefault();
                  const d = e.key === 'ArrowDown' ? 1 : -1;
                  setMention({ ...mention, index: (mention.index + d + mentionMatches.length) % mentionMatches.length });
                  return;
                }
                if (e.key === 'Enter' || e.key === 'Tab') {
                  e.preventDefault();
                  applyMention(mentionMatches[mention.index] ?? mentionMatches[0]!);
                  return;
                }
                if (e.key === 'Escape') {
                  setMention(null);
                  return;
                }
              }
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                void send();
                e.currentTarget.style.height = 'auto';
              }
            }}
          />
          <button
            onClick={() => void send()}
            disabled={!text.trim() || !token}
            aria-label="Send"
            className="size-8 shrink-0 rounded-lg inline-flex items-center justify-center text-accent hover:bg-hover disabled:opacity-40"
          >
            <Send className="size-[18px]" />
          </button>
        </div>
      </div>
    </div>
  );
}

function MessageRow({ m, grouped, names, selfName }: { m: LocalMessage; grouped: boolean; names: string[]; selfName: string }) {
  if (m.kind === 'system') {
    return (
      <div className="py-1 text-center">
        <span className="text-[11px] text-muted/90 italic" title={absoluteTime(m.ts)}>
          {m.text}
        </span>
      </div>
    );
  }
  const mentionsMe = !!selfName && tokenize(m.text, [selfName]).some((t) => t.type === 'mention');
  return (
    <div
      className={cn(
        'group relative rounded-lg px-2 -mx-2 hover:bg-hover/60',
        grouped ? 'py-0.5' : 'pt-2.5 pb-0.5',
        m.pending && 'opacity-60',
        m.failed && 'opacity-60 line-through',
        mentionsMe && 'bg-accent/10 hover:bg-accent/15',
      )}
    >
      {!grouped && (
        <div className="flex items-baseline gap-2">
          <span className="text-[13px] font-semibold" style={{ color: m.from.color }}>
            {m.from.name}
          </span>
          <span className="text-[11px] text-muted" title={absoluteTime(m.ts)}>
            {relativeTime(m.ts)}
          </span>
        </div>
      )}
      <div className="chat-text text-[13.5px] leading-[1.45]">
        <RichText text={m.text} names={names} />
      </div>
      {grouped && (
        <span className="absolute right-2 top-0.5 text-[10px] text-muted opacity-0 group-hover:opacity-100" title={absoluteTime(m.ts)}>
          {relativeTime(m.ts)}
        </span>
      )}
    </div>
  );
}

function RichText({ text, names }: { text: string; names: string[] }) {
  const tokens = useMemo(() => tokenize(text, names), [text, names]);
  const images = tokens.filter((t) => t.type === 'link' && isImageUrl(t.href)).slice(0, 3);
  return (
    <>
      {tokens.map((t, i) => {
        if (t.type === 'link') {
          return (
            <a key={i} href={t.href} target="_blank" rel="noopener noreferrer nofollow" className="text-info underline decoration-info/40 hover:decoration-info break-all">
              {t.value}
            </a>
          );
        }
        if (t.type === 'mention') {
          return (
            <span key={i} className="rounded px-1 bg-accent/20 text-accent font-medium">
              {t.value}
            </span>
          );
        }
        return <span key={i}>{t.value}</span>;
      })}
      {images.length > 0 && (
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {images.map((t, i) =>
            t.type === 'link' ? (
              <a key={i} href={t.href} target="_blank" rel="noopener noreferrer nofollow" className="block max-w-full">
                <img src={t.href} alt="" loading="lazy" referrerPolicy="no-referrer" className="max-h-56 max-w-full rounded-lg border border-hairline object-contain" />
              </a>
            ) : null,
          )}
        </div>
      )}
    </>
  );
}
