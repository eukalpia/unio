// Error types raised by the admin API client, and the user-facing text for them.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string | undefined;
  /** Seconds to wait before retrying, from the Retry-After header of a 429 response. */
  readonly retryAfterSeconds: number | undefined;

  constructor(status: number, code: string, message: string, requestId?: string, retryAfterSeconds?: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

/** Raised before a mutation is sent when no CSRF value is held in memory. */
export class ReauthRequiredError extends Error {
  constructor() {
    super("Sign in again to make changes.");
    this.name = "ReauthRequiredError";
  }
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

/** True when the node does not implement the endpoint yet (501). */
export function isNotImplemented(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 501 || error.code === "NotImplemented");
}

/** Maps any thrown value to a short message that is safe to show to the user. */
export function describeError(error: unknown): string {
  if (error instanceof ReauthRequiredError) {
    return error.message;
  }
  if (error instanceof ApiError) {
    if (error.status === 0) {
      return "Cannot reach the console server. Check the connection and try again.";
    }
    if (error.code === "SlowDown" || error.status === 429) {
      return "Too many requests. Wait a moment and try again.";
    }
    if (isNotImplemented(error)) {
      return "This feature is not available on this node yet.";
    }
    return error.message;
  }
  return "Something went wrong. Try again.";
}
