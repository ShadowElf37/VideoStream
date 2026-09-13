import { forwardRef, type ButtonHTMLAttributes } from 'react';
import { cn } from '@/lib/cn';
import { Tooltip } from './Tooltip';

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string;
  /** Keyboard hint rendered inside the tooltip. */
  kbd?: string;
  size?: 'sm' | 'md' | 'lg';
  active?: boolean;
  tone?: 'default' | 'danger' | 'warn' | 'info' | 'accent';
  tooltipSide?: 'top' | 'bottom' | 'left' | 'right';
}

const sizes = { sm: 'size-8 rounded-lg [&>svg]:size-4', md: 'size-10 rounded-xl [&>svg]:size-5', lg: 'size-12 rounded-xl [&>svg]:size-[22px]' };

const activeTones = {
  default: 'bg-active text-text',
  danger: 'bg-danger/20 text-danger ring-1 ring-danger/40',
  warn: 'bg-warn/20 text-warn ring-1 ring-warn/40',
  info: 'bg-info/20 text-info ring-1 ring-info/40',
  accent: 'bg-accent/20 text-accent ring-1 ring-accent/40',
};

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, kbd, size = 'md', active, tone = 'default', tooltipSide = 'top', className, children, ...rest },
  ref,
) {
  return (
    <Tooltip label={label} kbd={kbd} side={tooltipSide}>
      <button
        ref={ref}
        aria-label={label}
        aria-pressed={active}
        className={cn(
          'inline-flex items-center justify-center text-muted hover:text-text hover:bg-hover active:bg-active transition-[background,color] duration-150 ease-out disabled:opacity-40 disabled:pointer-events-none',
          sizes[size],
          active && activeTones[tone],
          className,
        )}
        {...rest}
      >
        {children}
      </button>
    </Tooltip>
  );
});
