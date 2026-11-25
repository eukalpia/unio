import { linear, polylinePath } from "../../lib/chart-math";
import { useElementWidth } from "../../lib/use-element-width";

const HEIGHT = 40;
const PAD = 4;

/**
 * A 12-point trend line for a stat tile. The line uses the de-emphasis text
 * tone, and the latest point carries the accent. Its value is in the tile text,
 * so the sparkline is decorative and hidden from assistive technology.
 */
export function Sparkline({ values, className }: { values: readonly number[]; className?: string }) {
  const { ref, width } = useElementWidth<HTMLDivElement>(160);
  if (values.length < 2) {
    return <div ref={ref} className={className} style={{ height: HEIGHT }} aria-hidden="true" />;
  }
  const min = Math.min(...values);
  const max = Math.max(...values);
  const points = values.map((value, index) => ({
    x: linear(index, 0, values.length - 1, PAD, width - PAD),
    y: linear(value, min, max === min ? min + 1 : max, HEIGHT - PAD, PAD),
  }));
  const last = points[points.length - 1];
  return (
    <div ref={ref} className={className} aria-hidden="true">
      <svg width={width} height={HEIGHT} viewBox={`0 0 ${width} ${HEIGHT}`} focusable="false">
        <path d={polylinePath(points)} fill="none" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="stroke-muted-foreground" />
        {last ? <circle cx={last.x} cy={last.y} r={4} className="fill-chart-1 stroke-card" strokeWidth={2} /> : null}
      </svg>
    </div>
  );
}
