import { defineConfig } from "@playwright/test";

/**
 * The design drive. It asserts the geometry a design review measures by hand,
 * so a regression in the Files lens fails here rather than in the next review.
 *
 * CI does not run it: the gates there are `npm test`, typecheck, build and
 * verify:build, and this needs a browser download. Run it locally with
 * `npm run test:e2e`.
 */
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: true,
  reporter: process.env.CI ? "line" : "list",
  use: { baseURL: "http://localhost:5199" },
  webServer: {
    // Not `npm run dev`: that script carries --open, which launches a browser
    // on every run.
    command: "npx vite --port 5199 --strictPort",
    url: "http://localhost:5199/dev.html",
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
