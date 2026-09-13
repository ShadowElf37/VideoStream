import * as RT from '@radix-ui/react-tabs';
import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export const Tabs = RT.Root;
export const TabsContent = RT.Content;

export function TabsList({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <RT.List className={cn('flex items-center gap-1 p-1 rounded-xl bg-panel border border-hairline', className)}>
      {children}
    </RT.List>
  );
}

export function TabsTrigger({
  value,
  children,
  badge,
  className,
}: {
  value: string;
  children: ReactNode;
  badge?: number;
  className?: string;
}) {
  return (
    <RT.Trigger
      value={value}
      className={cn(
        'relative flex-1 h-8 px-3 rounded-lg text-[13px] font-medium text-muted inline-flex items-center justify-center gap-1.5 transition-colors duration-150',
        'hover:text-text data-[state=active]:bg-active data-[state=active]:text-text',
        className,
      )}
    >
      {children}
      {badge ? (
        <span className="min-w-[18px] h-[18px] px-1 rounded-full bg-accent text-accent-ink text-[11px] font-semibold inline-flex items-center justify-center leading-none">
          {badge > 99 ? '99+' : badge}
        </span>
      ) : null}
    </RT.Trigger>
  );
}
