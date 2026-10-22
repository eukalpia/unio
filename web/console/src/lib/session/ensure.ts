import type { AdminApi } from "../api/client";
import { getSession, setSession, type SessionState } from "./store";

let pending: Promise<SessionState> | null = null;

/**
 * Resolves the session once per page load. GET session answers 200 with the CSRF
 * value when the session cookie is valid, so a reload resumes without signing in.
 * It answers 401 when there is no valid session, and the caller shows the login page.
 * The CSRF value stays in memory; this function writes nothing to browser storage.
 */
export function ensureSession(api: AdminApi): Promise<SessionState> {
  const current = getSession();
  if (current.status !== "unknown") {
    return Promise.resolve(current);
  }
  pending ??= api
    .getSession()
    .then((info) => {
      const next: SessionState =
        info?.csrf == null
          ? { status: "anonymous" }
          : { status: "signed-in", accessKey: info.accessKey, expiresAt: info.expiresAt, csrf: info.csrf };
      setSession(next);
      return next;
    })
    .finally(() => {
      pending = null;
    });
  return pending;
}
