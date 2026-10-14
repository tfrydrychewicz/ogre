// The extension's first-run page (plan L4.3, L4.5; EXT-3): it opens by itself
// on install, once; it shows whether the Directory answers, and how to start
// it when it does not; it notices by itself that the user typed go/, but only
// once the Directory answers (a go/ visit that fails teaches Chrome nothing:
// L0 spike, check 3), then a go link without the trailing slash; and once it
// is done or dismissed it never opens by itself again.
//
// Chromium started with --load-extension reports a fresh install on every
// launch (L0 spike, check 14), so a relaunch on the same profile is how these
// tests ask the worker to open the page "again", and a relaunch with
// --restore-last-session whether it opens a second one beside a restored
// tab. A real Chrome restart, which fires onStartup instead, is on the
// manual list.
//
// page.goto stands in for typing go/ in the address bar: the page watches
// requests, which a typed navigation and page.goto make alike, both started
// by the browser with no initiating page. Typing itself, and Chrome's
// search-instead-of-navigate behavior that the page teaches around, are on
// the manual list. (A go link that a web page opens would count as well:
// Chrome does not tell the extension who started a navigation; see
// onboarding.js.)
//
// Under OGRE_E2E_PORT the page opens at install, before the harness points
// the extension at the test server; it follows the saved address by itself,
// which these tests rely on instead of reloading it.

import { expect, test } from "@playwright/test";
import net from "node:net";
import path from "node:path";

import { DEFAULT_DIRECTORY_URL, FIRST_RUN_PAGE, STORAGE_KEYS } from "../extension/lib/config.js";
import { startDirectoryOnFreePort } from "./lib/browsers.mjs";
import {
  AT_DEFAULT_ADDRESS,
  DIRECTORY_URL,
  NEW_TAB_GRACE_MS,
  buildServer,
  createLink,
  extensionUrl,
  launchWithExtension,
  pointExtensionAt,
  startFixture,
  startServer,
  tempDir,
  waitForInstallHandled,
} from "./lib/harness.mjs";
import { expectAccessible, expectNoHorizontalScroll, refusedMeta, watchExtensionPages } from "./lib/ui.mjs";

/** @typedef {import("@playwright/test").Page} Page */
/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */

test.beforeAll(() => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
});

/**
 * The first-run tabs open in the browser.
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @returns {Page[]}
 */
function firstRunTabs(context, extensionId) {
  const url = extensionUrl(extensionId, FIRST_RUN_PAGE);
  return context.pages().filter((page) => page.url() === url);
}

/** @param {number} ms */
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Launches the browser with the extension, leaving the first-run page open.
 *
 * @param {Parameters<typeof launchWithExtension>[0]} [options]
 */
function launch(options = {}) {
  return launchWithExtension({ ...options, keepFirstRunTab: true });
}

/**
 * Waits until the worker has handled the install event, which ends with the
 * first-run page opened or found not needed, and asserts it is open exactly
 * once. The pause after that is for Playwright, which reports a tab a moment
 * after Chrome creates it.
 *
 * @param {import("./lib/harness.mjs").ExtensionBrowser} browser
 * @returns {Promise<Page>}
 */
async function theFirstRunTab({ context, extensionId, serviceWorker }) {
  await waitForInstallHandled(serviceWorker);
  await expect.poll(() => firstRunTabs(context, extensionId).length, { message: "the first-run page opened", timeout: NEW_TAB_GRACE_MS }).toBeGreaterThan(0);
  await sleep(1000);
  expect(firstRunTabs(context, extensionId), "first-run tabs").toHaveLength(1);
  return firstRunTabs(context, extensionId)[0];
}

/**
 * Asserts that the worker opened no first-run page: once it has handled the
 * install event, none shows up.
 *
 * @param {import("./lib/harness.mjs").ExtensionBrowser} browser
 */
async function expectNoFirstRunTab({ context, extensionId, serviceWorker }) {
  await waitForInstallHandled(serviceWorker);
  await sleep(NEW_TAB_GRACE_MS);
  expect(firstRunTabs(context, extensionId), "first-run tabs").toHaveLength(0);
}


/**
 * The extension's storage, read from one of its pages.
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @returns {Promise<Record<string, unknown>>}
 */
async function storage(context, extensionId) {
  const page = await context.newPage();
  try {
    await page.goto(extensionUrl(extensionId, "options/options.html"));
    return await page.evaluate(() => chrome.storage.local.get(null));
  } finally {
    await page.close();
  }
}

/**
 * Opens a go link in a new tab, the way a user types one, and closes the tab.
 *
 * @param {BrowserContext} context
 * @param {string} url
 * @returns {Promise<string>} where the tab landed, or "failed"
 */
