// Which Sec-Fetch-Site the Directory receives when a tab reaches it through
// the extension, and what the create and edit forms do with it (plan L4.5).
//
// Why it matters: the forms take routes from the address (?pattern= and
// ?destination=, DIR-7) and treat them as another site's unless the request
// says Sec-Fetch-Site "same-origin" or "none" (L3.6). The edit form then
// leaves them out, the create form keeps them under a warning. A way into the
// Directory that real users take and that arrived as "cross-site" would
// cost them a legitimate prefill.
//
// What was observed, in Playwright's Chromium, and is pinned below:
//
//   go link opened as typed (page.goto, a browser-initiated navigation),
//     then redirected by the extension's rule         -> "none"
//   chrome.tabs.create from an extension page, with a go link or with the
//     Directory's own address                          -> "none"
//   a link clicked on an extension page, to a go link
//     or to the Directory                              -> "cross-site"
//   (a go link clicked on another site's page          -> "cross-site",
//     pinned by redirect.spec.mjs)
//
// So the redirect rule does not change the value: a typed go link stays the
// user's own, and the forms keep its prefill without a warning. A link on an
// extension page counts as another site's. No extension page links to the
// Directory today; a page that needs to open the forms prefilled (the popup
// of L6.7, "Create a go link for this page") must open the tab with
// chrome.tabs.create, which arrives as "none". The form rule is unchanged.
//
// page.goto stands in for typing in the address bar, which Playwright cannot
// do. Both are navigations the browser starts without an initiating page;
// that typing gives the same value is on the manual list.

import { expect, test } from "@playwright/test";
import path from "node:path";

import {
  DIRECTORY_URL,
  buildServer,
  createLink,
  extensionUrl,
  launchWithExtension,
  pointExtensionAt,
  startFixture,
  startServer,
  tempDir,
} from "./lib/harness.mjs";

/** @typedef {import("@playwright/test").Page} Page */
/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */

/** A prefill as another site, or the user, would write it. */
const PREFILL = "pattern=probe%2F%7Bid%7D&destination=https%3A%2F%2Fexample.com%2Fprobe%2F%7Bid%7D";

test.beforeAll(() => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
});

/**
 * Opens `href` from one of the extension's pages and resolves with the tab
 * it ends in, once that tab has left the extension page.
 *
 * @param {BrowserContext} context
 * @param {Page} extensionPage an open page of the extension
 * @param {"click" | "click-same-tab" | "tabs.create"} how a real click on a link the page holds
 *   (opening a new tab, or this one), or the extension API a page or the worker would call
 * @param {string} href
 * @returns {Promise<Page>}
 */
async function openFromExtensionPage(context, extensionPage, how, href) {
  if (how === "tabs.create") {
    const opened = context.waitForEvent("page");
    await extensionPage.evaluate((url) => chrome.tabs.create({ url }), href);
    return opened;
  }
  await extensionPage.evaluate(
    /** @param {[string, boolean]} args */
    ([url, newTab]) => {
      document.getElementById("probe-link")?.remove();
      const link = document.createElement("a");
      link.id = "probe-link";
      link.href = url;
      link.textContent = "probe";
      if (newTab) link.target = "_blank";
      document.body.prepend(link);
    },
    /** @type {[string, boolean]} */ ([href, how === "click"]),
  );
  if (how === "click-same-tab") {
    await extensionPage.click("#probe-link");
    return extensionPage;
  }
  const opened = context.waitForEvent("page");
  await extensionPage.click("#probe-link");
  return opened;
}

test("the Sec-Fetch-Site a Directory receives through the extension", async () => {
  // A stand-in Directory that records each request as it arrived on the
  // wire. The extension is pointed at it like at any Directory.
  const standIn = await startFixture();
  const browser = await launchWithExtension({ directory: standIn.origin });
  try {
    const { context, extensionId } = browser;
    /** @param {string} pathAndQuery */
    const received = async (pathAndQuery) => {
      await expect.poll(() => standIn.requestsFor(pathAndQuery).length, { message: `the Directory received ${pathAndQuery}` }).toBeGreaterThan(0);
      const headers = /** @type {import("node:http").IncomingHttpHeaders} */ (standIn.requestsFor(pathAndQuery).at(-1)?.headers);
      return { site: headers["sec-fetch-site"], mode: headers["sec-fetch-mode"], dest: headers["sec-fetch-dest"] };
    };
    const navigation = { mode: "navigate", dest: "document" };

    await test.step("a go link opened as typed", async () => {
      const page = await context.newPage();
      await page.goto(`http://go/typed?${PREFILL}`);
      expect(page.url()).toBe(`${standIn.origin}/typed?${PREFILL}`);
      expect(await received(`/typed?${PREFILL}`)).toEqual({ site: "none", ...navigation });
      await page.close();
    });

    const extensionPage = await context.newPage();
    await extensionPage.goto(extensionUrl(extensionId, "options/options.html"));

    /** @type {[string, "click" | "click-same-tab" | "tabs.create", string, string][]} label, how, href, expected */
    const cases = [
      ["chrome.tabs.create with a go link", "tabs.create", "http://go/created-go", "none"],
      ["chrome.tabs.create with the Directory's address", "tabs.create", `${standIn.origin}/created-direct`, "none"],
      ["a go link clicked on an extension page", "click", "http://go/clicked-go", "cross-site"],
      ["the Directory's address clicked on an extension page", "click", `${standIn.origin}/clicked-direct`, "cross-site"],
      ["the Directory's address clicked on an extension page, same tab", "click-same-tab", `${standIn.origin}/clicked-same-tab`, "cross-site"],
    ];
    for (const [label, how, href, expected] of cases) {
      await test.step(label, async () => {
        const target = href.startsWith("http://go/") ? href.slice("http://go".length) : href.slice(standIn.origin.length);
        const tab = await openFromExtensionPage(context, extensionPage, how, `${href}?${PREFILL}`);
        await tab.waitForURL(`${standIn.origin}${target}?*`);
        expect(await received(`${target}?${PREFILL}`), label).toEqual({ site: expected, ...navigation });
        if (tab === extensionPage) await extensionPage.goto(extensionUrl(extensionId, "options/options.html"));
        else await tab.close();
      });
    }
  } finally {
    await browser.close();
    await standIn.close();
  }
});

