import { setupWorker } from "msw/browser";
import { handlers } from "./handlers";

/** Service worker for development with mocks. It is never imported by the production build. */
export const worker = setupWorker(...handlers);
