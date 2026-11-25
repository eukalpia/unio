import type { ClusterInfo, PolicyDocument } from "../lib/api/types";

// In-memory fixture state for the mock server (development and tests only).
// Its shapes follow the Admin API contract, so the UI can be built and
// exercised without a running node.

export const MOCK_ACCESS_KEY = "UNIOROOTDEV0000000001";
/** A development-only secret. It is never used by a running node. */
export const MOCK_SECRET_KEY = "dev-only-secret-for-mock-server";
export const MOCK_LOGIN_LIMIT = 10;
/** Sent as Retry-After with the mock 429 response, as the node does. */
export const MOCK_LOGIN_RETRY_AFTER_SECONDS = 60;
const LOGIN_WINDOW_MS = 60_000;
const SESSION_MS = 12 * 60 * 60 * 1000;

export interface MockObject {
  key: string;
  size: number;
  etag: string;
  lastModified: string;
}

export interface MockBucket {
  name: string;
  created: string;
  versioning: string;
  objects: Map<string, MockObject>;
}

export interface MockAccessKey {
  id: string;
  created: string;
}

export interface MockUser {
  name: string;
  enabled: boolean;
  policies: string[];
  accessKeys: MockAccessKey[];
}

export interface MockState {
  session: { accessKey: string; csrf: string; expiresAt: string } | null;
  loginAttempts: number[];
  buckets: Map<string, MockBucket>;
  users: Map<string, MockUser>;
  policies: Map<string, PolicyDocument>;
  cluster: ClusterInfo;
  requests: number;
}

const TIB = 1024 ** 4;

function buildCluster(): ClusterInfo {
  const nodes = ["unio-0", "unio-1", "unio-2", "unio-3"].map((id, index) => ({
    id,
    address: `${id}.unio-hl.local:9100`,
    s3Address: `${id}.unio-hl.local:9000`,
    healthy: index !== 9,
  }));
  const drives = nodes.flatMap((node) =>
    [1, 2, 3, 4].map((disk) => {
      const total = 2 * TIB;
      const used = Math.round(total * (0.31 + ((disk * 7 + node.id.length) % 9) / 40));
      return {
        id: `${node.id}-d${disk}`,
        node: node.id,
        path: `/data${disk}`,
        online: !(node.id === "unio-2" && disk === 4),
        total,
        free: total - used,
        used,
      };
    }),
  );
  const sets = [0, 1].map((index) => ({
    id: `set-${index}`,
    dataShards: 6,
    parityShards: 2,
    drives: drives.slice(index * 8, index * 8 + 8),
  }));
  const total = drives.reduce((sum, drive) => sum + drive.total, 0);
  const used = drives.reduce((sum, drive) => sum + drive.used, 0);
  return {
    clusterId: "5f0c2a7e9b3d41c8a6e1f0d2b7c4a913",
    nodes,
    sets,
    capacity: { total, used, free: total - used },
  };
}

function makeObjects(prefix: string, count: number, startedAt: number): Map<string, MockObject> {
  const objects = new Map<string, MockObject>();
  for (let index = 0; index < count; index += 1) {
    const key = `${prefix}${String(index).padStart(5, "0")}.log`;
    objects.set(key, {
      key,
      size: 512 + ((index * 7919) % 90_000),
      etag: `"${(index * 2654435761 + 97).toString(16).padStart(8, "0")}"`,
      lastModified: new Date(startedAt - index * 60_000).toISOString(),
    });
  }
  return objects;
}

