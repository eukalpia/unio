import type { ClusterInfo, Drive, ClusterNode } from "../../lib/api/types";
import { percentOf } from "../../lib/format";

export const CAPACITY_WARNING_PERCENT = 80;
export const CAPACITY_CRITICAL_PERCENT = 90;

export interface HealthSummary {
  nodesHealthy: number;
  nodesTotal: number;
  drivesOnline: number;
  drivesTotal: number;
  offlineDrives: Drive[];
  unhealthyNodes: ClusterNode[];
}

export interface Alert {
  id: string;
  severity: "critical" | "warning";
  title: string;
  detail: string;
}

export function summarizeHealth(cluster: ClusterInfo): HealthSummary {
  const drives = cluster.sets.flatMap((set) => set.drives);
  const offlineDrives = drives.filter((drive) => !drive.online);
  const unhealthyNodes = cluster.nodes.filter((node) => !node.healthy);
  return {
    nodesHealthy: cluster.nodes.length - unhealthyNodes.length,
    nodesTotal: cluster.nodes.length,
    drivesOnline: drives.length - offlineDrives.length,
    drivesTotal: drives.length,
    offlineDrives,
    unhealthyNodes,
  };
}

/** Builds the alert list from cluster state. Offline drives and unhealthy nodes are critical. */
export function buildAlerts(cluster: ClusterInfo): Alert[] {
  const health = summarizeHealth(cluster);
  const alerts: Alert[] = [];
  for (const node of health.unhealthyNodes) {
    alerts.push({
      id: `node-${node.id}`,
      severity: "critical",
      title: `Node ${node.id} is unhealthy`,
      detail: `Its internode address is ${node.address}.`,
    });
  }
  for (const drive of health.offlineDrives) {
    alerts.push({
      id: `drive-${drive.id}`,
      severity: "critical",
      title: `Drive ${drive.path} on ${drive.node} is offline`,
      detail: "Its erasure set has less redundancy until the drive returns.",
    });
  }
  const used = percentOf(cluster.capacity.used, cluster.capacity.total);
  if (used >= CAPACITY_WARNING_PERCENT) {
    alerts.push({
      id: "capacity",
      severity: used >= CAPACITY_CRITICAL_PERCENT ? "critical" : "warning",
      title: `Capacity is ${used.toFixed(0)} percent used`,
      detail: "Add drives or delete data before writes start to fail.",
    });
  }
  return alerts;
}
