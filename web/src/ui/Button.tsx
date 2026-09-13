import { forwardRef, type ButtonHTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

export type ButtonVariant = 'primary' | 'ghost' | 'subtle' | 'danger';
export type ButtonSize = 'sm' | 'md' | 'lg';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}

const variants: Record<ButtonVariant, string> = {
  primary: 'bg-accent text-accent-ink hover:brightness-110 active:brightness-95 font-semibold shadow-[0_1px_0_rgba(255,255,255,0.15)_inset]',
  ghost: 'bg-transparent text-text hover:bg-hover active:bg-active',
  subtle: 'bg-panel border border-hairline text-text hover:bg-hover active:bg-active',
  danger: 'bg-danger/15 text-danger border border-danger/30 hover:bg-danger/25',
};

const sizes: Record<ButtonSize, string> = {
  sm: 'h-8 px-3 text-[13px] gap-1.5 rounded-lg',
  md: 'h-10 px-4 text-sm gap-2 rounded-xl',
  lg: 'h-12 px-5 text-[15px] gap-2 rounded-xl',
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'subtle', size = 'md', loading, className, children, disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center justify-center whitespace-nowrap select-none transition-[background,filter,transform] duration-150 ease-out disabled:opacity-50 disabled:pointer-events-none',
        variants[variant],
        sizes[size],
        className,
      )}
      {...rest}
    >
      {loading && (
        <span className="anim-spin inline-block size-3.5 rounded-full border-2 border-current border-t-transparent" />
      )}
      {children}
    </button>
  );
});
