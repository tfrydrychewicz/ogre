// The web UI's qualities that hold across pages (plan L3.11, NFR-11, NFR-20,
// NFR-21): the accessibility scan on every page in both color schemes, a
// phone's width without sideways scrolling, the reading pages without
// JavaScript, and search and launch by keyboard alone.
//
// These open the Directory at its own address in Playwright's bundled
// Chromium without the extension; the extension's part is covered by the
// scenarios. Every page opened here also fails the test on a content
// security policy violation or a script error (lib/ui.mjs).
//
// Needs port 4655 free (or the port OGRE_E2E_PORT names): the server under
// test listens there.

import { expect, test } from "@playwright/test";
import path from "node:path";

import { DIRECTORY_URL, buildServer, createLink, startFixture, startServer, tempDir } from "./lib/harness.mjs";
import { expectAccessible, expectNoHorizontalScroll, launchPlainBrowser, watchPolicy } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("@playwright/test").Browser | undefined} */
let browser;

/**
 * The pages, with what must be on screen before a page counts as ready: the
 * form pages are built by their module from the validate endpoint's answer.
 * status is the document's own status.
 *
 * @type {{ label: string, path: string, status: number, ready: string }[]}
 */
const PAGES = [
  { label: "home", path: "/-/", status: 200, ready: "#yours-title" },
  { label: "home with a search", path: "/-/?q=report", status: 200, ready: "#results .result" },
  { label: "new link form", path: "/-/new", status: 200, ready: "#routes > li" },
  {
    label: "new link form with variables",
    path: `/-/new?pattern=${encodeURIComponent("jira/{key:regex([A-Z]+-[0-9]+)}/{n:int}")}&destination=${encodeURIComponent("https://acme.atlassian.net/browse/{key}?n={n}")}`,
    status: 200,
    ready: ".variable",
  },
  { label: "edit form", path: "/-/links/reports/edit", status: 200, ready: ".variable" },
  { label: "link page", path: "/-/links/reports", status: 200, ready: "#launcher input" },
  { label: "link page with several routes", path: "/-/links/gh", status: 200, ready: "#launcher fieldset" },
  { label: "delete confirmation", path: "/-/links/reports/delete", status: 200, ready: "h1" },
  { label: "setup", path: "/-/setup", status: 200, ready: "h1" },
  { label: "miss page, unknown name", path: "/reprots/10/13", status: 404, ready: ".miss-suggestions" },
  { label: "miss page, unknown name without suggestions", path: "/xyzzy", status: 404, ready: ".miss-create" },
  { label: "miss page, known name", path: "/reports/13/01", status: 404, ready: ".miss-reason" },
  { label: "link page of an unknown link", path: "/-/links/nope", status: 404, ready: "h1" },
];

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  fixture = await startFixture();

  // The links of PRD 5.7, with the fixture as their destination.
  await createLink({
    description: "Daily report for one day",
    routes: [
      {
        pattern: "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
        destination: fixture.url("/daily?month={month}&day={day}"),
        variables: { month: { description: "Month, 1 to 12", example: "10" }, day: { description: "Day of the month", example: "13" } },
      },
    ],
  });
  await createLink({
    description: "GitHub shortcuts",
    routes: [
      { pattern: "gh", destination: fixture.url("/acme") },
      { pattern: "gh/new", destination: fixture.url("/organizations/acme/repositories/new") },
      { pattern: "gh/{repo}", destination: fixture.url("/acme/{repo}") },
      { pattern: "gh/{repo}/pr/{num:int}", destination: fixture.url("/acme/{repo}/pull/{num}") },
    ],
  });
  await createLink({ routes: [{ pattern: "jira/{key:regex([A-Z]+-[0-9]+)}", destination: fixture.url("/browse/{key}") }] });
  await createLink({ routes: [{ pattern: "search/{q}", destination: fixture.url("/search?q={q}") }] });
  await createLink({ description: "Who is on call", routes: [{ pattern: "on-call", destination: fixture.url("/schedule") }] });
  await createLink({ description: "Product roadmap", routes: [{ pattern: "roadmap", destination: fixture.url("/docs/roadmap") }] });

  browser = await launchPlainBrowser();
});

