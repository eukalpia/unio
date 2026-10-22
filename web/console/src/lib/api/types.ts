// Response and request shapes of the Admin API. These follow the contract in
// .local/wave2-contract.md (section "Admin API"). Identifiers are strings and
// sizes are bytes; timestamps are RFC 3339 strings.

/**
 * GET session. `csrf` is present when the request was authenticated by the session
 * cookie, which is the console's case, so a reload can resume without signing in again.
 */
export interface SessionInfo {
  accessKey: string;
  expiresAt: string | null;
  csrf: string | null;
}

/** POST session. Signing in always returns the CSRF value. */
export interface SessionCreated {
  accessKey: string;
  expiresAt: string;
  csrf: string;
}

export interface ClusterNode {
  id: string;
  address: string;
  s3Address: string;
  healthy: boolean;
}

export interface Drive {
  id: string;
  node: string;
  path: string;
  online: boolean;
  total: number;
  free: number;
  used: number;
}

export interface ErasureSet {
  id: string;
  dataShards: number;
  parityShards: number;
  drives: Drive[];
}

export interface Capacity {
  total: number;
  used: number;
  free: number;
}

export interface ClusterInfo {
  clusterId: string;
  nodes: ClusterNode[];
  sets: ErasureSet[];
  capacity: Capacity;
}

export interface MetricsSummary {
  requestsPerSecond: number;
  errorsPerSecond: number;
  bytesInPerSecond: number;
  bytesOutPerSecond: number;
  p99Millis: number;
  bufferBytesInUse: number;
  deletionsPending: number;
}

export interface Bucket {
  name: string;
  created: string;
  versioning: string;
}

export interface ObjectEntry {
  key: string;
  size: number;
  etag: string;
  lastModified: string;
}

export interface ObjectPage {
  prefixes: string[];
  objects: ObjectEntry[];
  nextToken: string | null;
}

export interface AccessKeyInfo {
  id: string;
  created: string;
}

export interface UserInfo {
  name: string;
  enabled: boolean;
  policies: string[];
  accessKeys: AccessKeyInfo[];
}

export interface CreatedAccessKey {
  accessKeyId: string;
  secretKey: string;
}

export type PolicyDocument = Record<string, unknown>;

export interface PolicyInfo {
  name: string;
  document: PolicyDocument;
}

export type PresignMethod = "GET" | "PUT";

export interface PresignResult {
  url: string;
}

export interface ObjectListQuery {
  prefix: string;
  delimiter?: string;
  token?: string;
  max?: number;
}
