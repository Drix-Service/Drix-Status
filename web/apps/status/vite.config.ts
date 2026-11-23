import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

const web = path.resolve(import.meta.dirname, "../..");

export default defineConfig({
  root: import.meta.dirname,
  cacheDir: path.join(web, "node_modules/.vite-status"),
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@drix/ui": path.join(web, "packages/ui/src"),
      "@status": path.join(import.meta.dirname, "src"),
    },
    dedupe: ["react", "react-dom"],
  },
  server: {
    port: Number(process.env.STATUS_PORT) || 3000,
    strictPort: true,
    proxy: {
      "/api": { target: process.env.API_URL ?? "http://localhost:8080", changeOrigin: false },
    },
  },
  build: {
    outDir: path.join(web, "dist/status"),
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
  },
});
