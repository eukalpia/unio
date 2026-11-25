import { describe, expect, it, vi } from "vitest";
import { api } from "../api/instance";
import { createApiClient } from "../api/client";
import { MOCK_ACCESS_KEY, MOCK_SECRET_KEY } from "../../mocks/state";
import { ensureSession } from "./ensure";
import { getSession, setSession } from "./store";

describe("ensureSession", () => {
  it("resumes a reloaded page from GET session, with the CSRF value kept in memory", async () => {
    // The cookie is set by a sign-in. The store is then reset, as a reload would do.
    await api.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
    setSession({ status: "unknown" });

    const session = await ensureSession(api);

    expect(session.status).toBe("signed-in");
    if (session.status === "signed-in") {
      expect(session.csrf).toMatch(/^csrf-/);
      expect(session.accessKey).toBe(MOCK_ACCESS_KEY);
    }
  });

  it("goes to anonymous when there is no valid session cookie", async () => {
    const session = await ensureSession(api);

    expect(session.status).toBe("anonymous");
  });

  it("writes nothing to localStorage or sessionStorage, so the CSRF value is never persisted", async () => {
    await api.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
    setSession({ status: "unknown" });

    await ensureSession(api);

    expect(window.localStorage.length).toBe(0);
    expect(window.sessionStorage.length).toBe(0);
  });

  it("shares one request between concurrent callers", async () => {
    const fetchSpy = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        new Response(JSON.stringify({ accessKey: MOCK_ACCESS_KEY, expiresAt: null, csrf: "csrf-one" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    const client = createApiClient({ getCsrf: () => null, fetch: fetchSpy });

    const [first, second] = await Promise.all([ensureSession(client), ensureSession(client)]);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(first).toEqual(second);
    expect(getSession().status).toBe("signed-in");
  });

  it("treats a 200 without a CSRF value as signed out, since mutations could not be sent", async () => {
    const client = createApiClient({
      getCsrf: () => null,
      fetch: () =>
        Promise.resolve(
          new Response(JSON.stringify({ accessKey: MOCK_ACCESS_KEY, expiresAt: null, csrf: null }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
    });

    await expect(ensureSession(client)).resolves.toEqual({ status: "anonymous" });
  });
});
