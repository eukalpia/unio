import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, HardDrive, Server } from "lucide-react";
import { PageHeader } from "../../components/chrome";
import { Badge, Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { Alert, Meter, Skeleton } from "../../components/ui/feedback";
import { Button } from "../../components/ui/button";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { ClusterInfo, Drive } from "../../lib/api/types";
import { formatBytes, percentOf } from "../../lib/format";
import { queryKeys } from "../../lib/query";
import { summarizeHealth } from "../dashboard/health";

const REFRESH_MS = 10_000;

export function ClusterPage() {
  const cluster = useQuery<ClusterInfo>({
    queryKey: queryKeys.cluster,
    queryFn: ({ signal }) => api.cluster(signal),
    refetchInterval: REFRESH_MS,
  });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Cluster"
        description="Nodes, erasure sets and the state of every drive."
        actions={
          cluster.data ? (
            <span className="text-muted-foreground font-mono text-xs break-all">{cluster.data.clusterId}</span>
          ) : null
        }
      />

      {cluster.isError && !cluster.data ? (
        <Alert tone="danger" title="The cluster could not be loaded">
          <span>{describeError(cluster.error)}</span>
          <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => void cluster.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : null}

      {cluster.isPending ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          <Skeleton className="h-24" />
          <Skeleton className="h-64" />
        </div>
      ) : null}

      {cluster.data ? <ClusterDetails cluster={cluster.data} /> : null}
    </div>
  );
}

function ClusterDetails({ cluster }: { cluster: ClusterInfo }) {
  const health = summarizeHealth(cluster);
  const usedPercent = percentOf(cluster.capacity.used, cluster.capacity.total);
  return (
    <>
      <Card className="gap-4">
        <CardHeader>
          <CardTitle>Capacity</CardTitle>
          <CardDescription>
            {health.nodesHealthy} of {health.nodesTotal} nodes healthy · {health.drivesOnline} of {health.drivesTotal} drives online
          </CardDescription>
        </CardHeader>
        <Meter
          value={cluster.capacity.used}
          max={cluster.capacity.total}
          label="Cluster capacity used"
          tone={usedPercent >= 90 ? "danger" : usedPercent >= 80 ? "warning" : "brand"}
        />
        <dl className="grid grid-cols-3 gap-3 text-sm">
          <div>
            <dt className="text-muted-foreground">Used</dt>
            <dd className="font-semibold tabular-nums">{formatBytes(cluster.capacity.used)}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Free</dt>
            <dd className="font-semibold tabular-nums">{formatBytes(cluster.capacity.free)}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Total</dt>
            <dd className="font-semibold tabular-nums">{formatBytes(cluster.capacity.total)}</dd>
          </div>
        </dl>
      </Card>

      <section aria-labelledby="nodes-heading" className="flex flex-col gap-3">
        <h2 id="nodes-heading" className="font-display text-lg font-semibold">Nodes</h2>
        <Card className="overflow-hidden p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[30rem] text-left text-sm">
              <caption className="sr-only">Nodes and their health</caption>
              <thead className="bg-muted/60 text-muted-foreground text-xs">
                <tr>
                  <th scope="col" className="px-4 py-2.5 font-medium">Node</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">Internode address</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">S3 address</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">Health</th>
                </tr>
              </thead>
              <tbody>
                {cluster.nodes.map((node) => (
                  <tr key={node.id} className="border-border border-t">
                    <th scope="row" className="px-4 py-3 text-left font-medium">
                      <span className="flex items-center gap-2">
                        <Server className="text-muted-foreground size-4" aria-hidden="true" />
                        {node.id}
                      </span>
                    </th>
                    <td className="px-4 py-3 font-mono text-xs break-all">{node.address}</td>
                    <td className="px-4 py-3 font-mono text-xs break-all">{node.s3Address}</td>
                    <td className="px-4 py-3">
                      <StatusBadge ok={node.healthy} okLabel="Healthy" badLabel="Unhealthy" />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      </section>

      <section aria-labelledby="sets-heading" className="flex flex-col gap-3">
        <h2 id="sets-heading" className="font-display text-lg font-semibold">Erasure sets</h2>
        <div className="grid gap-4 xl:grid-cols-2">
          {cluster.sets.map((set) => {
            const online = set.drives.filter((drive) => drive.online).length;
            return (
              <Card key={set.id} className="gap-4">
                <CardHeader>
                  <CardTitle className="flex flex-wrap items-center gap-2">
                    Set {set.id}
                    <Badge tone="brand">
                      {set.dataShards} data + {set.parityShards} parity
                    </Badge>
                  </CardTitle>
                  <CardDescription>
                    {online} of {set.drives.length} drives online
                  </CardDescription>
                </CardHeader>
                <ul className="grid gap-2 sm:grid-cols-2">
                  {set.drives.map((drive) => (
                    <DriveTile key={drive.id} drive={drive} />
                  ))}
                </ul>
              </Card>
            );
          })}
        </div>
      </section>
    </>
  );
}

function DriveTile({ drive }: { drive: Drive }) {
  const used = percentOf(drive.used, drive.total);
  return (
    <li className="border-border bg-card flex flex-col gap-2 rounded-md border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-2 text-sm font-medium">
          <HardDrive className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
          <span className="truncate" title={`${drive.node}${drive.path}`}>
            {drive.node} · {drive.path}
          </span>
        </span>
        <StatusBadge ok={drive.online} okLabel="Online" badLabel="Offline" />
      </div>
      <Meter value={drive.used} max={drive.total} label={`Used space on ${drive.node} ${drive.path}`} tone={used >= 90 ? "danger" : used >= 80 ? "warning" : "brand"} />
      <p className="text-muted-foreground text-xs tabular-nums">
        {formatBytes(drive.free)} free of {formatBytes(drive.total)}
      </p>
    </li>
  );
}

/** Status is shown with an icon and a word, never by colour alone. */
function StatusBadge({ ok, okLabel, badLabel }: { ok: boolean; okLabel: string; badLabel: string }) {
  return ok ? (
    <Badge tone="success">
      <CheckCircle2 className="size-3" aria-hidden="true" />
      {okLabel}
    </Badge>
  ) : (
    <Badge tone="danger">
      <AlertTriangle className="size-3" aria-hidden="true" />
      {badLabel}
    </Badge>
  );
}
