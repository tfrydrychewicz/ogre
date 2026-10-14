// The web UI in every engine of NFR-20 (plan L4.8): PRD scenarios 1 to 7,
// the accessibility scan in light and dark, a phone's width, and the
// pattern editor's highlighting, in Playwright's Chromium, Firefox and
// WebKit. Each page opened fails its test on a content security policy
// violation or a script error (lib/browsers.mjs).
//
// What runs where (playwright.config.mjs): the projects "firefox" and
// "webkit" run this file ("make e2e-browsers"); "chromium" runs it with
// everything else ("make e2e"), as the baseline the other two are read
// against. Microsoft Edge is Chromium, so the Chromium run stands in for
// it; no Edge binary is driven, and docs/manual-tests.md says so. WebKit
// stands in for Safari the same way, which is why Safari has a short
// manual pass as well.
//
// The extension is the one part left out: it is Chrome's, and neither
// Firefox nor WebKit can load it. A go link is opened here at the address
// the extension redirects it to, http://localhost:<port>/<path>, so every
// page and redirect after that point is the one a user of these browsers
// gets (with the setup page's search shortcut, for instance). The
// extension's redirect itself is covered by scenarios.spec.mjs in
// Chromium. That file is the authoritative telling of the scenarios; this
// one follows it step for step, with the redirect taken out, and must be
// kept in step with it.
//
// The Directory listens on a free port of its own (never 4655), and each
// test starts from an empty one.

import { expect, test as base } from "@playwright/test";

import { buildServer, createLink, startFixture } from "./lib/harness.mjs";
import { startDirectoryOnFreePort, watchPages } from "./lib/browsers.mjs";
import { expectAccessible, expectNoHorizontalScroll } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;

/**
 * @typedef {object} Fixtures
 * @property {import("./lib/browsers.mjs").PortDirectory} directory an empty Directory, stopped after the test, which must end gracefully
 * @property {Awaited<ReturnType<typeof watchPages>>} watch the policy watch on the test's context; it must have seen nothing by the end
 */

const test = base.extend(
  /** @type {import("@playwright/test").Fixtures<Fixtures, {}, import("@playwright/test").PlaywrightTestArgs & import("@playwright/test").PlaywrightTestOptions, import("@playwright/test").PlaywrightWorkerArgs & import("@playwright/test").PlaywrightWorkerOptions>} */ ({
    directory: async ({}, use, testInfo) => {
      const directory = await startDirectoryOnFreePort();
      await use(directory);
      const ended = await directory.stop();
      if (testInfo.status !== testInfo.expectedStatus) {
        await testInfo.attach("ogre output", { body: directory.output() || "(none)", contentType: "text/plain" });
      }
      expect(ended, directory.output()).toBe("exit code 0");
    },
    // Automatic, so it is in place before the test's first page opens,
    // whether or not the test names it.
    watch: [
      async ({ context, directory, browserName }, use, testInfo) => {
        const watch = await watchPages(context, { origin: directory.url, browserName });
        await use(watch);
        // Pages close first, so every message they wrote has arrived.
        for (const page of context.pages()) await page.close();
        if (watch.quirks() > 0) {
          testInfo.annotations.push({ type: "webkit-select-quirk", description: `${watch.quirks()} report(s) of WebKit's own <select> style let through` });
        }
        expect(watch.problemsNow(), "content security policy violations and console errors").toEqual([]);
      },
      { auto: true },
    ],
  }),
);

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  fixture = await startFixture();
});

test.afterAll(async () => {
  await fixture?.close();
});

/**
 * The link `reports` of PRD 5.7, as scenarios.spec.mjs has it (see there
 * why its month is a regex and not an int).
 */
function reportsDraft() {
  return {
    description: "Daily report for one day",
    routes: [
      {
        pattern: "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
        destination: fixture.url("/daily?month={month}&day={day}"),
        variables: {
          month: { description: "Month, 1 to 12", example: "10" },
          day: { description: "Day of the month", example: "13" },
        },
      },
    ],
  };
}

/**
 * Opens go<goPath> the way the extension would deliver it: at the
 * Directory's address.
 *
 * @param {import("@playwright/test").Page} page
 * @param {string} directory the Directory's origin
 * @param {string} goPath what follows "go", for example "/reports/10/13"
 */
function openGoLink(page, directory, goPath) {
  return page.goto(directory + goPath);
}

