import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 后端地址：dev 下把 /api 代理到 Go 控制面（默认 127.0.0.1:8788，可经 VITE_BACKEND 覆盖）。
const backend = process.env.VITE_BACKEND || "http://127.0.0.1:8788";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: backend, changeOrigin: true },
    },
  },
  build: { outDir: "dist" },
});
