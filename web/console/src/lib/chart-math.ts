/**
 * Rounds a positive value up to a clean axis maximum: 1, 2, 2.5, 5 or 10 times
 * a power of ten. Zero or negative input gives 1, so an empty chart still has an axis.
 */
export function niceCeil(value: number): number {
  if (!Number.isFinite(value) || value <= 0) {
    return 1;
  }
  const exponent = Math.floor(Math.log10(value));
  const base = 10 ** exponent;
  const fraction = value / base;
  const step = fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 2.5 ? 2.5 : fraction <= 5 ? 5 : 10;
  return step * base;
}

/** Maps a value in [domainMin, domainMax] to a pixel position in [rangeMin, rangeMax]. */
export function linear(value: number, domainMin: number, domainMax: number, rangeMin: number, rangeMax: number): number {
  if (domainMax === domainMin) {
    return rangeMin;
  }
  return rangeMin + ((value - domainMin) / (domainMax - domainMin)) * (rangeMax - rangeMin);
}

/** Builds an SVG path for a polyline. Values are already in pixel space. */
export function polylinePath(points: readonly { x: number; y: number }[]): string {
  return points.map((point, index) => `${index === 0 ? "M" : "L"}${point.x.toFixed(1)} ${point.y.toFixed(1)}`).join(" ");
}
