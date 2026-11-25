import { setupServer } from "msw/node";
import { handlers } from "./handlers";

/** Request interception for the test suite. */
export const server = setupServer(...handlers);
