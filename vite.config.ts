import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { createHash } from "node:crypto";
export default defineConfig({
  plugins: [
    react(),
    {
      name: "sofar-offline-shell",
      writeBundle(options, bundle) {
        const assets = Object.keys(bundle)
          .filter((name) => /\.(js|css)$/.test(name))
          .map((name) => "/" + name);
        const revision = createHash("sha256")
          .update(assets.join("|"))
          .digest("hex")
          .slice(0, 12);
        const path = resolve(options.dir || "dist", "sw.js");
        const source = readFileSync(path, "utf8")
          .replace(
            /const PRECACHE\s*=\s*\[\];/,
            `const PRECACHE=${JSON.stringify(assets)};`,
          )
          .replace("sofar-shell-v1", `sofar-shell-${revision}`);
        writeFileSync(path, source);
      },
    },
  ],
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8080",
        configure: (proxy) => proxy.on("proxyReq", (request) => request.setHeader("origin", process.env.SOFAR_ORIGIN || "http://localhost:8080")),
      },
    },
  },
  build: { outDir: "dist" },
});
