import * as RD from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  className,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <RD.Root open={open} onOpenChange={onOpenChange}>
      <RD.Portal>
        <RD.Overlay className="fixed inset-0 z-[70] bg-black/60 backdrop-blur-sm anim-fade-in" />
        <RD.Content
          className={cn(
            'glass-strong anim-pop fixed z-[71] left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-[min(92vw,560px)] max-h-[88vh] overflow-y-auto rounded-2xl p-6 outline-none',
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 mb-4">
            <div>
              <RD.Title className="text-lg font-semibold tracking-tight">{title}</RD.Title>
              {description ? (
                <RD.Description className="text-muted text-sm mt-0.5">{description}</RD.Description>
              ) : (
                <RD.Description className="sr-only">{typeof title === 'string' ? title : 'Dialog'}</RD.Description>
              )}
            </div>
            <RD.Close
              aria-label="Close"
              className="size-8 rounded-lg inline-flex items-center justify-center text-muted hover:text-text hover:bg-hover"
            >
              <X className="size-4" />
            </RD.Close>
          </div>
          {children}
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  );
}
