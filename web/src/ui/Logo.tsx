export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden className="shrink-0">
      <rect width="64" height="64" rx="14" fill="#0b0b0d" />
      <rect x="10" y="14" width="44" height="36" rx="6" fill="#f5b942" fillOpacity="0.08" stroke="#f5b942" strokeWidth="3" />
      <g fill="#f5b942">
        <rect x="14" y="18" width="5" height="5" rx="1" />
        <rect x="14" y="29.5" width="5" height="5" rx="1" />
        <rect x="14" y="41" width="5" height="5" rx="1" />
        <rect x="45" y="18" width="5" height="5" rx="1" />
        <rect x="45" y="29.5" width="5" height="5" rx="1" />
        <rect x="45" y="41" width="5" height="5" rx="1" />
        <path d="M28 24.5v15l12-7.5z" />
      </g>
    </svg>
  );
}
