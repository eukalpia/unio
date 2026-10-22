import { cn } from "../lib/utils";

/**
 * The horseshoe U mark, drawn from assets/logo/unio-mark.svg. The stroke uses
 * currentColor, and the data point uses the brand accent. The small variant
 * has the heavier stroke and larger dot from unio-icon-small.svg.
 */
export function UnioMark({ className, small = false }: { className?: string; small?: boolean }) {
  return (
    <svg
      viewBox="0 0 120 120"
      role="img"
      aria-label="Unio"
      className={cn("size-6 shrink-0", className)}
      data-slot="unio-mark"
    >
      <path
        d="M90.6 35.3A40 40 0 1 1 29.4 35.3"
        fill="none"
        stroke="currentColor"
        strokeWidth={small ? 22 : 18}
        strokeLinecap="round"
      />
      <circle cx="60" cy="22" r={small ? 11 : 9} className="fill-brand" />
    </svg>
  );
}

/** The mark with the lower-case wordmark, set in Space Grotesk Bold. */
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <UnioMark />
      <span className="font-display text-lg font-bold tracking-[-0.03em] lowercase">unio</span>
    </span>
  );
}