test.describe("PRD scenarios", () => {
  test("scenario 1: register a simple link, and go/roadmap works at once", async ({ page, directory }) => {
    const DIR = directory.url;
    const destination = fixture.url("/docs/roadmap");

    // go/ on its own is the Directory's home page (RES-5).
    await openGoLink(page, DIR, "/");
    await expect(page).toHaveURL(`${DIR}/-/`);
    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "New link" }).click();
    await expect(page).toHaveURL(`${DIR}/-/new`);

    await page.getByLabel("Pattern").pressSequentially("roadmap");
    await page.getByLabel("Destination").fill(destination);
    await expect(page.locator("#name-status")).toContainText("go/roadmap is free");

    await Promise.all([page.waitForURL(`${DIR}/-/links/roadmap`), page.getByRole("button", { name: "Create link" }).click()]);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("go/roadmap");

    // The request chain is read from the requests the page made, not from
    // redirectedFrom(): WebKit moves a navigation from localhost to another
    // site into a new web process, and Playwright's WebKit then reports the
    // destination's request without the redirect that led to it. Seen only
    // when the tab was already on a Directory page, as it is here.
    /** @type {string[]} */
    const asked = [];
    page.on("request", (request) => {
      if (request.isNavigationRequest()) asked.push(request.url());
    });
    await openGoLink(page, DIR, "/roadmap");
    expect(page.url()).toBe(destination);
    expect(asked).toEqual([`${DIR}/roadmap`, destination]);
    expect(fixture.requestsFor("/docs/roadmap")).not.toHaveLength(0);
  });

  test("scenario 2: register a link with typed variables and watch the preview", async ({ page, directory }) => {
    const DIR = directory.url;
    await page.goto(`${DIR}/-/new`);

    const pattern = page.getByLabel("Pattern");
    await pattern.pressSequentially("reports/{month}/{day}");
    const month = page.getByRole("group", { name: "Variable {month}" });
    const day = page.getByRole("group", { name: "Variable {day}" });
    await expect(month).toBeVisible();
    await expect(day).toBeVisible();

    // The type selectors are the <select> elements WebKit reports on its own
    // (lib/browsers.mjs); choosing from them must work all the same.
    await month.getByLabel("Type").selectOption("int");
    await expect(pattern).toHaveValue("reports/{month:int}/{day}");
    await expect(month.locator(".variable-accepts")).toHaveText("Accepts a whole number.");
    await day.getByLabel("Type").selectOption("int");
    await expect(pattern).toHaveValue("reports/{month:int}/{day:int}");
    await expect(day.locator(".variable-accepts")).toHaveText("Accepts a whole number.");
    await page.getByLabel("Destination").fill(fixture.url("/daily?month={month}&day={day}"));

    const preview = page.locator("#preview");
    const output = preview.locator(".launcher-output");
    const open = preview.getByRole("button", { name: "Open" });
    await expect(open).toHaveAttribute("aria-disabled", "true");

    await preview.getByLabel("month").pressSequentially("10");
    await expect(output).toContainText("go/reports/10/day");
    await expect(open).toHaveAttribute("aria-disabled", "true");

    await preview.getByLabel("day").pressSequentially("1");
    await expect(output).toContainText(fixture.url("/daily?month=10&day=1"));
    await preview.getByLabel("day").pressSequentially("3");
    await expect(output).toContainText("go/reports/10/13");
    await expect(output).toContainText(fixture.url("/daily?month=10&day=13"));
    await expect(open).toHaveAttribute("aria-disabled", "false");

    // An int has no range yet (VAR-9, L6).
    await preview.getByLabel("month").fill("13");
    await expect(output).toContainText(fixture.url("/daily?month=13&day=13"));
    await preview.getByLabel("month").fill("10");
    await expect(output).toContainText(fixture.url("/daily?month=10&day=13"));

    await preview.getByLabel("day").fill("x");
    await expect(preview).toContainText(`day doesn't accept "x"`);
    await expect(open).toHaveAttribute("aria-disabled", "true");
    await preview.getByLabel("day").fill("13");
    await expect(output).toContainText(fixture.url("/daily?month=10&day=13"));

    const before = await fetch(`${DIR}/-/api/v1/links`).then((response) => response.json());
    expect(before.links).toEqual([]);

    await Promise.all([page.waitForURL(`${DIR}/-/links/reports`), page.getByRole("button", { name: "Create link" }).click()]);
    const saved = await fetch(`${DIR}/-/api/v1/links`).then((response) => response.json());
    expect(saved.links.map((/** @type {{ routes: { pattern: string }[] }} */ link) => link.routes.map((route) => route.pattern))).toEqual([
      ["reports/{month:int}/{day:int}"],
    ]);

    await openGoLink(page, DIR, "/reports/10/13");
    expect(page.url()).toBe(fixture.url("/daily?month=10&day=13"));
  });

  test("scenario 3: go/reports/10/13 lands on the report for October 13", async ({ page, directory }) => {
    const DIR = directory.url;
    await createLink(reportsDraft(), DIR);
    const landing = "/daily?month=10&day=13";
    const before = fixture.requestsFor(landing).length;

    const response = await openGoLink(page, DIR, "/reports/10/13");

    expect(page.url()).toBe(fixture.url(landing));
    expect(response?.status()).toBe(200);
    const hop = response?.request().redirectedFrom();
    expect(hop?.url()).toBe(`${DIR}/reports/10/13`);
    expect((await hop?.response())?.status()).toBe(302);
    expect(fixture.requestsFor(landing)).toHaveLength(before + 1);
  });

  test("scenario 4: go/reprots/10/13 offers go/reports/10/13, and Enter follows it", async ({ page, directory, watch }) => {
    const DIR = directory.url;
    await createLink(reportsDraft(), DIR);
    watch.allowStatus(`${DIR}/reprots/10/13`);

    const response = await openGoLink(page, DIR, "/reprots/10/13");

    expect(page.url()).toBe(`${DIR}/reprots/10/13`);
    expect(response?.status()).toBe(404);
    const main = page.getByRole("main");
    await expect(main).toContainText("No link go/reprots/10/13");
    await expect(main).toContainText("Did you mean");
    const suggestion = main.getByRole("link", { name: "go/reports/10/13" });
    await expect(suggestion).toBeFocused();

    await Promise.all([page.waitForURL(fixture.url("/daily?month=10&day=13")), page.keyboard.press("Enter")]);
  });

  test("scenario 5: go/reports/13/01 says that month doesn't accept 13, and what it accepts", async ({ page, directory, watch }) => {
    const DIR = directory.url;
    await createLink(reportsDraft(), DIR);
    watch.allowStatus(`${DIR}/reports/13/01`);
    const before = fixture.requests.length;

    const response = await openGoLink(page, DIR, "/reports/13/01");

    expect(page.url()).toBe(`${DIR}/reports/13/01`);
    expect(response?.status()).toBe(404);
    const main = page.getByRole("main");
    await expect(main.getByRole("heading", { level: 1 })).toHaveText("go/reports can't open this address");
    await expect(main).toContainText("Daily report for one day");
    const reason = main.locator(".miss-reason");
    await expect(reason).toContainText(/\bmonth\b.* doesn't accept "13"\./);
    await expect(reason).toContainText("It accepts text matching ^(0?[1-9]|1[0-2])$.");
    await expect(main.getByRole("link", { name: "View go/reports" })).toHaveAttribute("href", "/-/links/reports");
    expect(fixture.requests).toHaveLength(before);
  });

  test("scenario 6: discover go/reports by searching, fill in two fields, open the report", async ({ page, directory }) => {
    const DIR = directory.url;
    await createLink(reportsDraft(), DIR);
    await createLink({ description: "Product roadmap", routes: [{ pattern: "roadmap", destination: fixture.url("/docs/roadmap") }] }, DIR);
    await createLink({ description: "Weekly status of the platform", routes: [{ pattern: "status", destination: fixture.url("/status") }] }, DIR);

    await page.goto(`${DIR}/-/`);
    const box = page.getByRole("searchbox", { name: "Search go links" });
    await expect(box).toBeFocused();
    await box.pressSequentially("daily report");
    await expect(page.locator("#search-status")).toHaveText("1 link matches.");
    const result = page.locator("#results").getByRole("link", { name: "go/reports" });
    await expect(result).toBeVisible();
    await expect(page.locator("#results .pattern-var")).toHaveText(["{month:regex(^(0?[1-9]|1[0-2])$)}", "{day:int}"]);

    await result.click();
    await expect(page).toHaveURL(`${DIR}/-/links/reports#launch`);

    const variables = page.getByRole("region", { name: /^Variables of go\/reports/ });
    await expect(variables.getByRole("row", { name: /^month\b/ })).toContainText("Month, 1 to 12");
    await expect(variables.getByRole("row", { name: /^day\b/ })).toContainText("Day of the month");

    const launcher = page.locator("#launcher");
    await expect(launcher.getByLabel("month")).toBeFocused();
    await launcher.getByLabel("month").fill("10");
    await launcher.getByLabel("day").fill("13");
    await expect(launcher.locator(".launcher-output")).toContainText(fixture.url("/daily?month=10&day=13"));

    await Promise.all([page.waitForURL(fixture.url("/daily?month=10&day=13")), launcher.getByRole("button", { name: "Open" }).click()]);
  });

  test("scenario 7: edit the destination once, and every go/reports/... lands on the new URL", async ({ page, directory }) => {
    const DIR = directory.url;
    await createLink(reportsDraft(), DIR);

    await page.goto(`${DIR}/-/links/reports`);
    await page.getByRole("link", { name: "Edit go/reports" }).click();
    await expect(page).toHaveURL(`${DIR}/-/links/reports/edit`);
    const destination = page.getByLabel("Destination");
    await expect(destination).toHaveValue(fixture.url("/daily?month={month}&day={day}"));
    await expect(page.getByRole("group", { name: "Variable {month}" }).getByLabel("Description")).toHaveValue("Month, 1 to 12");
    await destination.fill(fixture.url("/v2/daily?m={month}&d={day}"));
    await Promise.all([page.waitForURL(`${DIR}/-/links/reports`), page.getByRole("button", { name: "Save changes" }).click()]);
    await expect(page.locator(".route-destination")).toHaveText(fixture.url("/v2/daily?m={month}&d={day}"));

    const oldBefore = fixture.requests.filter((request) => request.url.startsWith("/daily")).length;
    for (const [goPath, landing] of [
      ["/reports/10/13", "/v2/daily?m=10&d=13"],
      ["/reports/1/2", "/v2/daily?m=1&d=2"],
      ["/Reports/12/31/", "/v2/daily?m=12&d=31"],
    ]) {
      await openGoLink(page, DIR, goPath);
      expect(page.url(), goPath).toBe(fixture.url(landing));
    }
    expect(fixture.requests.filter((request) => request.url.startsWith("/daily"))).toHaveLength(oldBefore);
    const check = await fetch(`${DIR}/-/api/v1/links`).then((response) => response.json());
    expect(check.links[0].routes[0].variables.map((/** @type {{ description: string }} */ v) => v.description)).toEqual([
      "Month, 1 to 12",
      "Day of the month",
    ]);
  });
});

