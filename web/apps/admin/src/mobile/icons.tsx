// Bold glyphs for the status symbol, closer to SF Symbols than the
// thin outline icons used elsewhere.

type P = { className?: string };

export function CheckIcon({ className }: P) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M5 12.5l4.5 4.5L19 7.5" />
    </svg>
  );
}

export function XIcon({ className }: P) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" aria-hidden>
      <path d="M7 7l10 10M17 7L7 17" />
    </svg>
  );
}

export function ExclamationTriangleIcon({ className }: P) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" stroke="currentColor" strokeWidth={3.2} strokeLinecap="round" aria-hidden>
      <path d="M12 6v8M12 18.5v.01" />
    </svg>
  );
}
