import { cn } from '@/lib/cn';
import { inkFor } from '@/lib/colors';
import { initials } from '@/lib/format';

export function Avatar({
  name,
  color,
  size = 32,
  speaking,
  className,
}: {
  name: string;
  color: string;
  size?: number;
  speaking?: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      className={cn(
        'inline-flex shrink-0 items-center justify-center rounded-full font-semibold select-none transition-shadow duration-150',
        speaking && 'speaking-ring',
        className,
      )}
      style={{ width: size, height: size, background: color, color: inkFor(color), fontSize: Math.round(size * 0.4) }}
    >
      {initials(name)}
    </span>
  );
}
