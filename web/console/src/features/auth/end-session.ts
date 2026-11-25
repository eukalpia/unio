import { setSession } from "../../lib/session/store";
import { resetMetricsHistory } from "../../lib/metrics-history";
import { uploads } from "../objects/uploads";

/** Ends the session on the client: cancels uploads and forgets per-session data. */
export function endSession(): void {
  uploads.cancelAll();
  resetMetricsHistory();
  setSession({ status: "anonymous" });
}
