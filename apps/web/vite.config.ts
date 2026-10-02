import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.DEV_PLANE_API_PROXY || "http://localhost:8080";

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@": fileURLToPath(new URL("./", import.meta.url)),
        "next/link": fileURLToPath(
          new URL("./lib/router-compat.tsx", import.meta.url),
        ),
        "next/navigation": fileURLToPath(
          new URL("./lib/router-compat.tsx", import.meta.url),
        ),
        "next/dynamic": fileURLToPath(
          new URL("./lib/dynamic-compat.tsx", import.meta.url),
        ),
      },
    },
    define: {
      // The shipped UI is same-origin with the Go API. During local Vite
      // development the proxy below preserves that exact production shape.
      "process.env.NEXT_PUBLIC_API_URL": "window.location.origin",
    },
    server: {
      port: 3000,
      strictPort: true,
      proxy: {
        "/api": { target: apiTarget, changeOrigin: false },
        "/health": { target: apiTarget, changeOrigin: false },
        "/ready": { target: apiTarget, changeOrigin: false },
      },
    },
    preview: {
      port: 3000,
      strictPort: true,
    },
    build: {
      // Build directly into the Go package so the API binary can embed the
      // exact static bundle it ships. The fallback index in dist/ remains
      // untouched because only the dist/web child is emptied by Vite.
      outDir: "../api/internal/webui/dist/web",
      emptyOutDir: true,
      sourcemap: true,
    },
  };
});
