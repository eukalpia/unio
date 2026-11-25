import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, HardDrive, Server } from "lucide-react";
import { useMemo } from "react";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import { formatBytes, formatClock, formatNumber, percentOf } from "../../lib/format";
import { recordMetrics, type MetricsSample } from "../../lib/metrics-history";
import { queryKeys } from "../../lib/query";
import { Sparkline } from "../../components/charts/sparkline";
import { ThroughputChart } from "../../components/charts/throughput-chart";
import { PageHeader } from "../../components/chrome";
import { Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Alert, Meter, Skeleton } from "../../components/ui/feedback";
import { Button } from "../../components/ui/button";
import { buildAlerts, CAPACITY_CRITICAL_PERCENT, CAPACITY_WARNING_PERCENT, summarizeHealth } from "./health";
import type { ClusterInfo, MetricsSummary } from "../../lib/api/types";
import { cn } from "../../lib/utils";

const METRICS_INTERVAL_MS = 5000;
const CLUSTER_INTERVAL_MS = 10_000;

interface MetricsData {
  summary: MetricsSummary;
  history: readonly MetricsSample[];
}

function StatTile({
  label,
  value,
  trend,
}: {
  label: string;
  value: string;
  trend?: readonly number[];
}) {
  return (
    <Card className="gap-2 p-4">
      <p className="text-muted-foreground text-sm">{label}</p>
      <p className="text-2xl font-semibold">{value}</p>
      {trend ? <Sparkline values={trend} /> : null}
    </Card>
  );
}

