import { describe, expect, it } from "vitest";
import type { ClusterInfo, Drive } from "../../lib/api/types";
import { buildAlerts, summarizeHealth } from "./health";

function drive(id: string, node: string, online: boolean): Drive {
  return { id, node, path: `/data-${id}`, online, total: 100, free: 40, used: 60 };
}

function cluster(overrides: Partial<ClusterInfo> = {}): ClusterInfo {
  return {
    clusterId: "c1",
    nodes: [
      { id: "n1", address: "n1:9100", s3Address: "n1:9000", healthy: true },
      { id: "n2", address: "n2:9100", s3Address: "n2:9000", healthy: false },
    ],
    sets: [
      { id: "s1", dataShards: 2, parityShards: 1, drives: [drive("a", "n1", true), drive("b", "n2", false)] },
    ],
    capacity: { total: 200, used: 120, free: 80 },
    ...overrides,
  };
}

describe("cluster health", () => {
  it("counts healthy nodes and online drives", () => {
    expect(summarizeHealth(cluster())).toMatchObject({
      nodesHealthy: 1,
      nodesTotal: 2,
      drivesOnline: 1,
      drivesTotal: 2,
    });
  });

  it("raises a critical alert for each unhealthy node and offline drive", () => {
    const alerts = buildAlerts(cluster());
    expect(alerts.map((alert) => alert.id)).toEqual(["node-n2", "drive-b"]);
    expect(alerts.every((alert) => alert.severity === "critical")).toBe(true);
  });

  it("warns when capacity passes 80 percent and is critical from 90", () => {
    const warning = buildAlerts(cluster({ nodes: [], sets: [], capacity: { total: 100, used: 85, free: 15 } }));
    expect(warning).toEqual([expect.objectContaining({ id: "capacity", severity: "warning" })]);

    const critical = buildAlerts(cluster({ nodes: [], sets: [], capacity: { total: 100, used: 95, free: 5 } }));
    expect(critical).toEqual([expect.objectContaining({ id: "capacity", severity: "critical" })]);
  });

  it("reports no alerts for a healthy cluster", () => {
    const healthy = cluster({
      nodes: [{ id: "n1", address: "n1:9100", s3Address: "n1:9000", healthy: true }],
      sets: [{ id: "s1", dataShards: 2, parityShards: 1, drives: [drive("a", "n1", true)] }],
      capacity: { total: 100, used: 10, free: 90 },
    });
    expect(buildAlerts(healthy)).toEqual([]);
  });
});