test("the forms keep a typed go link's prefill and flag an extension page's link, also when handed over", async () => {
  const server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  const browser = await launchWithExtension();
  try {
    const { context, extensionId } = browser;
    await createLink({ routes: [{ pattern: "probe", destination: "https://example.com/probe" }] });

    await test.step("typed go/-/new?…: prefilled, no warning", async () => {
      const page = await context.newPage();
      await page.goto(`http://go/-/new?${PREFILL}`);
      expect(page.url()).toBe(`${DIRECTORY_URL}/-/new?${PREFILL}`);
      await expect(page.getByLabel("Pattern").first()).toHaveValue("probe/{id}");
      await expect(page.locator("#foreign-prefill")).toHaveCount(0);
      await page.close();
    });

    await test.step("typed go/-/links/probe/edit?…: the route is added", async () => {
      const page = await context.newPage();
      await page.goto(`http://go/-/links/probe/edit?${PREFILL}`);
      await expect(page.getByLabel("Pattern")).toHaveCount(2);
      await expect(page.getByLabel("Pattern").nth(1)).toHaveValue("probe/{id}");
      await expect(page.locator("#ignored-prefill")).toHaveCount(0);
      await page.close();
    });

    const extensionPage = await context.newPage();
    await extensionPage.goto(extensionUrl(extensionId, "options/options.html"));

    await test.step("chrome.tabs.create: prefilled, no warning", async () => {
      const tab = await openFromExtensionPage(context, extensionPage, "tabs.create", `${DIRECTORY_URL}/-/new?${PREFILL}`);
      await tab.waitForURL(`${DIRECTORY_URL}/-/new?*`);
      await expect(tab.getByLabel("Pattern").first()).toHaveValue("probe/{id}");
      await expect(tab.locator("#foreign-prefill")).toHaveCount(0);
      await tab.close();
    });

    await test.step("a link on an extension page: the create form warns, the edit form leaves it out", async () => {
      const create = await openFromExtensionPage(context, extensionPage, "click", `${DIRECTORY_URL}/-/new?${PREFILL}`);
      await create.waitForURL(`${DIRECTORY_URL}/-/new?*`);
      await expect(create.locator("#foreign-prefill")).toBeVisible();
      await expect(create.getByLabel("Pattern").first()).toHaveValue("probe/{id}");

      // go/probe exists, so the form offers to add the route to it. That
      // opens the edit form from the Directory's own page ("same-origin"),
      // and the routes another site filled in must not lose their warning
      // on the way: one more click on Save would add them to a working link.
      await create.locator("#name-status").getByRole("button", { name: "Add this route to go/probe" }).click();
      await create.waitForURL(`${DIRECTORY_URL}/-/links/probe/edit?*`);
      expect(new URL(create.url()).searchParams.get("prefill")).toBe("foreign");
      await expect(create.getByLabel("Pattern")).toHaveCount(2);
      await expect(create.getByLabel("Pattern").nth(1)).toHaveValue("probe/{id}");
      const warning = create.locator("#foreign-prefill");
      await expect(warning).toBeVisible();
      await expect(warning).toContainText("Warning: filled in by another site");
      await expect(warning).toContainText("Opens a page on example.com");
      await create.close();

      const edit = await openFromExtensionPage(context, extensionPage, "click", `${DIRECTORY_URL}/-/links/probe/edit?${PREFILL}`);
      await edit.waitForURL(`${DIRECTORY_URL}/-/links/probe/edit?*`);
      await expect(edit.locator("#ignored-prefill")).toBeVisible();
      await expect(edit.getByLabel("Pattern")).toHaveCount(1);
      await edit.close();
    });
  } finally {
    await browser.close();
    const ended = await server.stop();
    expect(ended, server.output()).toBe("exit code 0");
  }
});
