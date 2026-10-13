import { defineConfig } from "@playwright/test";

// Run from the repository root: npm run e2e
//
// One worker and no parallelism: the Directory under test listens on the fixed
// address the extension redirects to (localhost:4655), so two tests can never
// run a server at the same time. Relative paths here are relative to e2e/.
export default defineConfig({
  testDir: ".",
  testMatch: "**/*.spec.mjs",
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 60_000,
  reporter: "list",
  outputDir: "test-results",
});
