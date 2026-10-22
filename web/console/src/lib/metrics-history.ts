import type { MetricsSummary } from "./api/types";

/** One poll of the metrics summary, stamped with the time it arrived. */
export interface MetricsSample {
  at: number;
  bytesInPerSecond: number;
  bytesOutPerSecond: number;
  requestsPerSecond: number;
  errorsPerSecond: number;
  p99Millis: number;
}

/** 120 samples at a 5 second poll interval is ten minutes of history. */
export const MAX_SAMPLES = 120;

let samples: readonly MetricsSample[] = [];

/** Adds a poll result and returns the history, oldest first. */
export function recordMetrics(summary: MetricsSummary, at: number): readonly MetricsSample[] {
  const sample: MetricsSample = {
    at,
    bytesInPerSecond: summary.bytesInPerSecond,
    bytesOutPerSecond: summary.bytesOutPerSecond,
    requestsPerSecond: summary.requestsPerSecond,
    errorsPerSecond: summary.errorsPerSecond,
    p99Millis: summary.p99Millis,
  };
  samples = [...samples, sample].slice(-MAX_SAMPLES);
  return samples;
}

export function metricsHistory(): readonly MetricsSample[] {
  return samples;
}

/** Forgets the history, for example when the session ends. */
export function resetMetricsHistory(): void {
  samples = [];
}
