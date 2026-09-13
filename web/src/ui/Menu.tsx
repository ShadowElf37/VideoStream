import * as RM from '@radix-ui/react-dropdown-menu';
import { Check } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function Menu({
  trigger,
  children,
  side = 'top',
  align = 'center',
  className,
}: {
  trigger: ReactNode;
  children: ReactNode;
  side?: 'top' | 'bottom' | 'left' | 'right';
  align?: 'start' | 'center' | 'end';
  className?: string;
}) {
  return (
    <RM.Root modal={false}>
      <RM.Trigger asChild>{trigger}</RM.Trigger>
      <RM.Portal>
        <RM.Content
          side={side}
          align={align}
          sideOffset={8}
          collisionPadding={12}
          className={cn('glass-strong anim-pop z-50 min-w-44 max-h-[60vh] overflow-y-auto rounded-xl p-1.5 outline-none', className)}
        >
          {children}
        </RM.Content>
      </RM.Portal>
    </RM.Root>
  );
}

export function MenuItem({
  children,
  onSelect,
  selected,
  disabled,
  destructive,
  hint,
}: {
  children: ReactNode;
  onSelect?: () => void;
  selected?: boolean;
  disabled?: boolean;
  destructive?: boolean;
  hint?: ReactNode;
}) {
  return (
    <RM.Item
      disabled={disabled}
      onSelect={onSelect}
      className={cn(
        'flex items-center gap-2 h-8 px-2 rounded-lg text-[13px] outline-none cursor-default select-none data-[highlighted]:bg-hover data-[disabled]:opacity-40',
        destructive && 'text-danger',
      )}
    >
      <span className="w-4 inline-flex justify-center">{selected && <Check className="size-3.5 text-accent" />}</span>
      <span className="flex-1 truncate">{children}</span>
      {hint && <span className="text-xs text-muted">{hint}</span>}
    </RM.Item>
  );
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <RM.Label className="px-2 pt-1.5 pb-1 text-[11px] uppercase tracking-wider text-muted">{children}</RM.Label>;
}

export function MenuSeparator() {
  return <RM.Separator className="my-1 h-px bg-hairline" />;
}
