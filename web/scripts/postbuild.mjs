// Runs automatically after `npm run build` (npm "postbuild" lifecycle).
//
// 1. Restores `.gitkeep` in the embed directory: Vite's `emptyOutDir` wipes it,
//    and the Go embed pattern in internal/api/web/embed.go needs the directory
//    to exist on a checkout with no frontend build.
// 2. Drops the MSW mock worker from the production output unless the build was
//    made with VITE_ENABLE_MSW=true. The worker is a development tool and must
//    not ship inside the binary (handbook operations/security.md: no debug
//    surfaces in production).
import { existsSync, rmSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = resolve(here, "..", "..", "internal", "api", "web", "dist");

if (!existsSync(outDir)) {
  console.error(`postbuild: expected build output at ${outDir}`);
  process.exit(1);
}

writeFileSync(resolve(outDir, ".gitkeep"), "");

const worker = resolve(outDir, "mockServiceWorker.js");
if (process.env.VITE_ENABLE_MSW !== "true" && existsSync(worker)) {
  rmSync(worker);
  console.log("postbuild: removed mockServiceWorker.js from production output");
}
