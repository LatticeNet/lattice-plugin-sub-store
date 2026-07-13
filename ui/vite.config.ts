import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "./",
  plugins: [vue()],
  build: {
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2020",
  },
});
