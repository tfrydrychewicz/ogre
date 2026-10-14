import { defineConfig } from "@playwright/test";

// Run from the repository root: npm run e2e (Chromium), npm run e2e:browsers
// (Firefox and WebKit).
//
// One worker and no parallelism: the Directory under test of most files
// listens on the one address the extension redirects to (localhost:4655, or
// the port OGRE_E2E_PORT names; lib/harness.mjs), so two tests can never run
// a server at the same time. Relative paths here are relative to e2e/.
//
// Three projects (plan L4.8, NFR-20):
//   - chromium runs every file. Most launch Playwright's Chromium themselves
//     (lib/harness.mjs), with or without the extension; the cross-browser
//     files use the project's browser, the same bundled Chromium. It is also
//     the proxy for Microsoft Edge, which is Chromium; no Edge binary is run.
//   - firefox and webkit run only the files that need no extension, since
//     neither engine can load it: the web UI's scenarios and pages
//     (browsers.spec.mjs) and the repro of WebKit's <select> report
//     (webkit-select-csp.spec.mjs). Their Directory takes a free port of its
//     own, never 4655. WebKit stands in for Safari.
const CROSS_BROWSER = ["browsers.spec.mjs", "webkit-select-csp.spec.mjs"];

export default defineConfig({
  testDir: ".",
  testMatch: "**/*.spec.mjs",
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 60_000,
  reporter: "list",
  outputDir: "test-results",
  use: { headless: true },
  projects: [
    { name: "chromium", use: { browserName: "chromium", channel: "chromium" } },
    { name: "firefox", testMatch: CROSS_BROWSER, use: { browserName: "firefox" } },
    { name: "webkit", testMatch: CROSS_BROWSER, use: { browserName: "webkit" } },
  ],
});
