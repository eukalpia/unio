import { describe, expect, it, vi } from "vitest";
import { createApiClient, MAX_BODY_BYTES } from "./client";
import { ApiError, ReauthRequiredError, describeError } from "./errors";
import { mockState, MOCK_ACCESS_KEY, MOCK_SECRET_KEY } from "../../mocks/state";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestUrl(spy: { mock: { calls: unknown[][] } }): string {
  const input: unknown = spy.mock.calls[0]?.[0];
  if (typeof input === "string") {
    return input;
  }
  if (input instanceof URL) {
    return input.href;
  }
  return (input as Request).url;
}

describe("admin API client against the contract mock", () => {
  it("sends the CSRF value from the sign-in response on every mutation", async () => {
    let csrf: string | null = null;
    const client = createApiClient({ getCsrf: () => csrf });

    const session = await client.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
    csrf = session.csrf;
    await client.createBucket("fresh-bucket");

    expect(mockState().buckets.has("fresh-bucket")).toBe(true);
  });

  it("refuses a mutation without CSRF before any request is sent", async () => {
    const fetchSpy = vi.fn<typeof fetch>();
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    await expect(client.createBucket("abc")).rejects.toBeInstanceOf(ReauthRequiredError);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("maps a contract error body to ApiError with its code, message and request id", async () => {
    const session = await createApiClient({ getCsrf: () => null }).login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
    const client = createApiClient({ getCsrf: () => session.csrf });

    const failure = client.createBucket("unio");

    await expect(failure).rejects.toMatchObject({
      status: 400,
      code: "InvalidBucketName",
      requestId: "mock-request",
    });
  });

  it("reads a session as null when the cookie is missing or expired", async () => {
    const client = createApiClient({ getCsrf: () => null });

    await expect(client.getSession()).resolves.toBeNull();
  });

  it("gives the login error without a session, so the wrong secret reveals nothing", async () => {
    const client = createApiClient({ getCsrf: () => null });

    const failure = client.login(MOCK_ACCESS_KEY, "wrong");

    await expect(failure).rejects.toMatchObject({ status: 403, code: "AccessDenied" });
  });
});

describe("admin API client behaviour", () => {
  it("ends the session through onUnauthorized for a session-bound 401 only", async () => {
    const onUnauthorized = vi.fn();
    const fetchSpy = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(401, { code: "AccessDenied", message: "Access denied.", requestId: "r1" }))
      .mockResolvedValueOnce(jsonResponse(401, { code: "AccessDenied", message: "Access denied.", requestId: "r2" }));
    const client = createApiClient({ getCsrf: () => "token", fetch: fetchSpy, onUnauthorized });

    await expect(client.listBuckets()).rejects.toBeInstanceOf(ApiError);
    await expect(client.getSession()).resolves.toBeNull();

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("maps 429 to SlowDown with a client-safe message", async () => {
    const fetchSpy = vi.fn<typeof fetch>().mockResolvedValue(new Response("slow", { status: 429 }));
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    const error = await client.login("a", "b").catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiError);
    expect(describeError(error)).toBe("Too many requests. Wait a moment and try again.");
  });

  it("falls back to a generic error when the failure body is not contract JSON", async () => {
    const fetchSpy = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response("<html>internal detail</html>", { status: 500 }));
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    const error = await client.cluster().catch((caught: unknown) => caught);

    expect(error).toMatchObject({ status: 500, code: "InternalError" });
    expect((error as Error).message).not.toContain("internal detail");
  });

  it("encodes names and keys so they cannot change the route", async () => {
    const fetchSpy = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }));
    const client = createApiClient({ getCsrf: () => "token", fetch: fetchSpy });

    await client.deleteObject("my-bucket", "dir/a?b=c&d");

    const url = new URL(requestUrl(fetchSpy));
    expect(url.pathname).toBe("/unio/admin/v1/buckets/my-bucket/objects");
    expect(url.searchParams.get("key")).toBe("dir/a?b=c&d");
  });

  it("leaves out query parameters that are not set", async () => {
    const fetchSpy = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse(200, { prefixes: [], objects: [], nextToken: null }));
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    await client.listObjects("b", { prefix: "logs/", delimiter: "/" });

    const url = new URL(requestUrl(fetchSpy));
    expect([...url.searchParams.keys()]).toEqual(["prefix", "delimiter"]);
  });

  it("refuses bodies above the 64 KiB contract limit without sending them", async () => {
    const fetchSpy = vi.fn<typeof fetch>();
    const client = createApiClient({ getCsrf: () => "token", fetch: fetchSpy });
    const huge = { document: { Statement: "x".repeat(MAX_BODY_BYTES) } };

    await expect(client.putPolicy("big", huge.document)).rejects.toMatchObject({ status: 413 });
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("reports an unreachable server as a network error", async () => {
    const fetchSpy = vi.fn<typeof fetch>().mockRejectedValue(new TypeError("Failed to fetch"));
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    const error = await client.cluster().catch((caught: unknown) => caught);

    expect(error).toMatchObject({ status: 0, code: "NetworkError" });
    expect(describeError(error)).toContain("Cannot reach the console server");
  });

  it("rethrows an abort so callers can tell cancellation from failure", async () => {
    const controller = new AbortController();
    const fetchSpy = vi.fn<typeof fetch>().mockImplementation(() => {
      controller.abort();
      return Promise.reject(new DOMException("aborted", "AbortError"));
    });
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    await expect(client.cluster(controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  });
});
