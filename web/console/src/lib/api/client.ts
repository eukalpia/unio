import { ApiError, ReauthRequiredError, isAbortError } from "./errors";
import type {
  Bucket,
  ClusterInfo,
  CreatedAccessKey,
  MetricsSummary,
  ObjectListQuery,
  ObjectPage,
  PolicyDocument,
  PolicyInfo,
  PresignMethod,
  PresignResult,
  SessionCreated,
  SessionInfo,
  UserInfo,
} from "./types";

export const API_BASE = "/unio/admin/v1/";
/** The server rejects request bodies above 64 KiB. Check before sending. */
export const MAX_BODY_BYTES = 64 * 1024;
export const CSRF_HEADER = "X-Unio-CSRF";

type Method = "GET" | "POST" | "PUT" | "DELETE";
type Query = Record<string, string | number | undefined>;

interface RequestOptions {
  query?: Query;
  body?: unknown;
  signal?: AbortSignal;
  /**
   * Session endpoints set this to false. They are not authenticated by the
   * cookie, so they carry no CSRF value, and a 401 does not end the session.
   */
  session?: boolean;
}

export interface ApiClientOptions {
  fetch?: typeof globalThis.fetch;
  /** Returns the CSRF value held in memory, or null when none is held. */
  getCsrf: () => string | null;
  /** Called when a session-bound request gets 401, so the app can sign out. */
  onUnauthorized?: () => void;
}

/** Encodes each path segment, so names and keys cannot change the route. */
export function pathOf(...segments: string[]): string {
  return segments.map((segment) => encodeURIComponent(segment)).join("/");
}

/** Resolves against the page origin, so the same code runs in browsers and in Node tests. */
function buildUrl(path: string, query: Query | undefined): string {
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(query ?? {})) {
    if (value !== undefined) {
      params.set(name, String(value));
    }
  }
  const search = params.toString();
  const base = typeof location === "undefined" ? "http://localhost/" : location.href;
  return new URL(search === "" ? `${API_BASE}${path}` : `${API_BASE}${path}?${search}`, base).href;
}

const MAX_ERROR_BYTES = 4096;

/** Reads Retry-After as delay-seconds or an HTTP date. Returns undefined when it is missing or unusable. */
export function parseRetryAfter(value: string | null, now: number = Date.now()): number | undefined {
  if (value === null) {
    return undefined;
  }
  const trimmed = value.trim();
  if (/^\d{1,6}$/.test(trimmed)) {
    return Number(trimmed);
  }
  const date = Date.parse(trimmed);
  if (Number.isNaN(date)) {
    return undefined;
  }
  return Math.max(0, Math.ceil((date - now) / 1000));
}

async function readError(response: Response): Promise<ApiError> {
  const retryAfter = parseRetryAfter(response.headers.get("Retry-After"));
  const fallback = new ApiError(
    response.status,
    fallbackCode(response.status),
    fallbackMessage(response.status),
    undefined,
    retryAfter,
  );
  let text: string;
  try {
    text = await response.text();
  } catch {
    return fallback;
  }
  if (text.length > MAX_ERROR_BYTES) {
    return fallback;
  }
  try {
    const body: unknown = JSON.parse(text);
    if (typeof body !== "object" || body === null) {
      return fallback;
    }
    const { code, message, requestId } = body as Record<string, unknown>;
    if (typeof code !== "string" || typeof message !== "string") {
      return fallback;
    }
    return new ApiError(
      response.status,
      code,
      message,
      typeof requestId === "string" ? requestId : undefined,
      retryAfter,
    );
  } catch {
    return fallback;
  }
}

function fallbackCode(status: number): string {
  if (status === 429) {
    return "SlowDown";
  }
  if (status === 501) {
    return "NotImplemented";
  }
  return "InternalError";
}

function fallbackMessage(status: number): string {
  if (status === 429) {
    return "Too many requests. Wait a moment and try again.";
  }
  if (status === 501) {
    return "This feature is not available on this node yet.";
  }
  return `Request failed with status ${status}.`;
}