/**
 * The pages of ui.spec.mjs, less its variants: one of each kind. `ready`
 * is what must be on screen before the page counts as drawn.
 *
 * @type {{ label: string, path: string, status: number, ready: string }[]}
 */
const PAGES = [
  { label: "home with a search", path: "/-/?q=report", status: 200, ready: "#results .result" },
  {
    label: "new link form with variables",
    path: `/-/new?pattern=${encodeURIComponent("jira/{key:regex([A-Z]+-[0-9]+)}/{n:int}")}&destination=${encodeURIComponent("https://acme.atlassian.net/browse/{key}?n={n}")}`,
    status: 200,
    ready: ".variable",
  },
  { label: "edit form", path: "/-/links/reports/edit", status: 200, ready: ".variable" },
  { label: "link page", path: "/-/links/reports", status: 200, ready: "#launcher input" },
  { label: "delete confirmation", path: "/-/links/reports/delete", status: 200, ready: "h1" },
  { label: "setup", path: "/-/setup", status: 200, ready: "h1" },
  { label: "miss page, unknown name", path: "/reprots/10/13", status: 404, ready: ".miss-suggestions" },
  { label: "miss page, known name", path: "/reports/13/01", status: 404, ready: ".miss-reason" },
];

/**
 * Opens one of PAGES and waits until its modules have drawn it.
 *
 * @param {import("@playwright/test").Page} page
 * @param {string} origin
 * @param {(typeof PAGES)[number]} entry
 */
