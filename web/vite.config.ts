import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The Go binary embeds the build output from internal/api/web/dist and serves
// `index.html` for client-side routes (ADR 0002), so the SPA always lives at
// the origin root. `web/` is a separate Go module (see web/go.mod) purely so
// `go build ./...` never descends into node_modules.
const BACKEND_ORIGIN = "http://localhost:8080";

export default defineConfig({
  base: "/",
  plugins: [react()],
  build: {
    outDir: "../internal/api/web/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    proxy: {
      "/api": BACKEND_ORIGIN,
      "/livez": BACKEND_ORIGIN,
      "/readyz": BACKEND_ORIGIN,
    },
  },
});
