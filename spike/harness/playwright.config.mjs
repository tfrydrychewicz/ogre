import { defineConfig } from "@playwright/test";

// One worker, no parallelism: every check shares the stub ports 4655/4656.
export default defineConfig({
  testDir: "tests",
  testMatch: "**/*.spec.mjs",
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 90_000,
  reporter: "list",
});
