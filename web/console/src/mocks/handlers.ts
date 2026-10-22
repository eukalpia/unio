import { http, HttpResponse, type PathParams } from "msw";
import { CSRF_HEADER, API_BASE, MAX_BODY_BYTES } from "../lib/api/client";
import {
  MOCK_ACCESS_KEY,
  MOCK_LOGIN_RETRY_AFTER_SECONDS,
  MOCK_SECRET_KEY,
  metricsAt,
  mockState,
  openSession,
  sessionOpen,
  takeLoginAttempt,
  type MockBucket,
  type MockObject,
} from "./state";

// MSW handlers that implement the Admin API contract. They enforce the same
// rules as the node: a session (the mock session stands in for the cookie),
// a CSRF header on every session-bound mutation, strict JSON bodies, the root
// user protections, and the 501 answer for named policies, which the node gives
// until the IAM change lands.

const api = (path: string): string => `${API_BASE}${path}`;
const ROOT = "root";
const MAX_OBJECT_LIST = 1000;
const DEFAULT_OBJECT_LIST = 200;

function fail(status: number, code: string, message: string, headers?: Record<string, string>): Response {
  return HttpResponse.json({ code, message, requestId: "mock-request" }, { status, headers });
}

/** Named policies are not available yet on the node, so the mock answers 501 as the node does. */
function policiesNotImplemented(): Response {
  return fail(501, "NotImplemented", "Named policies are not available on this node yet.");
}

/** Rejects a session-bound mutation: 401 without a session, 403 without the CSRF header. */
function guardMutation(request: Request): Response | null {
  const session = mockState().session;
  if (!sessionOpen(Date.now()) || session === null) {
    return fail(401, "AccessDenied", "Access denied.");
  }
  if (request.headers.get(CSRF_HEADER) !== session.csrf) {
    return fail(403, "AccessDenied", "Access denied.");
  }
  return null;
}

function guardRead(): Response | null {
  return sessionOpen(Date.now()) ? null : fail(401, "AccessDenied", "Access denied.");
}

async function readBody(request: Request, allowed: readonly string[]): Promise<Record<string, unknown> | Response> {
  const text = await request.text();
  if (new TextEncoder().encode(text).byteLength > MAX_BODY_BYTES) {
    return fail(413, "EntityTooLarge", "The request is too large.");
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return fail(400, "MalformedRequest", "The request body is not valid JSON.");
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail(400, "MalformedRequest", "The request body must be a JSON object.");
  }
  const body = value as Record<string, unknown>;
  if (Object.keys(body).some((key) => !allowed.includes(key))) {
    return fail(400, "MalformedRequest", "The request body has unknown fields.");
  }
  return body;
}

function bucketOr404(name: string): MockBucket | Response {
  const bucket = mockState().buckets.get(name);
  return bucket ?? fail(404, "NoSuchBucket", "The bucket does not exist.");
}

function encodeToken(key: string): string {
  return btoa(key).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function decodeToken(token: string): string | null {
  try {
    const padded = token.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(token.length / 4) * 4, "=");
    return atob(padded);
  } catch {
    return null;
  }
}

type Listed = { kind: "prefix"; key: string } | { kind: "object"; object: MockObject };

function listEntries(bucket: MockBucket, prefix: string, delimiter: string): Listed[] {
  const keys = [...bucket.objects.keys()].filter((key) => key.startsWith(prefix)).sort();
  const seen = new Set<string>();
  const entries: Listed[] = [];
  for (const key of keys) {
    const rest = key.slice(prefix.length);
    const at = delimiter === "" ? -1 : rest.indexOf(delimiter);
    if (at >= 0) {
      const common = prefix + rest.slice(0, at + delimiter.length);
      if (!seen.has(common)) {
        seen.add(common);
        entries.push({ kind: "prefix", key: common });
      }
      continue;
    }
    const object = bucket.objects.get(key);
    if (object) {
      entries.push({ kind: "object", object });
    }
  }
  return entries;
}

