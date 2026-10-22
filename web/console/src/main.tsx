import "@fontsource-variable/space-grotesk/wght.css";
import "./index.css";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { queryClient } from "./lib/query";
import { router } from "./router";

async function enableMockServer(): Promise<void> {
  // VITE_MOCKS is replaced at build time. Production builds do not set it,
  // so the mock code is dropped from the bundle.
  if (import.meta.env.VITE_MOCKS !== "true") {
    return;
  }
  const { worker } = await import("./mocks/browser");
  await worker.start({
    serviceWorker: { url: `${import.meta.env.BASE_URL}mockServiceWorker.js` },
    onUnhandledRequest: "bypass",
    quiet: true,
  });
}

async function start(): Promise<void> {
  await enableMockServer();
  const container = document.getElementById("root");
  if (!container) {
    throw new Error("The console root element is missing from index.html.");
  }
  createRoot(container).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  );
}

void start();
