import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";
import { resetMockState } from "../mocks/state";
import { server } from "../mocks/server";
import { setSession } from "../lib/session/store";

// The suite runs in jsdom for component tests and in Node for the token checks,
// so DOM-only cleanup is guarded.
const hasDom = typeof window !== "undefined";

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  if (hasDom) {
    cleanup();
    window.localStorage.clear();
  }
  server.resetHandlers();
  resetMockState();
  setSession({ status: "unknown" });
});

afterAll(() => {
  server.close();
});
