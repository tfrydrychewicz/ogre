// The extension's settings page with two real Directories (plan L4.2, L4.5;
// EXT-2, EXT-4): saving another address re-points the redirect rule, an
// address that cannot be used is refused with the rule left alone, one that
// does not answer is saved only after a warning, and access to an address no
// longer used is given up.
//
// Directory A is the harness's (DIRECTORY_URL, the extension's default unless
// OGRE_E2E_PORT moves it); Directory B listens on a free port of its own.
// Each has a link `where` that leads to a different page of one fixture, so
// where a go link lands says which Directory answered it.
//
// The one thing a test cannot do is answer Chrome's permission prompt.
// approveHostAccess (lib/harness.mjs) records the user's "Allow" in advance
// through Chrome's own extensions page; the settings page's request then
// resolves at once, as after a real click. Deny is played by replacing the
// page's chrome.permissions.request with one that answers false, which is
// what Chrome's answers. The prompt itself, and a real Deny, are on the
// manual list (docs/manual-tests.md).

import { expect, test } from "@playwright/test";
import path from "node:path";

import { DEFAULT_DIRECTORY_URL, REQUIRED_HOST_PATTERNS, STORAGE_KEYS } from "../extension/lib/config.js";
import { freeLoopbackPort, startDirectoryOnFreePort } from "./lib/browsers.mjs";
import {
  AT_DEFAULT_ADDRESS,
  DIRECTORY_URL,
  approveHostAccess,
  buildServer,
  createLink,
  dynamicRules,
  extensionUrl,
  launchWithExtension,
  setDirectoryThroughWorker,
  startFixture,
  startServer,
  tempDir,
  waitForRedirectRule,
} from "./lib/harness.mjs";
import { expectAccessible, expectNoHorizontalScroll, refusedMeta, watchExtensionPages } from "./lib/ui.mjs";

/** @typedef {import("@playwright/test").Page} Page */

test.beforeAll(() => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
});

