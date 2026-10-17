import { defineConfig } from "vite";
export default defineConfig({
  base: "/assets/control/",
  build: {
    outDir: "../internal/web/assets/control",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "app.js",
        chunkFileNames: "[name]-[hash].js",
        assetFileNames: "[name]-[hash][extname]",
      },
    },
  },
  server: {
    proxy: {
      "/funnel.v1.": "http://127.0.0.1:8097",
      "/assets": "http://127.0.0.1:8097",
    },
  },
});
