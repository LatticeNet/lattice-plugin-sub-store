import { defineConfig } from "vitest/config";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  base: "./",
  plugins: [vue()],
  // The unit suite is the src/*.test.ts files. Vitest's default pattern also
  // claims *.spec.ts, which would hand it the Playwright drive in e2e/ and fail
  // on the first import.
  test: { include: ["src/**/*.test.ts"] },
  build: {
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2020",
  },
});
