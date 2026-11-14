/** The horn mark: the Gjallarhorn sounding a warning. */
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg className="logo" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true" focusable="false">
      <rect width="64" height="64" rx="14" fill="#0b1424" />
      <path d="M14 40c8-16 28-22 38-20-6 3-9 8-10 14l-6-2c-1 5-4 9-8 12l-2-6c-4 1-8 2-12 2z" fill="#38e1ff" />
      <circle cx="46" cy="20" r="4" fill="#ff3b5c" />
    </svg>
  );
}
