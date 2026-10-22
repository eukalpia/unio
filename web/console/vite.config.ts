import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, loadEnv } from "vite";

// The console is served by the node under /unio/console/ (see docs/security
// and the Admin API contract). Every asset URL is relative to that prefix.
const BASE = "/unio/console/";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  // In live mode the dev server forwards the admin API to a running node.
  const apiTarget = env.UNIO_DEV_API;

  return {
    base: BASE,
    plugins: [react(), tailwindcss()],
    server: {
      proxy: apiTarget
        ? { "/unio/admin": { target: apiTarget, changeOrigin: true, secure: false } }
        : undefined,
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
      target: "es2022",
      sourcemap: false,
      // Inlined assets become data: URIs, which the CSP (font-src 'self') blocks.
      assetsInlineLimit: 0,
      cssCodeSplit: true,
      reportCompressedSize: true,
    },
    test: {
      environment: "jsdom",
      include: ["src/**/*.test.{ts,tsx}"],
      setupFiles: ["src/test/setup.ts"],
      css: false,
      restoreMocks: true,
    },
  };
});