test.afterAll(async () => {
  const results = await Promise.allSettled([browser?.close(), fixture?.close(), server?.stop()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
  const ended = results[2].status === "fulfilled" ? results[2].value : undefined;
  if (server) expect(ended, server.output()).toBe("exit code 0");
});

test.afterEach(async ({}, testInfo) => {
  if (testInfo.status !== testInfo.expectedStatus && server) {
    await testInfo.attach("ogre output", { body: server.output() || "(none)", contentType: "text/plain" });
    console.log(`--- ogre output ---\n${server.output() || "(none)"}\n--- end of ogre output ---`);
  }
});

/**
 * A new browser context with the policy watch on, handed to `use` and
 * closed afterwards; the watch must have seen nothing.
 *
 * @param {import("@playwright/test").BrowserContextOptions} options
 * @param {(context: import("@playwright/test").BrowserContext, watch: Awaited<ReturnType<typeof watchPolicy>>) => Promise<void>} use
 */
async function withContext(options, use) {
  if (!browser) throw new Error("the browser did not start");
  const context = await browser.newContext(options);
  try {
    const watch = await watchPolicy(context);
    for (const page of PAGES) if (page.status !== 200) watch.allowStatus(DIRECTORY_URL + page.path);
    await use(context, watch);
    watch.assertClean();
  } finally {
    await context.close();
  }
}

/**
 * Opens one of PAGES and waits until it is ready.
 *
 * @param {import("@playwright/test").Page} page
 * @param {(typeof PAGES)[number]} entry
 */
async function openPage(page, entry) {
  const response = await page.goto(DIRECTORY_URL + entry.path);
  expect(response?.status(), entry.label).toBe(entry.status);
  await expect(page.locator(entry.ready).first(), `${entry.label} is ready`).toBeVisible();
  // The form and the launcher are built by their modules: the form from the
  // validate endpoint's first answer. Each marks its root data-ready once it
  // has drawn that; nothing on the page changes after it without input.
  if ((await page.locator("#link-form, #launcher").count()) > 0) {
    await expect(page.locator("#link-form[data-ready], #launcher[data-ready]").first(), `${entry.label} is drawn`).toBeAttached();
  }
}

for (const colorScheme of /** @type {const} */ (["light", "dark"])) {
  test(`every page passes the accessibility scan (${colorScheme})`, async () => {
    await withContext({ colorScheme, viewport: { width: 1280, height: 900 } }, async (context) => {
      const page = await context.newPage();
      for (const entry of PAGES) {
        await test.step(entry.label, async () => {
          await openPage(page, entry);
          await expectAccessible(page, entry.label);
        });
      }
    });
  });
}

test("at 375 pixels wide no page scrolls sideways", async () => {
  for (const colorScheme of /** @type {const} */ (["light", "dark"])) {
    await withContext({ colorScheme, viewport: { width: 375, height: 800 }, isMobile: true, hasTouch: true }, async (context) => {
      const page = await context.newPage();
      for (const entry of PAGES) {
        await test.step(`${entry.label} (${colorScheme})`, async () => {
          await openPage(page, entry);
          await expectNoHorizontalScroll(page, entry.label);
        });
      }
      // The launcher's answer, a long URL, also fits.
      await openPage(page, PAGES.find((entry) => entry.label === "link page") ?? PAGES[0]);
      await page.locator("#launcher").getByLabel("month").fill("10");
      await page.locator("#launcher").getByLabel("day").fill("13");
      await expect(page.locator("#launcher .launcher-output")).toContainText(fixture.url("/daily?month=10&day=13"));
      await expectNoHorizontalScroll(page, "link page with the launcher's answer");
    });
  }
});

test("the reading pages work without JavaScript", async () => {
  await withContext({ javaScriptEnabled: false }, async (context) => {
    const page = await context.newPage();

    await test.step("home with a search", async () => {
      await page.goto(`${DIRECTORY_URL}/-/?q=${encodeURIComponent("daily report")}`);
      await expect(page.locator("#search-status")).toHaveText("1 link matches.");
      const result = page.locator("#results").getByRole("link", { name: "go/reports" });
      await expect(result).toHaveAttribute("href", "/-/links/reports#launch");
      await expect(page.locator("#results .pattern-var")).toHaveText(["{month:regex(^(0?[1-9]|1[0-2])$)}", "{day:int}"]);
      await expect(page.locator("#home-lists")).toBeHidden();
      // What needs a script stays out of sight, and nothing promises
      // results as you type.
      await expect(page.locator(".js-only")).toBeHidden();
      await expect(page.locator("#q-help")).not.toContainText("as you type", { useInnerText: true });
    });

    await test.step("home", async () => {
      await page.goto(`${DIRECTORY_URL}/-/`);
      await expect(page.getByRole("heading", { name: "Your links" })).toBeVisible();
      await expect(page.locator("#home-lists").getByRole("link", { name: "go/roadmap" }).first()).toBeVisible();
    });

    await test.step("link page", async () => {
      await page.goto(`${DIRECTORY_URL}/-/links/reports`);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText("go/reports");
      await expect(page.getByRole("main")).toContainText("Daily report for one day");
      await expect(page.locator(".route-destination")).toHaveText(fixture.url("/daily?month={month}&day={day}"));
      const variables = page.getByRole("region", { name: /^Variables of go\/reports/ });
      await expect(variables.getByRole("row", { name: /^month\b/ })).toContainText("Month, 1 to 12");
      await expect(variables.getByRole("row", { name: /^day\b/ })).toContainText("a whole number");
      // Playwright's text matchers skip <noscript>, so the notice inside it
      // is asked for by itself: rendered and visible, as scripts are off.
      const launcherNotice = page.locator("#launcher noscript .notice");
      await expect(launcherNotice).toBeVisible();
      await expect(launcherNotice).toContainText("The launcher needs JavaScript");
      // Copy buttons need a script; without one they stay out of the way.
      await expect(page.locator("button[data-copy]")).toHaveCount(2);
      for (const button of await page.locator("button[data-copy]").all()) await expect(button).toBeHidden();
    });

    await test.step("miss page, unknown name", async () => {
      const response = await page.goto(`${DIRECTORY_URL}/reprots/10/13`);
      expect(response?.status()).toBe(404);
      const suggestion = page.getByRole("link", { name: "go/reports/10/13" });
      await expect(suggestion).toHaveAttribute("href", "/reports/10/13");
      await expect(suggestion).toBeFocused();
      await expect(page.getByRole("link", { name: "Create go/reprots/10/13" })).toHaveAttribute("href", "/-/new?pattern=reprots%2F10%2F13");
      await expect(page.getByRole("searchbox", { name: "Search go links" })).toHaveValue("reprots");
      await Promise.all([page.waitForURL(fixture.url("/daily?month=10&day=13")), page.keyboard.press("Enter")]);
    });

    await test.step("miss page, known name", async () => {
      const response = await page.goto(`${DIRECTORY_URL}/reports/13/01`);
      expect(response?.status()).toBe(404);
      await expect(page.locator(".miss-reason")).toContainText(/\bmonth\b.* doesn't accept "13"\. It accepts text matching/);
    });

    await test.step("the form says it needs JavaScript", async () => {
      await page.goto(`${DIRECTORY_URL}/-/new`);
      await expect(page.locator("#link-form")).toBeHidden();
      const formNotice = page.locator("main noscript .notice");
      await expect(formNotice).toBeVisible();
      await expect(formNotice).toContainText("this form needs JavaScript");
    });
  });
});

test("search and launch work by keyboard alone", async () => {
  await withContext({}, async (context) => {
    const page = await context.newPage();

    await test.step("a link with variables: search, Down, Enter, fill in, Enter", async () => {
      await page.goto(`${DIRECTORY_URL}/-/`);
      await expect(page.locator("#q")).toBeFocused();
      await page.keyboard.type("daily report");
      await expect(page.locator("#search-status")).toHaveText("1 link matches.");
      await page.keyboard.press("ArrowDown");
      await expect(page.locator("#results").getByRole("link", { name: "go/reports" })).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(page).toHaveURL(`${DIRECTORY_URL}/-/links/reports#launch`);

      const launcher = page.locator("#launcher");
      await expect(launcher.getByLabel("month")).toBeFocused();
      await page.keyboard.type("10");
      await page.keyboard.press("Tab");
      await expect(launcher.getByLabel("day")).toBeFocused();
      await page.keyboard.type("13");
      await Promise.all([page.waitForURL(fixture.url("/daily?month=10&day=13")), page.keyboard.press("Enter")]);
    });

    await test.step("a link without variables: search, Enter opens it", async () => {
      await page.goto(`${DIRECTORY_URL}/-/`);
      await page.keyboard.type("roadmap");
      await expect(page.locator("#search-status")).toHaveText("1 link matches.");
      await Promise.all([page.waitForURL(fixture.url("/docs/roadmap")), page.keyboard.press("Enter")]);
    });

    await test.step("Up, Down and Escape move between the box and the results", async () => {
      await page.goto(`${DIRECTORY_URL}/-/`);
      await page.keyboard.type("gh");
      await expect(page.locator("#results .result").first()).toBeVisible();
      await page.keyboard.press("ArrowDown");
      await expect(page.locator("#results .result-link").first()).toBeFocused();
      await page.keyboard.press("Escape");
      await expect(page.locator("#q")).toBeFocused();
      await expect(page.locator("#q")).toHaveValue("gh");
    });
  });
});