function keyOf(entry: Listed): string {
  return entry.kind === "prefix" ? entry.key : entry.object.key;
}

const bucketsGet = http.get(api("buckets"), () => {
  const denied = guardRead();
  if (denied) return denied;
  return HttpResponse.json(
    [...mockState().buckets.values()].map(({ name, created, versioning }) => ({ name, created, versioning })),
  );
});

const bucketsCreate = http.post(api("buckets"), async ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const body = await readBody(request, ["name"]);
  if (body instanceof Response) return body;
  const name = body["name"];
  if (typeof name !== "string" || name === "") {
    return fail(400, "InvalidBucketName", "The bucket name is not valid.");
  }
  if (name === "unio") {
    return fail(400, "InvalidBucketName", "The bucket name is reserved.");
  }
  if (mockState().buckets.has(name)) {
    return fail(409, "BucketAlreadyExists", "A bucket with this name already exists.");
  }
  mockState().buckets.set(name, { name, created: new Date().toISOString(), versioning: "", objects: new Map() });
  return HttpResponse.json({ name }, { status: 201 });
});

const bucketDelete = http.delete(api("buckets/:name"), ({ request, params }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const name = String(params["name"]);
  const bucket = bucketOr404(name);
  if (bucket instanceof Response) return bucket;
  if (bucket.objects.size > 0) {
    return fail(409, "BucketNotEmpty", "The bucket is not empty. Delete its objects first.");
  }
  mockState().buckets.delete(name);
  return new HttpResponse(null, { status: 204 });
});

const objectsList = http.get(api("buckets/:name/objects"), ({ params, request }) => {
  const denied = guardRead();
  if (denied) return denied;
  const bucket = bucketOr404(String(params["name"]));
  if (bucket instanceof Response) return bucket;
  const url = new URL(request.url);
  const prefix = url.searchParams.get("prefix") ?? "";
  const delimiter = url.searchParams.get("delimiter") ?? "";
  const maxParam = url.searchParams.get("max");
  const max = maxParam === null ? DEFAULT_OBJECT_LIST : Number(maxParam);
  if (!Number.isInteger(max) || max < 1 || max > MAX_OBJECT_LIST) {
    return fail(400, "InvalidArgument", "max must be between 1 and 1000.");
  }
  const tokenParam = url.searchParams.get("token");
  let after: string | null = null;
  if (tokenParam) {
    after = decodeToken(tokenParam);
    if (after === null) {
      return fail(400, "InvalidArgument", "The continuation token is not valid.");
    }
  }
  const entries = listEntries(bucket, prefix, delimiter).filter((entry) => after === null || keyOf(entry) > after);
  const page = entries.slice(0, max);
  const last = page[page.length - 1];
  // nextToken is null on the last page, as the contract says.
  const nextToken = entries.length > max && last ? encodeToken(keyOf(last)) : null;
  return HttpResponse.json({
    prefixes: page.flatMap((entry) => (entry.kind === "prefix" ? [entry.key] : [])),
    objects: page.flatMap((entry) =>
      entry.kind === "object"
        ? [{ key: entry.object.key, size: entry.object.size, etag: entry.object.etag, lastModified: entry.object.lastModified }]
        : [],
    ),
    nextToken,
  });
});

