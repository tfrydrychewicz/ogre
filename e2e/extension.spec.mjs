// The extension by itself, in a real browser, with no server: what it installs,
// and what its redirect rule does to a go link.
//
// The Directory is played by Playwright, which answers requests for
// http://localhost:4655 inside the browser. Nothing listens on port 4655 and
// no request leaves the browser, so these tests run whether or not a real
// Directory is running on this machine.

import { expect, test } from "@playwright/test";

import { DEFAULT_DIRECTORY_URL, buildRedirectRule } from "../extension/lib/config.js";
import { dynamicRules, launchWithExtension } from "./lib/harness.mjs";

test("a fresh install leaves exactly the redirect rule", async () => {
  const browser = await launchWithExtension({ waitForRule: false });
  try {
    expect(browser.serviceWorker.url()).toBe(`chrome-extension://${browser.extensionId}/background.js`);
    const manifest = await browser.serviceWorker.evaluate(() => chrome.runtime.getManifest());
    expect(manifest.name).toBe("Ogre");
    expect(manifest.permissions).toEqual(["declarativeNetRequestWithHostAccess"]);
    expect(manifest.host_permissions).toEqual(["*://go/*", "http://localhost:4655/*"]);

    // The worker installs the rule a moment after it starts.
    await expect.poll(() => dynamicRules(browser.serviceWorker)).toEqual([buildRedirectRule(DEFAULT_DIRECTORY_URL)]);
  } finally {
    await browser.close();
  }
});

// This does not prove that rules persist: Chromium started with
// --load-extension fires onInstalled on every launch (L0 spike, check 14), so
// the worker would re-install a lost rule. What it proves is that a second
// start neither duplicates the rule nor fails on it. Real persistence across
// a restart of Chrome is on the manual list; the onStartup path is covered by
// extension/background.test.js.
test("a second launch on the same profile leaves exactly one rule", async () => {
  const first = await launchWithExtension();
  await first.close();

  const second = await launchWithExtension({ userDataDir: first.userDataDir, waitForRule: false });
  try {
    expect(second.extensionId).toBe(first.extensionId);
    await expect.poll(() => dynamicRules(second.serviceWorker)).toEqual([buildRedirectRule(DEFAULT_DIRECTORY_URL)]);
  } finally {
    await second.close();
  }
});

test("the rule sends a go link to the same path and query on the Directory", async () => {
  const browser = await launchWithExtension();
  try {
    /** @type {string[]} what the stand-in Directory was asked for */
    const asked = [];
    await browser.context.route(`${DEFAULT_DIRECTORY_URL}/**`, async (route) => {
      if (new URL(route.request().url()).pathname === "/favicon.ico") {
        await route.fulfill({ status: 404 }); // the browser's own request, not a go link
        return;
      }
      asked.push(`${route.request().method()} ${route.request().url()}`);
      await route.fulfill({ status: 200, contentType: "text/plain", body: "stand-in Directory\n" });
    });

    /** @type {[string, string][]} go link, and the Directory URL it must become */
    const cases = [
      ["http://go/roadmap", "http://localhost:4655/roadmap"],
      ["https://go/roadmap", "http://localhost:4655/roadmap"],
      ["http://go/RoadMap", "http://localhost:4655/RoadMap"],
      ["http://go/", "http://localhost:4655/"],
      ["http://go/gh/api-server/pr/12?tab=files&q=a+b", "http://localhost:4655/gh/api-server/pr/12?tab=files&q=a+b"],
      ["http://go/a%20b%2Fc", "http://localhost:4655/a%20b%2Fc"],
    ];
    for (const [goLink, directoryUrl] of cases) {
      const page = await browser.context.newPage();
      await page.goto(goLink);
      expect(page.url(), goLink).toBe(directoryUrl);
      expect(asked.at(-1), goLink).toBe(`GET ${directoryUrl}`);
      await page.close();
    }
    expect(asked).toHaveLength(cases.length);
  } finally {
    await browser.close();
  }
});
