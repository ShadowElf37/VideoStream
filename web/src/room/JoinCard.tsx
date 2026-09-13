import { ArrowRight, Lock } from 'lucide-react';
import { useState } from 'react';
import { colorFor } from '@/lib/colors';
import type { RoomInfo } from '@/proto/messages';
import { usePrefs } from '@/state/prefs';
import { Avatar } from '@/ui/Avatar';
import { Button } from '@/ui/Button';
import { Field, Input } from '@/ui/Field';

/** Step 1: who are you (name, password if the room has one). */
export function JoinCard({ room, isHost, onNext }: { room: RoomInfo; isHost: boolean; onNext: (name: string, password: string) => void }) {
  const savedName = usePrefs((s) => s.name);
  const setPref = usePrefs((s) => s.set);
  const [name, setName] = useState(savedName);
  const [password, setPassword] = useState('');
  const valid = name.trim().length > 0 && (!room.hasPassword || password.length > 0);

  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!valid) return;
        setPref('name', name.trim());
        onNext(name.trim(), password);
      }}
    >
      <div className="flex items-center gap-3">
        <Avatar name={name || '?'} color={colorFor(name || 'x')} size={44} />
        <div className="flex-1">
          <Field label="Your name">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="How friends know you" maxLength={40} autoFocus autoComplete="nickname" />
          </Field>
        </div>
      </div>
      {room.hasPassword && (
        <Field label="Room password">
          <div className="relative">
            <Lock className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted" />
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="pl-9" autoComplete="current-password" />
          </div>
        </Field>
      )}
      {isHost && <p className="text-xs text-accent">You're joining with the host link: you'll get the transport controls.</p>}
      <Button type="submit" variant="primary" size="lg" className="w-full" disabled={!valid}>
        Continue <ArrowRight className="size-4" />
      </Button>
    </form>
  );
}
