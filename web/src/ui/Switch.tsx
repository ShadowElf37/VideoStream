import * as RS from '@radix-ui/react-switch';
import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

export function Switch({
  checked,
  onCheckedChange,
  label,
  hint,
  disabled,
  className,
}: {
  checked: boolean;
  onCheckedChange: (v: boolean) => void;
  label: ReactNode;
  hint?: ReactNode;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <label className={cn('flex items-center justify-between gap-4 py-2 cursor-pointer', disabled && 'opacity-50', className)}>
      <span className="min-w-0">
        <span className="block text-sm">{label}</span>
        {hint && <span className="block text-xs text-muted mt-0.5">{hint}</span>}
      </span>
      <RS.Root
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
        className="relative h-6 w-10 shrink-0 rounded-full bg-hairline-strong transition-colors duration-150 data-[state=checked]:bg-accent"
      >
        <RS.Thumb className="block size-5 translate-x-0.5 rounded-full bg-white shadow transition-transform duration-150 ease-out data-[state=checked]:translate-x-[18px]" />
      </RS.Root>
    </label>
  );
}