const objectDelete = http.delete(api("buckets/:name/objects"), ({ params, request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const bucket = bucketOr404(String(params["name"]));
  if (bucket instanceof Response) return bucket;
  const key = new URL(request.url).searchParams.get("key");
  if (!key) {
    return fail(400, "InvalidArgument", "The key is required.");
  }
  bucket.objects.delete(key);
  return new HttpResponse(null, { status: 204 });
});

const presign = http.post(api("buckets/:name/presign"), async ({ params, request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const bucket = bucketOr404(String(params["name"]));
  if (bucket instanceof Response) return bucket;
  const body = await readBody(request, ["method", "key", "expiresSeconds"]);
  if (body instanceof Response) return body;
  const method = body["method"];
  const key = body["key"];
  const expires = body["expiresSeconds"];
  if ((method !== "GET" && method !== "PUT") || typeof key !== "string" || key === "") {
    return fail(400, "InvalidArgument", "The presign request is not valid.");
  }
  if (typeof expires !== "number" || !Number.isInteger(expires) || expires < 1 || expires > 3600) {
    return fail(400, "InvalidArgument", "expiresSeconds must be between 1 and 3600.");
  }
  const url = `/unio/mock-s3/${encodeURIComponent(bucket.name)}/${encodeURIComponent(key)}?X-Mock-Method=${method}&X-Mock-Expires=${expires}`;
  return HttpResponse.json({ url });
});

const mockObjectPut = http.put("/unio/mock-s3/:bucket/:key", async ({ params, request }) => {
  const bucket = mockState().buckets.get(decodeURIComponent(String(params["bucket"])));
  if (!bucket) {
    return fail(404, "NoSuchBucket", "The bucket does not exist.");
  }
  const key = decodeURIComponent(String(params["key"]));
  const size = (await request.arrayBuffer()).byteLength;
  bucket.objects.set(key, {
    key,
    size,
    etag: `"${size.toString(16)}mock"`,
    lastModified: new Date().toISOString(),
  });
  return new HttpResponse(null, { status: 200 });
});

const mockObjectGet = http.get("/unio/mock-s3/:bucket/:key", ({ params }) => {
  const bucket = mockState().buckets.get(decodeURIComponent(String(params["bucket"])));
  const object = bucket?.objects.get(decodeURIComponent(String(params["key"])));
  if (!object) {
    return fail(404, "NoSuchKey", "The object does not exist.");
  }
  return new HttpResponse(`mock content of ${object.key}`, {
    headers: { "Content-Type": "text/plain", "Content-Length": String(object.size) },
  });
});

const clusterGet = http.get(api("cluster"), () => {
  const denied = guardRead();
  if (denied) return denied;
  return HttpResponse.json(mockState().cluster);
});

const metricsGet = http.get(api("metrics/summary"), () => {
  const denied = guardRead();
  if (denied) return denied;
  return HttpResponse.json(metricsAt(Date.now()));
});

const usersGet = http.get(api("users"), () => {
  const denied = guardRead();
  if (denied) return denied;
  return HttpResponse.json([...mockState().users.values()]);
});

const userCreate = http.post(api("users"), async ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const body = await readBody(request, ["name"]);
  if (body instanceof Response) return body;
  const name = body["name"];
  if (typeof name !== "string" || !/^[A-Za-z0-9+=,.@_-]{1,64}$/.test(name)) {
    return fail(400, "InvalidArgument", "The user name is not valid.");
  }
  if (mockState().users.has(name)) {
    return fail(409, "EntityAlreadyExists", "A user with this name already exists.");
  }
  mockState().users.set(name, { name, enabled: true, policies: [], accessKeys: [] });
  return HttpResponse.json({ name }, { status: 201 });
});

function userOr404(name: string) {
  const user = mockState().users.get(name);
  return user ?? fail(404, "NoSuchEntity", "The user does not exist.");
}

const userDelete = http.delete(api("users/:name"), ({ params, request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const name = String(params["name"]);
  if (name === ROOT) {
    return fail(409, "RootUserProtected", "The root user cannot be deleted.");
  }
  const user = userOr404(name);
  if (user instanceof Response) return user;
  mockState().users.delete(name);
  return new HttpResponse(null, { status: 204 });
});

function setEnabled(enabled: boolean) {
  return ({ params, request }: { params: PathParams; request: Request }) => {
    const denied = guardMutation(request);
    if (denied) return denied;
    const name = String(params["name"]);
    if (name === ROOT) {
      return fail(409, "RootUserProtected", "The root user cannot be disabled.");
    }
    const user = userOr404(name);
    if (user instanceof Response) return user;
    user.enabled = enabled;
    return new HttpResponse(null, { status: 204 });
  };
}

const userEnable = http.post(api("users/:name/enable"), setEnabled(true));
const userDisable = http.post(api("users/:name/disable"), setEnabled(false));

const keyCreate = http.post(api("users/:name/keys"), ({ params, request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const user = userOr404(String(params["name"]));
  if (user instanceof Response) return user;
  if (user.accessKeys.length >= 2) {
    return fail(409, "LimitExceeded", "This user already has the maximum number of access keys.");
  }
  const id = `UNIO${Math.random().toString(36).slice(2, 10).toUpperCase().padEnd(16, "X")}`;
  user.accessKeys.push({ id, created: new Date().toISOString() });
  const secretKey = `${Math.random().toString(36).slice(2)}${Math.random().toString(36).slice(2)}`;
  return HttpResponse.json({ accessKeyId: id, secretKey }, { status: 201 });
});

const keyDelete = http.delete(api("users/:name/keys/:id"), ({ params, request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  const user = userOr404(String(params["name"]));
  if (user instanceof Response) return user;
  user.accessKeys = user.accessKeys.filter((key) => key.id !== String(params["id"]));
  return new HttpResponse(null, { status: 204 });
});

const attach = http.post(api("users/:name/policies"), ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  return policiesNotImplemented();
});

const detach = http.delete(api("users/:name/policies/:policy"), ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  return policiesNotImplemented();
});

const policiesGet = http.get(api("policies"), () => {
  const denied = guardRead();
  if (denied) return denied;
  return policiesNotImplemented();
});

const policyPut = http.put(api("policies/:name"), ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  return policiesNotImplemented();
});

const policyDelete = http.delete(api("policies/:name"), ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  return policiesNotImplemented();
});

