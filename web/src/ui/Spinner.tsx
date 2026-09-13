import { cn } from '@/lib/cn';

export function Spinner({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <span
      role="status"
      aria-label="Loading"
      className={cn('anim-spin inline-block rounded-full border-2 border-white/25 border-t-white', className)}
      style={{ width: size, height: size }}
    />
  );
}