async function openGoLink(context, url) {
  const tab = await context.newPage();
  try {
    return await tab.goto(url).then(
      () => tab.url(),
      () => "failed",
    );
  } finally {
    await tab.close();
  }
}

test("with the Directory up: it opens once, confirms go/ and a go link, and never opens again", async () => {
  test.setTimeout(120_000);
  const fixture = await startFixture();
  const server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  await createLink({ routes: [{ pattern: "roadmap", destination: fixture.url("/docs/roadmap") }] });
  let browser = await launch();
  try {
    const watch = watchExtensionPages(browser.context, browser.extensionId);
    // Under OGRE_E2E_PORT the page asked the default address at install.
    if (!AT_DEFAULT_ADDRESS) watch.allow(refusedMeta(DEFAULT_DIRECTORY_URL));
    const page = await theFirstRunTab(browser);
    await expect(page.locator("h1")).toHaveText("Set up go links");
    await expect(page.locator("#directory")).toHaveText(DIRECTORY_URL);

    await test.step("step 1: the Directory answers", async () => {
      await expect(page.locator("#step-server-badge")).toHaveText("Done");
      await expect(page.locator("#server-status")).toContainText(`An Ogre server (version 0.1.0-dev) answers at ${DIRECTORY_URL}.`);
      await expect(page.locator("#server-help")).toBeHidden();
      await expect(page.locator("#step-go-badge")).toHaveText("Your turn");
      await expect(page.locator("#step-go-body")).toBeVisible();
      await expect(page.locator("#step-link-badge")).toHaveText("Waiting for step 2");
    });

    await test.step("step 2: go/ is noticed by itself", async () => {
      expect(await openGoLink(browser.context, "http://go/")).toBe(`${DIRECTORY_URL}/-/`);
      await expect(page.locator("#step-go-badge")).toHaveText("Done");
      await expect(page.locator("#go-status")).toContainText("Chrome opened go/, and the extension sent it to your server.");
      await expect(page.locator("#step-link-badge")).toHaveText("Your turn");
      await expect(page.locator("#step-link-body")).toContainText("If Chrome searches for it instead");
      await expect(page.getByRole("button", { name: "Finish" })).toBeVisible();
    });

    await test.step("step 3 waits for a go link, not go/ again", async () => {
      expect(await openGoLink(browser.context, "http://go/")).toBe(`${DIRECTORY_URL}/-/`);
      await expect(page.locator("#step-link-badge")).toHaveText("Your turn");
      await expect(page.locator("#link-status")).toBeEmpty();
    });

    await test.step("step 3: a go link, here upgraded to https as Chrome's strictest setting does", async () => {
      expect(await openGoLink(browser.context, "https://go/roadmap?from=first-run")).toBe(fixture.url("/docs/roadmap"));
      await expect(page.locator("#step-link-badge")).toHaveText("Done");
      await expect(page.locator("#link-status")).toContainText("Chrome opened go/roadmap as a go link. Go links work in this browser.");
      // The query is not repeated: it can hold anything.
      await expect(page.locator("#link-status")).not.toContainText("from=");
    });

    await test.step("opened again, the page remembers step 2", async () => {
      const again = await browser.context.newPage();
      await again.goto(extensionUrl(browser.extensionId, FIRST_RUN_PAGE));
      await expect(again.locator("#step-go-badge")).toHaveText("Done");
      await expect(again.locator("#go-status")).toContainText("Chrome has opened go/ before");
      await expect(again.locator("#step-link-badge")).toHaveText("Your turn");
      await again.close();
    });

    // Seeing go/ ends the first run: the page never opens by itself again.
    expect(await storage(browser.context, browser.extensionId)).toMatchObject({
      [STORAGE_KEYS.firstRunDone]: true,
      [STORAGE_KEYS.firstRunSawGo]: true,
    });

    await test.step("the page is accessible and fits a phone, in both color schemes", async () => {
      for (const colorScheme of /** @type {const} */ (["light", "dark"])) {
        await page.emulateMedia({ colorScheme });
        await expectAccessible(page, `the first-run page (${colorScheme})`);
      }
      await page.setViewportSize({ width: 375, height: 800 });
      await expectNoHorizontalScroll(page, "the first-run page at 375 px");
    });

    await page.getByRole("button", { name: "Finish" }).click();
    await expect(page.locator("#finish-status")).toContainText("All set");
    await expect(page.locator("#finish-status")).toBeFocused();
    await expect(page.getByRole("button", { name: "Finish" })).toBeHidden();
    watch.assertClean();

    await test.step("the next launch opens no first-run page", async () => {
      const profile = browser.userDataDir;
      await browser.close();
      browser = await launch({ userDataDir: profile });
      await expectNoFirstRunTab(browser);
    });
  } finally {
    await browser.close();
    await fixture.close();
    expect(await server.stop(), server.output()).toBe("exit code 0");
  }
});

