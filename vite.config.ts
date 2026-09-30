import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// The dev server proxies /v1 to the Go API so the browser sees a single
// origin. That keeps cookies working and means no CORS setup while
// developing. Override with VITE_API_PROXY when the API lives elsewhere.
export default defineConfig(({ mode }) => {
  // loadEnv is used rather than process.env because Vite populates
  // process.env only for variables already in the shell. Reading the env
  // files directly is what makes VITE_API_PROXY in .env.local work.
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.VITE_API_PROXY || "http://localhost:8080";

  return {
    plugins: [react()],
    server: {
      port: 5173,
      // This filesystem does not deliver change notifications, so the
      // default native watcher silently misses every edit and serves stale
      // modules. Polling is the only reliable option here.
      watch: {
        usePolling: true,
        interval: 300,
      },
      proxy: {
        "/v1": { target: apiTarget, changeOrigin: true },
        "/health": { target: apiTarget, changeOrigin: true },
        "/ready": { target: apiTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: "dist",
      sourcemap: true,
      // pdf.js ships as ESM that references Node built-ins; the browser
      // build of react-pdf does not need them, and leaving them out stops
      // the bundle failing to resolve at load time.
      rollupOptions: {
        external: ["canvas", "fs", "http", "https", "url", "zlib"],
      },
    },
    // Vite serves dependencies pre-bundled; react-pdf's worker and its
    // dependencies must be excluded or esbuild collapses the worker into
    // the main chunk and pdf.js then fails to spawn it.
    optimizeDeps: {
      exclude: ["pdfjs-dist"],
    },
  };
});
