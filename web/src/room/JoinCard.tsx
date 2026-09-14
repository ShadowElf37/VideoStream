import { ArrowRight, Crown, KeyRound, Lock, Users } from 'lucide-react';
import { useState } from 'react';
import { colorFor } from '@/lib/colors';
import type { Access } from '@/proto/messages';
import { usePrefs } from '@/state/prefs';
import { Avatar } from '@/ui/Avatar';
import { Button } from '@/ui/Button';
import { Field, Input } from '@/ui/Field';

/**
 * Step 1 at the door: who are you, and — only if nothing already vouches for
 * you — the room password.
 *
 * `access` is what the server said the visitor already holds: the key in the
 * link they opened, or the cookie from an earlier visit. A host's device
 * remembers being a host, so the password field is the exception, not the
 * rule.
 */
export function JoinCard({
  access,
  hadKey,
  occupants,
  notice,
  onNext,
}: {
  access: Access;
  /** A key was in the URL (so 'none' means it has been rotated away). */
  hadKey: boolean;
  occupants: number;
  /** Why we are back at the door, if we were inside a moment ago. */
  notice?: string | null;
  onNext: (name: string, password: string) => void;
}) {
  const savedName = usePrefs((s) => s.name);
  const setPref = usePrefs((s) => s.set);
  const [name, setName] = useState(savedName);
  const [password, setPassword] = useState('');
  // A viewer can always claim the host seat with the password; the field is
  // tucked away so friends are not asked for a secret they do not have.
  const [claimHost, setClaimHost] = useState(false);
  const needsPassword = access === 'none';
  const showPassword = needsPassword || claimHost;
  const valid = name.trim().length > 0 && (!needsPassword || password.length > 0);

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!valid) return;
        setPref('name', name.trim());
        onNext(name.trim(), showPassword ? password : '');
      }}
    >
      {notice && <div className="rounded-xl bg-warn/10 border border-warn/30 text-warn text-[13px] px-3 py-2">{notice}</div>}

      <Standing access={access} hadKey={hadKey} occupants={occupants} />

      <div className="flex items-center gap-3">
        <Avatar name={name || '?'} color={colorFor(name || 'x')} size={44} />
        <div className="flex-1">
          <Field label="Your name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="How friends know you" maxLength={32} autoFocus autoComplete="nickname" />
          </Field>
        </div>
      </div>

      {showPassword ? (
        <Field label="Room password" hint={needsPassword ? undefined : 'Optional: enter it to take the host seat.'}>
          <div className="relative">
            <Lock className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted" />
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="pl-9" autoComplete="current-password" autoFocus={!!savedName} />
          </div>
        </Field>
      ) : (
        access === 'viewer' && (
          <button type="button" onClick={() => setClaimHost(true)} className="inline-flex items-center gap-1.5 text-xs text-muted hover:text-text">
            <KeyRound className="size-3.5" /> I'm the host: enter the password
          </button>
        )
      )}

      <Button type="submit" variant="primary" size="lg" className="w-full" disabled={!valid}>
        Continue <ArrowRight className="size-4" />
      </Button>
    </form>
  );
}

/** One line on where the visitor stands, before they type anything. */
function Standing({ access, hadKey, occupants }: { access: Access; hadKey: boolean; occupants: number }) {
  const who = occupants === 0 ? 'Nobody is in yet.' : occupants === 1 ? 'One person is in.' : `${occupants} people are in.`;
  if (access === 'host') {
    return (
      <p className="flex items-start gap-2 text-[13px] text-muted">
        <Crown className="size-4 text-accent shrink-0 mt-0.5" />
        <span>
          This device is remembered as <span className="text-text">host</span>. {who}
        </span>
      </p>
    );
  }
  if (access === 'viewer') {
    return (
      <p className="flex items-start gap-2 text-[13px] text-muted">
        <Users className="size-4 text-accent shrink-0 mt-0.5" />
        <span>
          You have an invite. {who}
        </span>
      </p>
    );
  }
  return (
    <p className="flex items-start gap-2 text-[13px] text-muted">
      <Lock className="size-4 shrink-0 mt-0.5" />
      <span>
        {hadKey
          ? 'That invite link has expired: links refresh once the room has been empty for a while. Ask for a fresh one, or enter the room password.'
          : 'This is a private room. The password gets you in as host, and this device will be remembered.'}
      </span>
    </p>
  );
}
