import { Check, Copy, KeyRound, LogOut, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { useSession } from '@/state/session';
import { Button } from '@/ui/Button';
import { Dialog } from '@/ui/Dialog';

/**
 * The one place the invite link lives. There is no host link: hosts get in
 * with the password, once per device, and everyone who has been in is
 * remembered by cookie. So the dialog is short — the link, why it might stop
 * working, and the host's "that got out" button.
 */
export function InviteDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const links = useSession((s) => s.links);
  const role = useSession((s) => s.role);
  const token = useSession((s) => s.token);
  const [busy, setBusy] = useState(false);
  const isHost = role === 'host';

  // The link may have rotated since we joined (a host pressed the button, or
  // the room stood empty while we were reconnecting); refresh on open.
  useEffect(() => {
    if (!open || !token) return;
    api
      .getLinks(token.session)
      .then((l) => useSession.getState().setLinks(l))
      .catch(() => undefined);
  }, [open, token]);

  const rotate = async () => {
    if (!token) return;
    setBusy(true);
    try {
      const l = await api.rotateLinks(token.session);
      useSession.getState().setLinks(l);
      useSession.getState().toast('Invite link refreshed. The old one no longer works.', 'info', 4000);
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    } finally {
      setBusy(false);
    }
  };

  const forget = async () => {
    try {
      await api.logout();
      useSession.getState().toast('This device is forgotten. Next time you will need a link or the password.', 'info', 5000);
      onOpenChange(false);
    } catch (e) {
      useSession.getState().toast(e instanceof Error ? e.message : String(e), 'error');
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Invite friends" description="Anyone with this link gets in as a viewer.">
      <div className="space-y-5">
        <LinkRow value={links?.viewer ?? ''} />
        <p className="text-[12.5px] text-muted leading-relaxed">
          The link stops working once the room has been empty for a couple of minutes, so one that sat in a group chat for a
          week does not admit strangers. Friends who have been in before are remembered on their device and keep getting in.
        </p>

        <div className="rounded-xl bg-panel border border-hairline p-3 space-y-2">
          <div className="flex items-center gap-2 text-[13px] font-medium">
            <KeyRound className="size-4 text-accent" /> Hosts
          </div>
          <p className="text-[12.5px] text-muted">
            There is no host link. A host types the room password at the door once per device, and the desktop projector
            joins with the same password.
          </p>
          {isHost && (
            <Button size="sm" onClick={() => void rotate()} loading={busy}>
              <RefreshCw className="size-3.5" /> Refresh the invite link now
            </Button>
          )}
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-hairline pt-4">
          <span className="text-[12px] text-muted">Shared computer?</span>
          <Button size="sm" variant="ghost" onClick={() => void forget()}>
            <LogOut className="size-3.5" /> Forget this device
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

function LinkRow({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    if (!value) return;
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
      <input
        readOnly
        value={value || 'Loading…'}
        onFocus={(e) => e.currentTarget.select()}
        className="flex-1 min-w-0 h-10 px-3 rounded-lg bg-panel border border-hairline text-[13px] font-mono"
        aria-label="Invite link"
      />
      <Button size="md" variant="primary" onClick={copy} aria-label="Copy invite link" className="w-[104px]" disabled={!value}>
        {copied ? (
          <>
            <Check className="size-4" /> Copied
          </>
        ) : (
          <>
            <Copy className="size-4" /> Copy
          </>
        )}
      </Button>
    </div>
  );
}