export function createApiClient(options: ApiClientOptions) {
  // Looked up on each call, so a replaced global fetch (for example the test server) is used.
  const fetchImpl: typeof globalThis.fetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init));

  async function request<T>(method: Method, path: string, init: RequestOptions = {}): Promise<T> {
    const session = init.session !== false;
    const headers: Record<string, string> = { Accept: "application/json" };
    let body: string | undefined;

    if (init.body !== undefined) {
      body = JSON.stringify(init.body);
      if (new TextEncoder().encode(body).byteLength > MAX_BODY_BYTES) {
        throw new ApiError(413, "EntityTooLarge", "The request is too large.");
      }
      headers["Content-Type"] = "application/json";
    }

    if (session && method !== "GET") {
      const csrf = options.getCsrf();
      if (csrf === null) {
        // A mutation is never sent without CSRF. The caller asks the user to sign in again.
        throw new ReauthRequiredError();
      }
      headers[CSRF_HEADER] = csrf;
    }

    let response: Response;
    try {
      response = await fetchImpl(buildUrl(path, init.query), {
        method,
        headers,
        body,
        credentials: "same-origin",
        cache: "no-store",
        signal: init.signal,
      });
    } catch (error) {
      if (isAbortError(error)) {
        throw error;
      }
      throw new ApiError(0, "NetworkError", "Cannot reach the console server.");
    }

    if (response.status === 204) {
      return undefined as T;
    }
    if (!response.ok) {
      const error = await readError(response);
      if (response.status === 401 && session) {
        options.onUnauthorized?.();
      }
      throw error;
    }
    const contentType = response.headers.get("Content-Type") ?? "";
    if (!contentType.includes("application/json")) {
      throw new ApiError(response.status, "InternalError", "The server sent an unexpected response.");
    }
    return (await response.json()) as T;
  }

  return {
    // The secret key is passed once, sent in the request body, and never kept.
    login(accessKey: string, secretKey: string, signal?: AbortSignal): Promise<SessionCreated> {
      return request<SessionCreated>("POST", "session", {
        body: { accessKey, secretKey },
        session: false,
        signal,
      });
    },
    /** Resolves to null when there is no valid session cookie. */
    async getSession(signal?: AbortSignal): Promise<SessionInfo | null> {
      try {
        return await request<SessionInfo>("GET", "session", { session: false, signal });
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          return null;
        }
        throw error;
      }
    },
    logout(): Promise<void> {
      return request<undefined>("DELETE", "session");
    },

    cluster(signal?: AbortSignal): Promise<ClusterInfo> {
      return request<ClusterInfo>("GET", "cluster", { signal });
    },
    metricsSummary(signal?: AbortSignal): Promise<MetricsSummary> {
      return request<MetricsSummary>("GET", "metrics/summary", { signal });
    },

    listBuckets(signal?: AbortSignal): Promise<Bucket[]> {
      return request<Bucket[]>("GET", "buckets", { signal });
    },
    createBucket(name: string): Promise<{ name: string }> {
      return request<{ name: string }>("POST", "buckets", { body: { name } });
    },
    deleteBucket(name: string): Promise<void> {
      return request<undefined>("DELETE", `buckets/${pathOf(name)}`);
    },

    listObjects(bucket: string, query: ObjectListQuery, signal?: AbortSignal): Promise<ObjectPage> {
      return request<ObjectPage>("GET", `buckets/${pathOf(bucket)}/objects`, {
        query: {
          prefix: query.prefix,
          delimiter: query.delimiter,
          token: query.token,
          max: query.max,
        },
        signal,
      });
    },
    /** The contract deletes one key per call. Callers that delete many keys limit the concurrency. */
    deleteObject(bucket: string, key: string): Promise<void> {
      return request<undefined>("DELETE", `buckets/${pathOf(bucket)}/objects`, { query: { key } });
    },
    presign(
      bucket: string,
      input: { method: PresignMethod; key: string; expiresSeconds: number },
      signal?: AbortSignal,
    ): Promise<PresignResult> {
      return request<PresignResult>("POST", `buckets/${pathOf(bucket)}/presign`, { body: input, signal });
    },

    listUsers(signal?: AbortSignal): Promise<UserInfo[]> {
      return request<UserInfo[]>("GET", "users", { signal });
    },
    createUser(name: string): Promise<void> {
      return request<undefined>("POST", "users", { body: { name } });
    },
    deleteUser(name: string): Promise<void> {
      return request<undefined>("DELETE", `users/${pathOf(name)}`);
    },
    setUserEnabled(name: string, enabled: boolean): Promise<void> {
      const action = enabled ? "enable" : "disable";
      return request<undefined>("POST", `users/${pathOf(name)}/${action}`);
    },
    createAccessKey(name: string): Promise<CreatedAccessKey> {
      return request<CreatedAccessKey>("POST", `users/${pathOf(name)}/keys`);
    },
    deleteAccessKey(name: string, id: string): Promise<void> {
      return request<undefined>("DELETE", `users/${pathOf(name)}/keys/${pathOf(id)}`);
    },
    attachPolicy(user: string, policy: string): Promise<void> {
      return request<undefined>("POST", `users/${pathOf(user)}/policies`, { body: { policy } });
    },
    detachPolicy(user: string, policy: string): Promise<void> {
      return request<undefined>("DELETE", `users/${pathOf(user)}/policies/${pathOf(policy)}`);
    },

    listPolicies(signal?: AbortSignal): Promise<PolicyInfo[]> {
      return request<PolicyInfo[]>("GET", "policies", { signal });
    },
    putPolicy(name: string, document: PolicyDocument): Promise<void> {
      return request<undefined>("PUT", `policies/${pathOf(name)}`, { body: { document } });
    },
    deletePolicy(name: string): Promise<void> {
      return request<undefined>("DELETE", `policies/${pathOf(name)}`);
    },
  };
}

export type AdminApi = ReturnType<typeof createApiClient>;
