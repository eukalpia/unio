import { createApiClient } from "./client";
import { getCsrf, getSession, setSession } from "../session/store";

/** The one client the app uses. It reads the CSRF value from memory on every mutation. */
export const api = createApiClient({
  getCsrf,
  onUnauthorized: () => {
    if (getSession().status === "signed-in") {
      setSession({ status: "anonymous" });
    }
  },
});
