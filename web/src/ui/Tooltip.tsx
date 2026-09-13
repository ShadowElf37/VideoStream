import * as RT from '@radix-ui/react-tooltip';
import type { ReactNode } from 'react';
import { Kbd } from './Kbd';

export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <RT.Provider delayDuration={400} skipDelayDuration={200}>
      {children}
    </RT.Provider>
  );
}

export function Tooltip({
  label,
  kbd,
  side = 'top',
  children,
}: {
  label: ReactNode;
  kbd?: string;
  side?: 'top' | 'bottom' | 'left' | 'right';
  children: ReactNode;
}) {
  return (
    <RT.Root>
      <RT.Trigger asChild>{children}</RT.Trigger>
      <RT.Portal>
        <RT.Content
          side={side}
          sideOffset={6}
          collisionPadding={8}
          className="glass-strong anim-pop z-[60] rounded-lg px-2.5 py-1.5 text-xs text-text flex items-center gap-2 pointer-events-none"
        >
          <span>{label}</span>
          {kbd && <Kbd>{kbd}</Kbd>}
        </RT.Content>
      </RT.Portal>
    </RT.Root>
  );
}
