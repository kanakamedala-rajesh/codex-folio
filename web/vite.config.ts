import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

const webDirectory = dirname(fileURLToPath(import.meta.url));
const productVersion = readFileSync(
  resolve(webDirectory, "../internal/buildinfo/version.txt"),
  "utf8",
).trim();

if (!productVersion) {
  throw new Error("internal/buildinfo/version.txt must contain the product version");
}

export default defineConfig({
  base: "/assets/",
  plugins: [react(), tailwindcss()],
  publicDir: false,
  define: {
    __CODEXFOLIO_VERSION__: JSON.stringify(productVersion),
  },
  build: {
    emptyOutDir: true,
    outDir: "../internal/httpapi/assets",
    sourcemap: false,
    assetsDir: ".",
    rollupOptions: {
      output: {
        assetFileNames: (assetInfo) =>
          assetInfo.name?.endsWith(".css") ? "styles.css" : "[name][extname]",
        chunkFileNames: "[name].js",
        entryFileNames: "app.js",
      },
    },
  },
});