test("saving another Directory re-points go links, and saving back releases its access", async () => {
  test.setTimeout(120_000);
  const fixture = await startFixture();
  const serverA = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  const serverB = await startDirectoryOnFreePort();
  const down = `http://localhost:${await freeLoopbackPort()}`; // nothing listens there
  const browser = await launchWithExtension();
  /** @type {import("./lib/harness.mjs").ExtensionBrowser | undefined} */
  let relaunched;
  try {
    const { context, extensionId, serviceWorker } = browser;
    await createLink({ routes: [{ pattern: "where", destination: fixture.url("/from-a") }] }, serverA.url);
    await createLink({ routes: [{ pattern: "where", destination: fixture.url("/from-b") }] }, serverB.url);
    await createLink({ routes: [{ pattern: "files/{path}", destination: fixture.url("/b-files/{path}") }] }, serverB.url);

    const watch = watchExtensionPages(context, extensionId);
    // Asking the address where nothing listens is expected to fail.
    watch.allow(refusedMeta(down));

    /** The settings page's view of the stored state, and Chrome's. */
    const state = async () => {
      const page = await context.newPage();
      try {
        await page.goto(extensionUrl(extensionId, "options/options.html"));
        return await page.evaluate(async (key) => ({
          stored: (await chrome.storage.local.get(key))[key],
          origins: ((await chrome.permissions.getAll()).origins ?? []).slice().sort(),
        }), STORAGE_KEYS.directoryUrl);
      } finally {
        await page.close();
      }
    };
    /** Opens go/where in a new tab and says where it landed, or which request failed and how. */
    const where = async () => {
      const tab = await context.newPage();
      /** @type {string[]} */
      const failed = [];
      tab.on("requestfailed", (request) => failed.push(`${request.url()} ${request.failure()?.errorText}`));
      try {
        return await tab.goto("http://go/where").then(
          () => tab.url(),
          () => `failed: ${failed.join(", ")}`,
        );
      } finally {
        await tab.close();
      }
    };

    expect(await where()).toBe(fixture.url("/from-a"));

    const page = await context.newPage();
    await page.goto(extensionUrl(extensionId, "options/options.html"));
    const field = page.getByLabel("Directory URL");
    const result = page.locator("#result");
    await expect(page.locator("#current")).toHaveText(DIRECTORY_URL);
    await expect(field).toHaveValue(DIRECTORY_URL);

    await test.step("an address that cannot be used is refused, and nothing changes", async () => {
      const portB = new URL(serverB.url).port;
      for (const [typed, message] of [
        [`localhost:${portB}`, `Start the address with http:// or https://, for example http://localhost:${portB}.`],
        [`${serverB.url}/-/`, "Leave out everything after the server's name and port"],
        ["javascript:alert(1)", "The address must start with http:// or https://, not javascript:."],
        [`http://user:secret@localhost:${portB}`, "Leave out the user name and password"],
        ["http://go:4655", "The Directory cannot be at the host go"],
      ]) {
        await field.fill(typed);
        await page.getByRole("button", { name: "Save", exact: true }).click();
        await expect(field, typed).toHaveAttribute("aria-invalid", "true");
        await expect(page.locator("#url-error"), typed).toContainText(message);
      }
      expect(await dynamicRules(serviceWorker)).toHaveLength(1);
      await waitForRedirectRule(serviceWorker, DIRECTORY_URL, 1000);
      expect((await state()).stored).toBe(AT_DEFAULT_ADDRESS ? undefined : DIRECTORY_URL);
    });

    await test.step("the worker refuses an address the user did not allow", async () => {
      const reply = await setDirectoryThroughWorker(context, extensionId, serverB.url);
      expect(reply).toEqual({
        ok: false,
        message: `Chrome has not given the extension access to ${serverB.url}, so nothing was changed. Save again and allow access when Chrome asks.`,
      });
      await waitForRedirectRule(serviceWorker, DIRECTORY_URL, 1000);
    });

    await test.step("saving Directory B, typed loosely, re-points go links there", async () => {
      await approveHostAccess(context, extensionId, serverB.url);
      await field.fill(`  HTTP://LocalHost:${new URL(serverB.url).port}/ `);
      // By keyboard: focus stays on Save while the buttons are disabled
      // and come back.
      await page.locator("#save").focus();
      await page.keyboard.press("Enter");
      await expect(result.locator(".notice-success")).toContainText(`Go links now go to ${serverB.url}.`);
      await expect(page.locator("#save")).toBeFocused();
      await expect(result).toContainText(`An Ogre server, version 0.1.0-dev, answers at ${serverB.url}.`);
      await expect(field).toHaveValue(serverB.url);
      await expect(page.locator("#current")).toHaveText(serverB.url);
      await waitForRedirectRule(serviceWorker, serverB.url, 5000);
      const after = await state();
      expect(after.stored).toBe(serverB.url);
      expect(after.origins).toContain(`${serverB.url}/*`);

      expect(await where()).toBe(fixture.url("/from-b"));
      // Path, query and encoding come through a changed Directory too.
      const tab = await context.newPage();
      const response = await tab.goto("http://go/files/a%2Fb?x=1#frag");
      expect(tab.url()).toBe(fixture.url("/b-files/a%2Fb#frag"));
      expect(response?.request().redirectedFrom()?.url()).toBe(`${serverB.url}/files/a%2Fb?x=1`);
      await tab.close();

      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(result.locator(".notice-success")).toContainText("This is the saved address.");
    });

    await test.step("when the user denies access, nothing is saved", async () => {
      // Another origin for the same server: one Chrome has not been asked about.
      const other = `http://127.0.0.1:${new URL(serverB.url).port}`;
      await page.evaluate(() => {
        chrome.permissions.request = async () => false;
      });
      await field.fill(other);
      await page.getByRole("button", { name: "Save", exact: true }).click();
      await expect(result.locator(".notice-error")).toContainText("Error: access not given");
      await expect(result).toContainText(`The extension may not contact ${other} until you allow it.`);
      await expect(page.locator("#current")).toHaveText(serverB.url);
      await waitForRedirectRule(serviceWorker, serverB.url, 1000);
      expect((await state()).stored).toBe(serverB.url);

      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(result.locator(".notice-error")).toContainText("Error: access not given");
      expect((await state()).origins).not.toContain(`${other}/*`);
      await page.reload(); // Chrome's own request again
    });

    await test.step("access withdrawn outside the page is reported, and saving again restores it", async () => {
      const other = await context.newPage();
      await other.goto(extensionUrl(extensionId, "options/options.html"));
      expect(await other.evaluate((pattern) => chrome.permissions.remove({ origins: [pattern] }), `${serverB.url}/*`)).toBe(true);
      await other.close();
      await page.reload();
      await expect(result.locator(".notice-warning")).toContainText("Warning: access withdrawn");
      await expect(result).toContainText(`The extension may no longer contact ${serverB.url}. Go links still go there`);
      // Go links still go there: the rule needs no host access to redirect.
      await waitForRedirectRule(serviceWorker, serverB.url, 1000);
      expect(await where()).toBe(fixture.url("/from-b"));

      await approveHostAccess(context, extensionId, serverB.url);
      await page.getByRole("button", { name: "Save", exact: true }).click();
      await expect(result.locator(".notice-success")).toContainText(`Go links now go to ${serverB.url}.`);
      expect((await state()).origins).toContain(`${serverB.url}/*`);
      await page.reload();
      await expect(result).toBeEmpty();
    });

    await test.step("an address where nothing answers is saved only after a warning", async () => {
      await approveHostAccess(context, extensionId, down);
      await field.fill(down);
      await page.locator("#save").focus();
      await page.keyboard.press("Enter");
      const warning = page.locator("#unreachable");
      await expect(warning).toBeVisible();
      await expect(warning).toContainText(`Could not connect to ${down}. Is the Ogre server running there?`);
      // The keyboard user lands on the warning's first choice.
      await expect(page.getByRole("button", { name: "Save anyway" })).toBeFocused();
      await waitForRedirectRule(serviceWorker, serverB.url, 1000); // not saved yet

      await page.keyboard.press("Enter");
      await expect(warning).toBeHidden();
      await expect(result.locator(".notice-success")).toContainText(`Go links now go to ${down}.`);
      await expect(field).toBeFocused();
      await expect(result).toContainText("No Ogre server answered there when you saved.");
      await expect(result).toContainText(`Access the extension no longer needs was given up: ${serverB.url}.`);
      await waitForRedirectRule(serviceWorker, down, 5000);
      const after = await state();
      expect(after.stored).toBe(down);
      expect(after.origins).not.toContain(`${serverB.url}/*`);
      expect(after.origins).toContain(`${down}/*`);

      expect(await where()).toBe(`failed: ${down}/where net::ERR_CONNECTION_REFUSED`);
    });

    await test.step("saving Directory A again releases the other access", async () => {
      if (AT_DEFAULT_ADDRESS) {
        await page.getByRole("button", { name: "Use the default address" }).click();
        await expect(field).toHaveValue(DEFAULT_DIRECTORY_URL);
      } else {
        await approveHostAccess(context, extensionId, DIRECTORY_URL);
        await field.fill(DIRECTORY_URL);
      }
      await page.getByRole("button", { name: "Save", exact: true }).click();
      await expect(result.locator(".notice-success")).toContainText(`Go links now go to ${DIRECTORY_URL}.`);
      await expect(result).toContainText(`given up: ${down}.`);
      await waitForRedirectRule(serviceWorker, DIRECTORY_URL, 5000);
      const after = await state();
      expect(after.stored).toBe(DIRECTORY_URL);
      // Only the manifest's own hosts are left (and A, when the tests run
      // with A away from the default).
      const expected = AT_DEFAULT_ADDRESS ? [...REQUIRED_HOST_PATTERNS] : [...REQUIRED_HOST_PATTERNS, `${DIRECTORY_URL}/*`];
      expect(after.origins).toEqual(expected.sort());

      expect(await where()).toBe(fixture.url("/from-a"));
    });

    await test.step("the page is accessible and fits a phone, in both color schemes", async () => {
      for (const colorScheme of /** @type {const} */ (["light", "dark"])) {
        await page.emulateMedia({ colorScheme });
        await expectAccessible(page, `the settings page (${colorScheme})`);
      }
      await page.setViewportSize({ width: 375, height: 800 });
      await expectNoHorizontalScroll(page, "the settings page at 375 px");
    });

    watch.assertClean();

    await test.step("access allowed only for a test is given up when the browser starts again", async () => {
      const before = (await state()).origins;
      await page.setViewportSize({ width: 1280, height: 800 });
      await approveHostAccess(context, extensionId, serverB.url);
      await field.fill(serverB.url);
      await page.getByRole("button", { name: "Test connection" }).click();
      await expect(result.locator(".notice-success")).toContainText("Save to send go links there.");
      expect((await state()).origins).toContain(`${serverB.url}/*`);

      // Chromium with --load-extension reports an install at every launch,
      // which is when the worker gives unused access up, as at a browser start.
      await browser.close();
      // Not pointed anywhere by the harness: a save would give the access
      // up too, and hide whether the start did.
      relaunched = await launchWithExtension({ userDataDir: browser.userDataDir, directory: DEFAULT_DIRECTORY_URL, waitForRule: false });
      const again = await relaunched.context.newPage();
      await again.goto(extensionUrl(relaunched.extensionId, "options/options.html"));
      const origins = await again.evaluate(async () => ((await chrome.permissions.getAll()).origins ?? []).slice().sort());
      expect(origins).toEqual(before);
      await expect(again.locator("#current")).toHaveText(DIRECTORY_URL);
    });
  } finally {
    await relaunched?.close();
    await browser.close();
    await fixture.close();
    const ended = await Promise.all([serverA.stop(), serverB.stop()]);
    expect(ended).toEqual(["exit code 0", "exit code 0"]);
  }
});