test("with the Directory down: it says how to start it, holds step 2 back, and opens once per launch until dismissed", async () => {
  test.setTimeout(180_000);
  /** @type {import("./lib/harness.mjs").RunningServer | undefined} */
  let server;
  /** @type {import("./lib/browsers.mjs").PortDirectory | undefined} */
  let serverB;
  /** @type {import("./lib/harness.mjs").Fixture | undefined} */
  let otherProgram;
  let browser = await launch();
  /** @param {import("./lib/harness.mjs").ExtensionBrowser} b */
  const watchPages = (b) => {
    const w = watchExtensionPages(b.context, b.extensionId);
    w.allow(refusedMeta(DIRECTORY_URL));
    if (!AT_DEFAULT_ADDRESS) w.allow(refusedMeta(DEFAULT_DIRECTORY_URL));
    return w;
  };
  try {
    let watch = watchPages(browser);
    let page = await theFirstRunTab(browser);

    await test.step("step 1 says the server is not answering, and how to start it", async () => {
      await expect(page.locator("#step-server-badge")).toHaveText("Not answering");
      await expect(page.locator("#server-status")).toContainText(`Could not connect to ${DIRECTORY_URL}. Is the Ogre server running there?`);
      await expect(page.locator("#server-help")).toBeVisible();
      // The command the README gives.
      await expect(page.locator("#server-help")).toContainText("go run ./cmd/ogre");
      await expect(page.locator("#server-help-other")).toBeHidden();
      await expect(page.getByRole("button", { name: "Check again" })).toBeVisible();
      await expect(page.locator("#step-go-badge")).toHaveText("Waiting for step 1");
      await expect(page.locator("#step-go-body")).toBeHidden();
      await expect(page.getByRole("button", { name: "Finish" })).toBeHidden();
    });

    await test.step("go/ typed meanwhile is not counted", async () => {
      expect(await openGoLink(browser.context, "http://go/")).toBe("failed");
      await expect(page.locator("#go-status")).toContainText("Warning: your server wasn't answering");
      await expect(page.locator("#step-go-badge")).toHaveText("Waiting for step 1");
      const stored = await storage(browser.context, browser.extensionId);
      expect(stored[STORAGE_KEYS.firstRunSawGo]).toBeUndefined();
      expect(stored[STORAGE_KEYS.firstRunDone]).toBeUndefined();
    });
    watch.assertClean();

    await test.step("not done yet: the next launch opens it again, once", async () => {
      const profile = browser.userDataDir;
      await browser.close();
      browser = await launch({ userDataDir: profile });
      watch = watchPages(browser);
      page = await theFirstRunTab(browser);
      await expect(page.locator("#step-server-badge")).toHaveText("Not answering");
    });

    await test.step("a first-run tab restored from the last session gets no second one beside it", async () => {
      // The worker checks for an open first-run page before it opens one.
      // Without that check this launch shows two (tried by removing it).
      const profile = browser.userDataDir;
      await browser.close();
      browser = await launch({ userDataDir: profile, args: ["--restore-last-session"] });
      watch = watchPages(browser);
      page = await theFirstRunTab(browser);
      await expect(page.locator("#step-server-badge")).toHaveText("Not answering");
    });

    const dbPath = path.join(tempDir("ogre-e2e-data-"), "ogre.db");
    /** Stops the test server, and reloads the page so it sees that. */
    const serverDown = async () => {
      expect(await server?.stop(), server?.output()).toBe("exit code 0");
      server = undefined;
      await page.reload();
      await expect(page.locator("#step-server-badge")).toHaveText("Not answering");
    };

    await test.step("once the server runs, coming back to the page moves on to step 2", async () => {
      server = await startServer({ dbPath });
      // What Chrome does when the user switches back to this tab.
      await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
      await expect(page.locator("#step-server-badge")).toHaveText("Done");
      await expect(page.locator("#server-help")).toBeHidden();
      await expect(page.getByRole("button", { name: "Check again" })).toBeHidden();
      await expect(page.locator("#step-go-badge")).toHaveText("Your turn");
    });

    await test.step("so does focusing its window", async () => {
      await serverDown();
      server = await startServer({ dbPath });
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      await expect(page.locator("#step-server-badge")).toHaveText("Done");
    });

    await test.step("an address saved in Settings in another tab is followed without a reload", async () => {
      await serverDown();
      serverB = await startDirectoryOnFreePort();
      expect(await pointExtensionAt(browser.context, browser.extensionId, serverB.url)).toMatchObject({ ok: true });
      await expect(page.locator("#directory")).toHaveText(serverB.url);
      await expect(page.locator("#step-server-badge")).toHaveText("Done");
      await expect(page.locator("#server-status")).toContainText(`answers at ${serverB.url}.`);
    });

    await test.step("an address changed while a check runs: the old answer is dropped, the new address checked", async () => {
      // A server that accepts connections and never answers: the page's
      // check of it runs until its own timeout.
      /** @type {Set<import("node:net").Socket>} */
      const sockets = new Set();
      const hanging = net.createServer((socket) => {
        sockets.add(socket);
      });
      await new Promise((resolve) => hanging.listen(0, "127.0.0.1", () => resolve(undefined)));
      const hangingOrigin = `http://127.0.0.1:${/** @type {import("node:net").AddressInfo} */ (hanging.address()).port}`;
      try {
        expect(await pointExtensionAt(browser.context, browser.extensionId, hangingOrigin)).toMatchObject({ ok: true });
        await expect(page.locator("#directory")).toHaveText(hangingOrigin);
        await expect(page.locator("#step-server-badge")).toHaveText("Checking");
        await page.evaluate(() => {
          const badge = /** @type {HTMLElement} */ (document.getElementById("step-server-badge"));
          const seen = /** @type {string[]} */ ([]);
          /** @type {any} */ (window).badgesSeen = seen;
          new MutationObserver(() => seen.push(badge.textContent ?? "")).observe(badge, { childList: true, characterData: true, subtree: true });
        });
        expect(await pointExtensionAt(browser.context, browser.extensionId, /** @type {string} */ (serverB?.url))).toMatchObject({ ok: true });
        await expect(page.locator("#directory")).toHaveText(/** @type {string} */ (serverB?.url));
        // The check of the hanging address ends after 3 seconds; only then
        // is the new address checked.
        await expect(page.locator("#step-server-badge")).toHaveText("Done", { timeout: 10_000 });
        await expect(page.locator("#server-status")).toContainText(`answers at ${serverB?.url}.`);
        expect(await page.evaluate(() => /** @type {any} */ (window).badgesSeen), "badges shown meanwhile").not.toContain("Not answering");
      } finally {
        for (const socket of sockets) socket.destroy();
        await new Promise((resolve) => {
          hanging.close(() => resolve(undefined));
        });
      }
    });

    await test.step("another program at the address: it says so, and how to use another port", async () => {
      otherProgram = await startFixture();
      expect(await pointExtensionAt(browser.context, browser.extensionId, otherProgram.origin)).toMatchObject({ ok: true });
      await expect(page.locator("#directory")).toHaveText(otherProgram.origin);
      await expect(page.locator("#step-server-badge")).toHaveText("Wrong server");
      await expect(page.locator("#server-status")).toContainText("Error: something else answers there");
      await expect(page.locator("#server-status")).toContainText("it is not an Ogre server");
      await expect(page.locator("#server-help")).toBeHidden();
      await expect(page.locator("#server-help-other")).toBeVisible();
      await expect(page.locator("#server-help-other")).toContainText("go run ./cmd/ogre --addr localhost:4656");
      await expect(page.locator("#step-go-badge")).toHaveText("Waiting for step 1");
    });

    await test.step("back at the test address, Check again moves on once the server runs", async () => {
      expect(await pointExtensionAt(browser.context, browser.extensionId, DIRECTORY_URL)).toMatchObject({ ok: true });
      await expect(page.locator("#directory")).toHaveText(DIRECTORY_URL);
      await expect(page.locator("#step-server-badge")).toHaveText("Not answering");
      await expect(page.locator("#server-help")).toBeVisible();
      server = await startServer({ dbPath });
      await page.getByRole("button", { name: "Check again" }).click();
      await expect(page.locator("#step-server-badge")).toHaveText("Done");
      await expect(page.locator("#step-go-badge")).toHaveText("Your turn");
    });

    await test.step("dismissed, it never opens by itself again", async () => {
      await page.getByRole("button", { name: "Don't show this page again" }).click();
      await expect(page.locator("#finish-status")).toContainText("This page won't open by itself again.");
      const stored = await storage(browser.context, browser.extensionId);
      expect(stored[STORAGE_KEYS.firstRunDone]).toBe(true);
      expect(stored[STORAGE_KEYS.firstRunSawGo]).toBeUndefined();
      watch.assertClean();

      const profile = browser.userDataDir;
      await browser.close();
      browser = await launch({ userDataDir: profile });
      await expectNoFirstRunTab(browser);
    });
  } finally {
    await browser.close();
    await otherProgram?.close();
    if (serverB) expect(await serverB.stop(), serverB.output()).toBe("exit code 0");
    if (server) expect(await server.stop(), server.output()).toBe("exit code 0");
  }
});
