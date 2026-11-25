import { useSyncExternalStore } from "react";

// The session lives in memory only. The CSRF value is never written to storage
// (not localStorage, not sessionStorage), and the secret key never enters this store.
// A reload recovers the CSRF value from GET session, which the node returns for
// cookie-authenticated requests.

export type SessionState =
  | { status: "unknown" }
  | { status: "anonymous" }
  | {
      status: "signed-in";
      accessKey: string;
      /** Null only if the node reports no expiry. The console then relies on the server's 401. */
      expiresAt: string | null;
      csrf: string;
    };

type Listener = () => void;

let state: SessionState = { status: "unknown" };
const listeners = new Set<Listener>();

export function getSession(): SessionState {
  return state;
}

export function setSession(next: SessionState): void {
  if (next === state) {
    return;
  }
  state = next;
  for (const listener of listeners) {
    listener();
  }
}

export function subscribeSession(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getCsrf(): string | null {
  return state.status === "signed-in" ? state.csrf : null;
}

export function useSession(): SessionState {
  return useSyncExternalStore(subscribeSession, getSession, getSession);
}