async function openPage(page, origin, entry) {
  const response = await page.goto(origin + entry.path);
  expect(response?.status(), entry.label).toBe(entry.status);
  await expect(page.locator(entry.ready).first(), `${entry.label} is ready`).toBeVisible();
  if ((await page.locator("#link-form, #launcher").count()) > 0) {
    await expect(page.locator("#link-form[data-ready], #launcher[data-ready]").first(), `${entry.label} is drawn`).toBeAttached();
  }
}

test.describe("every page", () => {
  /** @param {string} origin */
  async function seed(origin) {
    await createLink(reportsDraft(), origin);
    await createLink({ description: "Product roadmap", routes: [{ pattern: "roadmap", destination: fixture.url("/docs/roadmap") }] }, origin);
  }

  for (const colorScheme of /** @type {const} */ (["light", "dark"])) {
    test.describe(`in ${colorScheme}`, () => {
      test.use({ colorScheme, viewport: { width: 1280, height: 900 } });

      test(`passes the accessibility scan (${colorScheme})`, async ({ page, directory, watch }) => {
        await seed(directory.url);
        for (const entry of PAGES) if (entry.status !== 200) watch.allowStatus(directory.url + entry.path);
        for (const entry of PAGES) {
          await test.step(entry.label, async () => {
            await openPage(page, directory.url, entry);
            await expectAccessible(page, entry.label);
          });
        }
      });
    });
  }

  test.describe("at a phone's width", () => {
    // Firefox has no mobile emulation in Playwright (isMobile); the width
    // alone is what this checks.
    test.use({ viewport: { width: 375, height: 800 } });

    test("no page scrolls sideways at 375 pixels", async ({ page, directory, watch }) => {
      await seed(directory.url);
      for (const entry of PAGES) if (entry.status !== 200) watch.allowStatus(directory.url + entry.path);
      for (const entry of PAGES) {
        await test.step(entry.label, async () => {
          await openPage(page, directory.url, entry);
          await expectNoHorizontalScroll(page, entry.label);
        });
      }
    });
  });
});