export function DashboardPage() {
  const metrics = useQuery<MetricsData>({
    queryKey: queryKeys.metrics,
    queryFn: async ({ signal }) => {
      const summary = await api.metricsSummary(signal);
      return { summary, history: recordMetrics(summary, Date.now()) };
    },
    refetchInterval: METRICS_INTERVAL_MS,
  });

  const cluster = useQuery<ClusterInfo>({
    queryKey: queryKeys.cluster,
    queryFn: ({ signal }) => api.cluster(signal),
    refetchInterval: CLUSTER_INTERVAL_MS,
  });

  const health = useMemo(() => (cluster.data ? summarizeHealth(cluster.data) : null), [cluster.data]);
  const alerts = useMemo(() => (cluster.data ? buildAlerts(cluster.data) : []), [cluster.data]);
  const history = metrics.data?.history ?? [];
  const summary = metrics.data?.summary;
  const usedPercent = cluster.data ? percentOf(cluster.data.capacity.used, cluster.data.capacity.total) : 0;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Dashboard"
        description={
          cluster.data ? (
            <span>
              Cluster <span className="font-mono">{cluster.data.clusterId.slice(0, 12)}</span>
              {metrics.dataUpdatedAt > 0 ? <> · updated {formatClock(metrics.dataUpdatedAt)}</> : null}
            </span>
          ) : (
            "Live status of the cluster"
          )
        }
      />

      {metrics.isError && !summary ? (
        <Alert tone="danger" title="Metrics are unavailable">
          <span>{describeError(metrics.error)}</span>
          <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => void metrics.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : null}

      <section aria-label="Live request metrics" className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {summary ? (
          <>
            <StatTile
              label="Requests per second"
              value={formatNumber(summary.requestsPerSecond)}
              trend={history.map((sample) => sample.requestsPerSecond)}
            />
            <StatTile
              label="Errors per second"
              value={formatNumber(summary.errorsPerSecond)}
              trend={history.map((sample) => sample.errorsPerSecond)}
            />
            <StatTile
              label="p99 latency"
              value={`${formatNumber(summary.p99Millis)} ms`}
              trend={history.map((sample) => sample.p99Millis)}
            />
            <StatTile label="Deletions pending" value={formatNumber(summary.deletionsPending)} />
          </>
        ) : (
          Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-28" />)
        )}
      </section>

      <div className="grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle>Throughput over the last ten minutes</CardTitle>
            <CardDescription>Bytes moved per second, refreshed every five seconds.</CardDescription>
          </CardHeader>
          {metrics.data ? <ThroughputChart samples={history} /> : <Skeleton className="h-[220px]" />}
        </Card>

        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader>
              <CardTitle>Capacity</CardTitle>
              <CardDescription>Space used across all drives.</CardDescription>
            </CardHeader>
            {cluster.data ? (
              <>
                <Meter
                  value={cluster.data.capacity.used}
                  max={cluster.data.capacity.total}
                  label="Used capacity"
                  tone={usedPercent >= CAPACITY_CRITICAL_PERCENT ? "danger" : usedPercent >= CAPACITY_WARNING_PERCENT ? "warning" : "brand"}
                />
                <dl className="grid grid-cols-3 gap-2 text-sm">
                  <div>
                    <dt className="text-muted-foreground">Used</dt>
                    <dd className="font-semibold tabular-nums">{formatBytes(cluster.data.capacity.used)}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Free</dt>
                    <dd className="font-semibold tabular-nums">{formatBytes(cluster.data.capacity.free)}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Total</dt>
                    <dd className="font-semibold tabular-nums">{formatBytes(cluster.data.capacity.total)}</dd>
                  </div>
                </dl>
              </>
            ) : (
              <Skeleton className="h-16" />
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Health</CardTitle>
              <CardDescription>Nodes and drives reporting in.</CardDescription>
            </CardHeader>
            {health ? (
              <ul className="flex flex-col gap-3 text-sm">
                <HealthRow
                  icon={Server}
                  label="Nodes healthy"
                  ok={health.nodesHealthy}
                  total={health.nodesTotal}
                  problem={health.unhealthyNodes.length > 0}
                />
                <HealthRow
                  icon={HardDrive}
                  label="Drives online"
                  ok={health.drivesOnline}
                  total={health.drivesTotal}
                  problem={health.offlineDrives.length > 0}
                />
              </ul>
            ) : (
              <Skeleton className="h-16" />
            )}
          </Card>
        </div>

        <Card className="xl:col-span-3">
          <CardHeader>
            <CardTitle>Alerts</CardTitle>
            <CardDescription>Offline drives, unhealthy nodes and capacity warnings.</CardDescription>
          </CardHeader>
          {cluster.isError && !cluster.data ? (
            <Alert tone="danger" title="Cluster state is unavailable">
              {describeError(cluster.error)}
            </Alert>
          ) : cluster.data === undefined ? (
            <Skeleton className="h-12" />
          ) : alerts.length === 0 ? (
            <p className="text-success flex items-center gap-2 text-sm font-medium">
              <CheckCircle2 className="size-4" aria-hidden="true" />
              No active alerts. Every node and drive is healthy.
            </p>
          ) : (
            <ul className="flex flex-col gap-2" aria-label="Active alerts">
              {alerts.map((alert) => (
                <li
                  key={alert.id}
                  className={cn(
                    "flex items-start gap-3 rounded-md border p-3 text-sm",
                    alert.severity === "critical" ? "border-destructive/50 bg-destructive/5" : "border-warning/50 bg-warning/5",
                  )}
                >
                  <AlertTriangle
                    className={cn("mt-0.5 size-4 shrink-0", alert.severity === "critical" ? "text-destructive" : "text-warning")}
                    aria-hidden="true"
                  />
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <p className="font-medium">
                      <span className="sr-only">{alert.severity === "critical" ? "Critical: " : "Warning: "}</span>
                      {alert.title}
                    </p>
                    <p className="text-muted-foreground">{alert.detail}</p>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}

function HealthRow({
  icon: Icon,
  label,
  ok,
  total,
  problem,
}: {
  icon: typeof Server;
  label: string;
  ok: number;
  total: number;
  problem: boolean;
}) {
  return (
    <li className="flex items-center justify-between gap-3">
      <span className="flex items-center gap-2">
        <Icon className="text-muted-foreground size-4" aria-hidden="true" />
        {label}
      </span>
      <span className={cn("flex items-center gap-1.5 font-semibold tabular-nums", problem ? "text-destructive" : "text-success")}>
        {problem ? <AlertTriangle className="size-4" aria-hidden="true" /> : <CheckCircle2 className="size-4" aria-hidden="true" />}
        {ok} of {total}
        <span className="sr-only">{problem ? " (attention needed)" : " (all healthy)"}</span>
      </span>
    </li>
  );
}
