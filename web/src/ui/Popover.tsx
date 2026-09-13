import * as RP from '@radix-ui/react-popover';
import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function Popover({
  trigger,
  children,
  side = 'top',
  align = 'center',
  className,
  open,
  onOpenChange,
}: {
  trigger: ReactNode;
  children: ReactNode;
  side?: 'top' | 'bottom' | 'left' | 'right';
  align?: 'start' | 'center' | 'end';
  className?: string;
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
}) {
  return (
    <RP.Root open={open} onOpenChange={onOpenChange}>
      <RP.Trigger asChild>{trigger}</RP.Trigger>
      <RP.Portal>
        <RP.Content
          side={side}
          align={align}
          sideOffset={10}
          collisionPadding={12}
          className={cn('glass-strong anim-pop z-50 rounded-xl p-3 outline-none', className)}
        >
          {children}
        </RP.Content>
      </RP.Portal>
    </RP.Root>
  );
}
