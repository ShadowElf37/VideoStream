export type Token =
  | { type: 'text'; value: string }
  | { type: 'link'; value: string; href: string }
  | { type: 'mention'; value: string; name: string };

const URL_RE = /\bhttps?:\/\/[^\s<>"'`]+/gi;
const MENTION_RE = /(^|[\s(])@([\p{L}\p{N}_.-]+(?: [\p{L}\p{N}_.-]+)?)/gu;

/** Strip trailing punctuation that is almost never part of a pasted URL. */
function trimUrl(raw: string): [string, string] {
  let url = raw;
  let rest = '';
  while (url.length > 0) {
    const last = url[url.length - 1]!;
    if ('.,;:!?'.includes(last)) {
      url = url.slice(0, -1);
      rest = last + rest;
      continue;
    }
    if (last === ')' && (url.match(/\(/g)?.length ?? 0) < (url.match(/\)/g)?.length ?? 0)) {
      url = url.slice(0, -1);
      rest = last + rest;
      continue;
    }
    break;
  }
  return [url, rest];
}

/**
 * Split a message into text / link / mention tokens. `names` is the set of
 * participant names that count as valid mentions (longest match wins so
 * "@Jean Luc" resolves when both "Jean" and "Jean Luc" exist).
 */
export function tokenize(text: string, names: readonly string[] = []): Token[] {
  const out: Token[] = [];
  let last = 0;
  const pushText = (s: string) => {
    if (!s) return;
    for (const t of tokenizeMentions(s, names)) out.push(t);
  };
  for (const m of text.matchAll(URL_RE)) {
    const start = m.index ?? 0;
    pushText(text.slice(last, start));
    const [url, rest] = trimUrl(m[0]);
    out.push({ type: 'link', value: url, href: url });
    last = start + m[0].length;
    if (rest) pushText(rest);
  }
  pushText(text.slice(last));
  return mergeText(out);
}

function tokenizeMentions(text: string, names: readonly string[]): Token[] {
  if (names.length === 0 || !text.includes('@')) return [{ type: 'text', value: text }];
  const sorted = [...names].filter(Boolean).sort((a, b) => b.length - a.length);
  const out: Token[] = [];
  let last = 0;
  for (const m of text.matchAll(MENTION_RE)) {
    const idx = (m.index ?? 0) + m[1]!.length;
    const candidate = m[2]!;
    const name = sorted.find((n) => candidate.toLowerCase().startsWith(n.toLowerCase()));
    if (!name) continue;
    out.push({ type: 'text', value: text.slice(last, idx) });
    out.push({ type: 'mention', value: `@${candidate.slice(0, name.length)}`, name });
    last = idx + 1 + name.length;
  }
  out.push({ type: 'text', value: text.slice(last) });
  return out.filter((t) => t.type !== 'text' || t.value);
}

function mergeText(tokens: Token[]): Token[] {
  const out: Token[] = [];
  for (const t of tokens) {
    const prev = out[out.length - 1];
    if (t.type === 'text' && prev?.type === 'text') prev.value += t.value;
    else out.push({ ...t });
  }
  return out;
}

const IMAGE_RE = /\.(png|jpe?g|gif|webp|avif)(\?.*)?$/i;

export function isImageUrl(url: string): boolean {
  try {
    const u = new URL(url);
    return IMAGE_RE.test(u.pathname) || u.hostname.endsWith('giphy.com') && u.pathname.includes('/media/');
  } catch {
    return false;
  }
}

export function mentionsName(text: string, name: string): boolean {
  if (!name) return false;
  return tokenize(text, [name]).some((t) => t.type === 'mention' && t.name === name);
}

/** Parse an invite/host/projector link (or bare path) into a router path, or null. */
export function parseRoomLink(input: string): { path: string; roomId: string; kind: 'invite' | 'host' | 'projector' | 'plain' } | null {
  const s = input.trim();
  if (!s) return null;
  let url: URL;
  try {
    url = s.startsWith('/') ? new URL(s, 'http://local') : new URL(s.includes('://') ? s : `https://${s}`);
  } catch {
    return null;
  }
  const m = url.pathname.match(/^\/r\/([^/]+)\/?$/);
  if (!m) return null;
  const roomId = decodeURIComponent(m[1]!);
  const q = url.searchParams;
  const kind = q.has('h') ? 'host' : q.has('k') ? 'invite' : q.has('p') ? 'projector' : 'plain';
  return { path: `/r/${encodeURIComponent(roomId)}${url.search}`, roomId, kind };
}