const sessionGet = http.get(api("session"), () => {
  const session = mockState().session;
  if (!session || !sessionOpen(Date.now())) {
    return fail(401, "AccessDenied", "Access denied.");
  }
  // A cookie-authenticated GET returns the CSRF value, so a reload resumes the session.
  return HttpResponse.json({ accessKey: session.accessKey, expiresAt: session.expiresAt, csrf: session.csrf });
});

const sessionCreate = http.post(api("session"), async ({ request }) => {
  const now = Date.now();
  if (!takeLoginAttempt(now)) {
    return fail(429, "SlowDown", "Too many sign-in attempts. Wait a minute and try again.", {
      "Retry-After": String(MOCK_LOGIN_RETRY_AFTER_SECONDS),
    });
  }
  const body = await readBody(request, ["accessKey", "secretKey"]);
  if (body instanceof Response) return body;
  const accessKey = body["accessKey"];
  const secretKey = body["secretKey"];
  if (accessKey !== MOCK_ACCESS_KEY || secretKey !== MOCK_SECRET_KEY) {
    return fail(403, "AccessDenied", "Access denied.");
  }
  const session = openSession(now);
  return HttpResponse.json({ accessKey: session.accessKey, expiresAt: session.expiresAt, csrf: session.csrf });
});

const sessionDelete = http.delete(api("session"), ({ request }) => {
  const denied = guardMutation(request);
  if (denied) return denied;
  mockState().session = null;
  return new HttpResponse(null, { status: 204 });
});

export const handlers = [
  sessionGet,
  sessionCreate,
  sessionDelete,
  clusterGet,
  metricsGet,
  bucketsGet,
  bucketsCreate,
  bucketDelete,
  objectsList,
  objectDelete,
  presign,
  mockObjectPut,
  mockObjectGet,
  usersGet,
  userCreate,
  userDelete,
  userEnable,
  userDisable,
  keyCreate,
  keyDelete,
  attach,
  detach,
  policiesGet,
  policyPut,
  policyDelete,
];