function buildState(): MockState {
  const now = Date.now();
  const sample = (key: string, size: number, minutes: number): MockObject => ({
    key,
    size,
    etag: `"${key.length.toString(16)}a9f${size.toString(16)}"`,
    lastModified: new Date(now - minutes * 60_000).toISOString(),
  });
  const photos = new Map<string, MockObject>([
    ["photos/2026/cover.jpg", sample("photos/2026/cover.jpg", 2_480_113, 90)],
    ["photos/2026/summit.png", sample("photos/2026/summit.png", 6_120_004, 40)],
    ["photos/index.html", sample("photos/index.html", 4_096, 5)],
  ]);
  const logs = makeObjects("logs/2026/10/app-", 2_500, now);
  const root = new Map<string, MockObject>([
    ["README.txt", sample("README.txt", 1_024, 1_440)],
    ["release-notes.md", sample("release-notes.md", 18_311, 300)],
  ]);

  const buckets = new Map<string, MockBucket>([
    ["backups", { name: "backups", created: new Date(now - 20 * 86_400_000).toISOString(), versioning: "Enabled", objects: new Map([...root]) }],
    ["media", { name: "media", created: new Date(now - 9 * 86_400_000).toISOString(), versioning: "", objects: photos }],
    ["logs", { name: "logs", created: new Date(now - 2 * 86_400_000).toISOString(), versioning: "Suspended", objects: logs }],
  ]);

  const users = new Map<string, MockUser>([
    ["root", { name: "root", enabled: true, policies: ["AdminAll"], accessKeys: [{ id: MOCK_ACCESS_KEY, created: new Date(now - 90 * 86_400_000).toISOString() }] }],
    ["alice", { name: "alice", enabled: true, policies: ["ReadWriteMedia"], accessKeys: [{ id: "UNIOALICE0000000000A", created: new Date(now - 12 * 86_400_000).toISOString() }] }],
    ["ci-bot", { name: "ci-bot", enabled: true, policies: [], accessKeys: [] }],
    ["bob", { name: "bob", enabled: false, policies: ["ReadOnly"], accessKeys: [] }],
  ]);

  const policies = new Map<string, PolicyDocument>([
    ["AdminAll", { Version: "2026-01-01", Statement: [{ Effect: "Allow", Action: ["*"], Resource: ["*"] }] }],
    ["ReadOnly", { Version: "2026-01-01", Statement: [{ Effect: "Allow", Action: ["s3:GetObject", "s3:ListBucket"], Resource: ["arn:unio:s3:::*"] }] }],
    ["ReadWriteMedia", { Version: "2026-01-01", Statement: [{ Effect: "Allow", Action: ["s3:*"], Resource: ["arn:unio:s3:::media", "arn:unio:s3:::media/*"] }] }],
  ]);

  return { session: null, loginAttempts: [], buckets, users, policies, cluster: buildCluster(), requests: 0 };
}

let state: MockState = buildState();

export function mockState(): MockState {
  return state;
}

/** Restores the fixtures. Tests call this between cases. */
export function resetMockState(): void {
  state = buildState();
}

/** Returns true when the login attempt is inside the per-IP budget. */
export function takeLoginAttempt(now: number): boolean {
  state.loginAttempts = state.loginAttempts.filter((at) => now - at < LOGIN_WINDOW_MS);
  if (state.loginAttempts.length >= MOCK_LOGIN_LIMIT) {
    return false;
  }
  state.loginAttempts.push(now);
  return true;
}

export function openSession(now: number): { accessKey: string; csrf: string; expiresAt: string } {
  const session = {
    accessKey: MOCK_ACCESS_KEY,
    csrf: `csrf-${now.toString(36)}-${Math.random().toString(36).slice(2, 10)}`,
    expiresAt: new Date(now + SESSION_MS).toISOString(),
  };
  state.session = session;
  return session;
}

export function sessionOpen(now: number): boolean {
  return state.session !== null && Date.parse(state.session.expiresAt) > now;
}

/** Mock values for the metrics summary. They vary slowly with time, so charts move. */
export function metricsAt(now: number) {
  const wave = (period: number, amplitude: number, base: number) =>
    base + amplitude * Math.sin((now / period) * Math.PI * 2);
  return {
    requestsPerSecond: Math.max(0, wave(40_000, 180, 420)),
    errorsPerSecond: Math.max(0, wave(90_000, 1.5, 2)),
    bytesInPerSecond: Math.max(0, wave(55_000, 9_000_000, 22_000_000)),
    bytesOutPerSecond: Math.max(0, wave(65_000, 14_000_000, 46_000_000)),
    p99Millis: Math.max(1, wave(30_000, 4, 18)),
    bufferBytesInUse: 33_554_432 + Math.round(wave(20_000, 4_000_000, 0)),
    deletionsPending: 3,
  };
}
