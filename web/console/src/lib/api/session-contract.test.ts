import { describe, expect, it, vi } from "vitest";
import { createApiClient, parseRetryAfter } from "./client";
import { ApiError, describeError, isNotImplemented } from "./errors";
import { MOCK_ACCESS_KEY, MOCK_LOGIN_RETRY_AFTER_SECONDS, MOCK_SECRET_KEY } from "../../mocks/state";
import { api } from "./instance";

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

describe("GET session and POST session", () => {
  it("returns the CSRF value for a cookie-authenticated request", async () => {
    const created = await api.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);

    const info = await api.getSession();

    expect(info).toMatchObject({ accessKey: MOCK_ACCESS_KEY, csrf: created.csrf });
    expect(info?.expiresAt).toEqual(expect.any(String));
  });
});

describe("Retry-After", () => {
  it("parses delay-seconds", () => {
    expect(parseRetryAfter("42")).toBe(42);
    expect(parseRetryAfter(" 7 ")).toBe(7);
  });

  it("parses an HTTP date relative to the given clock, never below zero", () => {
    const now = Date.parse("2026-10-22T10:00:00Z");
    expect(parseRetryAfter("Thu, 22 Oct 2026 10:01:30 GMT", now)).toBe(90);
    expect(parseRetryAfter("Thu, 22 Oct 2026 09:00:00 GMT", now)).toBe(0);
  });

  it("ignores a missing or unusable value", () => {
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("soon")).toBeUndefined();
  });

  it("carries the seconds of a 429 response on the error", async () => {
    const client = createApiClient({
      getCsrf: () => null,
      fetch: vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse(429, { code: "SlowDown", message: "Too many sign-in attempts.", requestId: "r" }, { "Retry-After": "42" }),
      ),
    });

    const error = await client.login("a", "b").catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).retryAfterSeconds).toBe(42);
  });

  it("gives the login rate limit the node's Retry-After in the mock", async () => {
    const client = createApiClient({ getCsrf: () => null });
    for (let attempt = 0; attempt < 10; attempt += 1) {
      await client.login(MOCK_ACCESS_KEY, "wrong").catch(() => undefined);
    }

    const error = await client.login(MOCK_ACCESS_KEY, "wrong").catch((caught: unknown) => caught);

    expect((error as ApiError).retryAfterSeconds).toBe(MOCK_LOGIN_RETRY_AFTER_SECONDS);
  });
});

describe("501 NotImplemented", () => {
  it("is recognised and explained without the word 'error'", async () => {
    const client = createApiClient({
      getCsrf: () => null,
      fetch: vi.fn<typeof fetch>().mockResolvedValue(
        jsonResponse(501, { code: "NotImplemented", message: "Named policies are not available on this node yet.", requestId: "r" }),
      ),
    });

    const error = await client.listPolicies().catch((caught: unknown) => caught);

    expect(isNotImplemented(error)).toBe(true);
    expect(describeError(error)).toBe("This feature is not available on this node yet.");
  });

  it("uses the same explanation when the body is not contract JSON", async () => {
    const client = createApiClient({
      getCsrf: () => null,
      fetch: vi.fn<typeof fetch>().mockResolvedValue(new Response("nope", { status: 501 })),
    });

    const error = await client.listPolicies().catch((caught: unknown) => caught);

    expect(isNotImplemented(error)).toBe(true);
  });
});