test("the pattern's highlighting sits exactly over the typed text", async ({ page, directory }) => {
  // Plan section 3.6: the pattern field is an input with transparent text
  // over a mirror that draws the tokens. It holds only if both lay the text
  // out the same way, which is engine behavior: same box, same font, same
  // padding and border, and the same advance for the same text.
  await page.goto(`${directory.url}/-/new`);
  const input = page.getByLabel("Pattern");
  await input.pressSequentially("reports/{month:int}/{day}");
  await expect(page.locator(".pattern-mirror .tok-var")).toHaveCount(2);

  const layout = await page.evaluate(() => {
    const field = /** @type {HTMLInputElement} */ (document.querySelector(".pattern-input"));
    const mirror = /** @type {HTMLElement} */ (document.querySelector(".pattern-mirror"));
    const keys = /** @type {const} */ ([
      "fontFamily",
      "fontSize",
      "fontWeight",
      "letterSpacing",
      "paddingLeft",
      "paddingTop",
      "borderLeftWidth",
      "borderTopWidth",
      "boxSizing",
    ]);
    /** @param {Element} node */
    const pick = (node) => {
      const style = getComputedStyle(node);
      return Object.fromEntries(keys.map((key) => [key, style[key]]));
    };
    const box = (/** @type {Element} */ node) => {
      const rect = node.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    };
    // The mirror's drawn text, without the trailing space it keeps for the
    // caret, against the input's value measured in the input's own font.
    const range = document.createRange();
    const spans = mirror.querySelectorAll("span");
    range.setStartBefore(/** @type {Node} */ (mirror.firstChild));
    range.setEndAfter(/** @type {Node} */ (spans[spans.length - 1] ?? mirror.firstChild));
    const canvas = document.createElement("canvas").getContext("2d");
    const style = getComputedStyle(field);
    if (canvas) canvas.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
    return {
      field: pick(field),
      mirror: pick(mirror),
      fieldBox: box(field),
      mirrorBox: box(mirror),
      mirrorText: range.toString(),
      mirrorWidth: range.getBoundingClientRect().width,
      valueWidth: canvas ? canvas.measureText(field.value).width : -1,
      value: field.value,
    };
  });
  expect(layout.mirror).toEqual(layout.field);
  expect(layout.mirrorBox).toEqual(layout.fieldBox);
  expect(layout.mirrorText).toBe(layout.value);
  expect(Math.abs(layout.mirrorWidth - layout.valueWidth), JSON.stringify(layout)).toBeLessThan(1);
});
