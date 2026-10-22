import { useState, type KeyboardEvent, type PointerEvent } from "react";
import type { MetricsSample } from "../../lib/metrics-history";
import { formatBytes, formatRate } from "../../lib/format";
import { linear, niceCeil, polylinePath } from "../../lib/chart-math";
import { useElementWidth } from "../../lib/use-element-width";
import { cn } from "../../lib/utils";

const HEIGHT = 220;
const MARGIN = { top: 14, right: 92, bottom: 30, left: 60 };
const MIN_WIDTH = 300;

interface SeriesDef {
  key: "bytesIn" | "bytesOut";
  label: string;
  /** Tailwind classes for the stroke and the key swatch. Slots come from the validated categorical pair. */
  stroke: string;
  swatch: string;
  read: (sample: MetricsSample) => number;
}

const SERIES: readonly SeriesDef[] = [
  {
    key: "bytesIn",
    label: "Bytes in per second",
    stroke: "stroke-chart-1",
    swatch: "bg-chart-1",
    read: (sample) => sample.bytesInPerSecond,
  },
  {
    key: "bytesOut",
    label: "Bytes out per second",
    stroke: "stroke-chart-2",
    swatch: "bg-chart-2",
    read: (sample) => sample.bytesOutPerSecond,
  },
];

function clock(at: number): string {
  return new Date(at).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/**
 * Two throughput series on one axis. A legend is always shown, and the latest
 * value of each series is labelled at its end. The crosshair and tooltip follow
 * the pointer, and the arrow keys move them too, so keyboard users get the same
 * readout. A table of the samples sits below the chart.
 */
export function ThroughputChart({ samples }: { samples: readonly MetricsSample[] }) {
  const { ref, width: measured } = useElementWidth<HTMLDivElement>(MIN_WIDTH);
  const width = Math.max(MIN_WIDTH, measured);
  const [cursor, setCursor] = useState<number | null>(null);

  const count = samples.length;
  const plotWidth = width - MARGIN.left - MARGIN.right;
  const plotHeight = HEIGHT - MARGIN.top - MARGIN.bottom;
  const peak = Math.max(0, ...samples.flatMap((sample) => SERIES.map((series) => series.read(sample))));
  const yMax = niceCeil(peak * 1.1);
  const xOf = (index: number): number => linear(index, 0, Math.max(1, count - 1), MARGIN.left, MARGIN.left + plotWidth);
  const yOf = (value: number): number => linear(value, 0, yMax, MARGIN.top + plotHeight, MARGIN.top);
  const ticks = [0, yMax / 2, yMax];
  const latest = samples[count - 1];
  const active = cursor !== null && cursor < count ? cursor : null;

  function indexFromPointer(clientX: number, element: Element): number {
    const box = element.getBoundingClientRect();
    const fraction = box.width === 0 ? 0 : (clientX - box.left - MARGIN.left) / plotWidth;
    return Math.round(Math.min(1, Math.max(0, fraction)) * Math.max(0, count - 1));
  }

  function onPointerMove(event: PointerEvent<SVGRectElement>): void {
    setCursor(indexFromPointer(event.clientX, event.currentTarget));
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (count === 0) {
      return;
    }
    const current = active ?? count - 1;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setCursor(Math.max(0, current - 1));
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setCursor(Math.min(count - 1, current + 1));
    } else if (event.key === "Home") {
      event.preventDefault();
      setCursor(0);
    } else if (event.key === "End" || event.key === "Escape") {
      event.preventDefault();
      setCursor(event.key === "End" ? count - 1 : null);
    }
  }

  if (count < 2) {
    return (
      <div ref={ref} className="flex h-[220px] items-center justify-center">
        <p role="status" className="text-muted-foreground text-sm">
          Collecting samples. The chart fills in over the next few seconds.
        </p>
      </div>
    );
  }

  const endLabels = SERIES.map((series) => {
    const latestValue = latest ? series.read(latest) : 0;
    return { series, y: yOf(latestValue), text: formatRate(latestValue) };
  });
  // End labels that would overlap are pushed apart. The labels are sorted by height,
  // and they are mutated in place, so the list above reflects the spacing.
  const byHeight = [...endLabels].sort((a, b) => a.y - b.y);
  for (let index = 1; index < byHeight.length; index += 1) {
    const previous = byHeight[index - 1];
    const current = byHeight[index];
    if (previous && current && current.y - previous.y < 16) {
      current.y = previous.y + 16;
    }
  }

  const lastX = xOf(count - 1);
  const activeSample = active !== null ? samples[active] : undefined;
  const tooltipLeft = active !== null ? Math.min(xOf(active) + 12, width - 180) : 0;

  return (
    <div className="flex flex-col gap-3">
      <ul aria-label="Series" className="text-foreground flex flex-wrap gap-x-5 gap-y-1 text-sm">
        {SERIES.map((series) => (
          <li key={series.key} className="flex items-center gap-2">
            <span aria-hidden="true" className={cn("h-0.5 w-4 rounded-full", series.swatch)} />
            <span>{series.label}</span>
          </li>
        ))}
      </ul>

      <div
        ref={ref}
        role="group"
        tabIndex={0}
        aria-label="Throughput chart. Use the left and right arrow keys to read each sample."
        onKeyDown={onKeyDown}
        onBlur={() => {
          setCursor(null);
        }}
        className="focus-visible:outline-ring relative rounded-md focus-visible:outline-2 focus-visible:outline-offset-2"
      >
        <svg width={width} height={HEIGHT} viewBox={`0 0 ${width} ${HEIGHT}`} aria-hidden="true" focusable="false" className="block">
          {ticks.map((tick) => (
            <g key={tick}>
              <line
                x1={MARGIN.left}
                x2={MARGIN.left + plotWidth}
                y1={yOf(tick)}
                y2={yOf(tick)}
                strokeWidth={1}
                className="stroke-border"
              />
              <text x={MARGIN.left - 8} y={yOf(tick) + 4} textAnchor="end" className="fill-muted-foreground text-[11px] tabular-nums">
                {formatBytes(tick)}
              </text>
            </g>
          ))}
          <text x={MARGIN.left} y={HEIGHT - 8} className="fill-muted-foreground text-[11px]">
            {clock(samples[0]?.at ?? 0)}
          </text>
          <text x={MARGIN.left + plotWidth} y={HEIGHT - 8} textAnchor="end" className="fill-muted-foreground text-[11px]">
            Now
          </text>

          {SERIES.map((series) => (
            <path
              key={series.key}
              d={polylinePath(samples.map((sample, index) => ({ x: xOf(index), y: yOf(series.read(sample)) })))}
              fill="none"
              strokeWidth={2}
              strokeLinecap="round"
              strokeLinejoin="round"
              className={series.stroke}
            />
          ))}

          {active !== null ? (
            <line
              x1={xOf(active)}
              x2={xOf(active)}
              y1={MARGIN.top}
              y2={MARGIN.top + plotHeight}
              strokeWidth={1}
              className="stroke-muted-foreground"
            />
          ) : null}

          {SERIES.map((series) => {
            const point = { x: lastX, y: yOf(latest ? series.read(latest) : 0) };
            return (
              <circle key={`end-${series.key}`} cx={point.x} cy={point.y} r={4} strokeWidth={2} className={cn("stroke-card", series.stroke.replace("stroke-", "fill-"))} />
            );
          })}
          {active !== null
            ? SERIES.map((series) => {
                const sample = samples[active];
                if (!sample) {
                  return null;
                }
                return (
                  <circle
                    key={`hover-${series.key}`}
                    cx={xOf(active)}
                    cy={yOf(series.read(sample))}
                    r={4.5}
                    strokeWidth={2}
                    className={cn("stroke-card", series.stroke.replace("stroke-", "fill-"))}
                  />
                );
              })
            : null}

          {endLabels.map((label) => (
            <text
              key={`label-${label.series.key}`}
              x={lastX + 10}
              y={label.y}
              dominantBaseline="middle"
              className="fill-foreground text-xs font-medium tabular-nums"
            >
              {label.text}
            </text>
          ))}

          <rect
            x={MARGIN.left}
            y={MARGIN.top}
            width={plotWidth}
            height={plotHeight}
            fill="transparent"
            onPointerMove={onPointerMove}
            onPointerLeave={() => {
              setCursor(null);
            }}
          />
        </svg>

        {activeSample ? (
          <div
            aria-live="polite"
            className="bg-card text-card-foreground border-border pointer-events-none absolute z-10 w-44 rounded-md border p-2 text-xs shadow-md"
            style={{ left: tooltipLeft, top: 4 }}
          >
            <p className="text-muted-foreground mb-1 tabular-nums">{clock(activeSample.at)}</p>
            <ul className="flex flex-col gap-1">
              {SERIES.map((series) => (
                <li key={series.key} className="flex items-center gap-2">
                  <span aria-hidden="true" className={cn("h-0.5 w-3 rounded-full", series.swatch)} />
                  <span className="text-foreground font-semibold tabular-nums">{formatRate(series.read(activeSample))}</span>
                  <span className="text-muted-foreground truncate">{series.key === "bytesIn" ? "in" : "out"}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>

      <details className="text-sm">
        <summary className="text-primary cursor-pointer font-medium underline-offset-4 hover:underline">
          Show the samples as a table
        </summary>
        <div className="mt-2 max-h-64 overflow-auto">
          <table className="w-full text-left text-xs">
            <caption className="sr-only">Throughput samples, newest first</caption>
            <thead className="text-muted-foreground">
              <tr>
                <th scope="col" className="py-1 pr-3 font-medium">Time</th>
                <th scope="col" className="py-1 pr-3 text-right font-medium">Bytes in per second</th>
                <th scope="col" className="py-1 pr-3 text-right font-medium">Bytes out per second</th>
              </tr>
            </thead>
            <tbody>
              {[...samples].reverse().slice(0, 30).map((sample) => (
                <tr key={sample.at} className="border-border border-t">
                  <td className="py-1 pr-3 tabular-nums">{clock(sample.at)}</td>
                  <td className="py-1 pr-3 text-right tabular-nums">{formatRate(sample.bytesInPerSecond)}</td>
                  <td className="py-1 pr-3 text-right tabular-nums">{formatRate(sample.bytesOutPerSecond)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
