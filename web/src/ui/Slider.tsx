import * as RS from '@radix-ui/react-slider';
import { cn } from '@/lib/cn';

export interface SliderProps {
  value: number;
  min?: number;
  max?: number;
  step?: number;
  onChange: (v: number) => void;
  onCommit?: (v: number) => void;
  label: string;
  disabled?: boolean;
  className?: string;
  /** Optional tick marks (as values) rendered on the track. */
  ticks?: number[];
  orientation?: 'horizontal' | 'vertical';
  accent?: boolean;
}

export function Slider({
  value,
  min = 0,
  max = 1,
  step = 0.01,
  onChange,
  onCommit,
  label,
  disabled,
  className,
  ticks,
  orientation = 'horizontal',
  accent,
}: SliderProps) {
  const vertical = orientation === 'vertical';
  return (
    <RS.Root
      value={[value]}
      min={min}
      max={max}
      step={step}
      disabled={disabled}
      orientation={orientation}
      onValueChange={(v) => onChange(v[0] ?? min)}
      onValueCommit={(v) => onCommit?.(v[0] ?? min)}
      className={cn(
        'relative flex touch-none select-none items-center data-[disabled]:opacity-40',
        vertical ? 'h-full w-5 flex-col justify-center' : 'h-5 w-full',
        className,
      )}
    >
      <RS.Track className={cn('relative grow rounded-full bg-hairline-strong', vertical ? 'w-1' : 'h-1')}>
        <RS.Range className={cn('absolute rounded-full', accent ? 'bg-accent' : 'bg-text/80', vertical ? 'w-full' : 'h-full')} />
        {ticks?.map((t) => {
          const pct = ((t - min) / (max - min)) * 100;
          return (
            <span
              key={t}
              className="absolute top-1/2 -translate-y-1/2 h-2 w-px bg-text/40"
              style={vertical ? { bottom: `${pct}%`, left: 0, width: '100%', height: 1 } : { left: `${pct}%` }}
            />
          );
        })}
      </RS.Track>
      <RS.Thumb className="slider-thumb" aria-label={label} />
    </RS.Root>
  );
}
