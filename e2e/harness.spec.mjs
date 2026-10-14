// Negative controls for the checks every UI test relies on (lib/ui.mjs): the
// watch for content security policy violations and script errors, and the
// accessibility scan. A check that silently stopped reporting would leave
// every other test green while the product broke the rule it guards, so each
// one is shown here to fail on a page that breaks it on purpose.
//
// Needs port 4655 free: the server under test listens there.

import { expect, test } from "@playwright/test";
import path from "node:path";

import { DIRECTORY_URL, buildServer, startServer, tempDir } from "./lib/harness.mjs";
import { expectAccessible, launchPlainBrowser, watchPolicy } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("@playwright/test").Browser | undefined} */
let browser;

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  browser = await launchPlainBrowser();
});

test.afterAll(async () => {
  const results = await Promise.allSettled([browser?.close(), server?.stop()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
  const ended = results[1].status === "fulfilled" ? results[1].value : undefined;
  if (server) expect(ended, server.output()).toBe("exit code 0");
});

test("the policy watch reports inline script, inline style and markup sinks", async () => {
  if (!browser) throw new Error("the browser did not start");
  const context = await browser.newContext();
  try {
    const watch = await watchPolicy(context);
    const page = await context.newPage();
    await page.goto(`${DIRECTORY_URL}/-/`);
    await expect(page.locator("#q")).toBeVisible();
    expect(watch.problems, "a clean page").toEqual([]);

    // A style attribute, which the policy forbids.
    await page.evaluate(() => document.body.setAttribute("style", "outline: 0"));
    // An image from another site, which img-src forbids.
    await page.evaluate(() => {
      const img = document.createElement("img");
      img.src = "https://evil.example/pixel.png";
      document.body.appendChild(img);
    });
    await expect.poll(() => watch.problems.join("\n")).toMatch(/CSP violation: img-src/);
    expect(watch.problems.join("\n")).toMatch(/CSP violation: style-src/);
    expect(watch.problems.join("\n")).not.toMatch(/require-trusted-types-for/);

    // A string turned into markup: Trusted Types refuses every such sink,
    // whichever one the code reaches for, this one included, which the
    // source check (make dom-guard) also bans.
    await page.evaluate(() => {
      try {
        document.createRange().createContextualFragment("<b>markup</b>");
      } catch {
        // The violation is what counts; the exception is the browser's.
      }
    });
    await expect.poll(() => watch.problems.join("\n")).toMatch(/CSP violation: require-trusted-types-for/);

    // An inline script: refused, and reported, before it can run.
    const before = watch.problems.length;
    await page.addScriptTag({ content: "window.inlineRan = true;" }).catch(() => {});
    await expect.poll(() => watch.problems.slice(before).join("\n")).toMatch(/CSP violation: (script-src|require-trusted-types-for)/);
    expect(await page.evaluate(() => /** @type {any} */ (window).inlineRan)).toBeUndefined();
  } finally {
    await context.close();
  }
});

test("the policy watch reports an uncaught error and a console error", async () => {
  if (!browser) throw new Error("the browser did not start");
  const context = await browser.newContext();
  try {
    const watch = await watchPolicy(context);
    const page = await context.newPage();
    await page.goto(`${DIRECTORY_URL}/-/`);
    await page.evaluate(() => {
      console.error("something broke");
      setTimeout(() => {
        throw new Error("nobody caught this");
      }, 0);
    });
    await expect.poll(() => watch.problems.join("\n")).toMatch(/uncaught on .*nobody caught this/);
    expect(watch.problems.join("\n")).toMatch(/error on .*something broke/);
  } finally {
    await context.close();
  }
});

test("the accessibility scan fails on a serious issue", async () => {
  if (!browser) throw new Error("the browser did not start");
  const context = await browser.newContext();
  try {
    const page = await context.newPage();
    await page.goto(`${DIRECTORY_URL}/-/`);
    await expectAccessible(page, "home as served");
    await page.evaluate(() => document.documentElement.removeAttribute("lang"));
    await expect(expectAccessible(page, "home without a language")).rejects.toThrow(/html-has-lang \(serious\)/);
  } finally {
    await context.close();
  }
});
