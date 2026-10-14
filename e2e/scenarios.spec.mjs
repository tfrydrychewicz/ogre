// The PRD's scenarios 1 to 7 (PRD section 3), end to end (plan L3.11): the
// real server, the real extension, a real browser, and the web UI driven as
// the PRD tells each story. They are the acceptance tests of milestone L3.
//
// Each scenario starts from an empty Directory of its own, so none depends
// on another having run, and fails on any content security policy violation
// or script error in any page it opened (lib/ui.mjs).
//
// Needs port 4655 free, because that is where the extension redirects to.

import { expect, test } from "@playwright/test";
import path from "node:path";

import {
  DIRECTORY_URL,
  buildServer,
  createLink,
  launchWithExtension,
  startFixture,
  startServer,
  tempDir,
} from "./lib/harness.mjs";
import { watchPolicy } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("./lib/harness.mjs").ExtensionBrowser | undefined} */
let browser;
/** @type {Awaited<ReturnType<typeof watchPolicy>>} */
let watch;

/**
 * The link `reports` of PRD 5.7, with what its owner wrote about the
 * variables.
 *
 * Scenarios 2 and 5 cannot both hold with the same link while the P0 types
 * are the only ones: scenario 2 sets month to an integer, and scenario 5
 * needs month to refuse 13, which takes an integer range (VAR-9, P2,
 * milestone L6). Scenario 5 therefore uses the regex of PRD 5.7, as the
 * worked examples do; scenario 2 sets int as it tells, and pins that an int
 * takes 13 until ranges exist.
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

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  fixture = await startFixture();
  browser = await launchWithExtension();
  watch = await watchPolicy(browser.context);
});

test.beforeEach(async () => {
  watch.problems.length = 0;
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
});

test.afterEach(async ({}, testInfo) => {
  for (const page of browser?.context.pages() ?? []) await page.close();
  const ended = await server?.stop();
  if (testInfo.status !== testInfo.expectedStatus && server) {
    await testInfo.attach("ogre output", { body: server.output() || "(none)", contentType: "text/plain" });
    console.log(`--- ogre output ---\n${server.output() || "(none)"}\n--- end of ogre output ---`);
  }
  expect(ended, server?.output()).toBe("exit code 0");
  watch.assertClean();
});

test.afterAll(async () => {
  const results = await Promise.allSettled([browser?.close(), fixture?.close()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
});

/** @returns {Promise<import("@playwright/test").Page>} */
async function newTab() {
  if (!browser) throw new Error("the browser did not start");
  return browser.context.newPage();
}

/**
 * Types a go link into the tab as the address bar would send it, and waits
 * until the tab has arrived where it ends.
 *
 * @param {import("@playwright/test").Page} page
 * @param {string} goPath what follows "go", for example "/reports/10/13"
 */
function openGoLink(page, goPath) {
  return page.goto(`http://go${goPath}`);
}

test("scenario 1: register a simple link, and go/roadmap works at once", async () => {
  const destination = fixture.url("/docs/roadmap");
  const page = await newTab();

  // Ana opens the Directory: go/ on its own is its home page (RES-5).
  await openGoLink(page, "/");
  await expect(page).toHaveURL(`${DIRECTORY_URL}/-/`);
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "New link" }).click();
  await expect(page).toHaveURL(`${DIRECTORY_URL}/-/new`);

  // She enters roadmap and a destination URL. The form says the name is free.
  await page.getByLabel("Pattern").pressSequentially("roadmap");
  await page.getByLabel("Destination").fill(destination);
  await expect(page.locator("#name-status")).toContainText("go/roadmap is free");

  // She saves, and lands on the link's page.
  await Promise.all([
    page.waitForURL(`${DIRECTORY_URL}/-/links/roadmap`),
    page.getByRole("button", { name: "Create link" }).click(),
  ]);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("go/roadmap");

  // go/roadmap works immediately.
  const response = await openGoLink(page, "/roadmap");
  expect(page.url()).toBe(destination);
  expect(response?.request().redirectedFrom()?.url()).toBe(`${DIRECTORY_URL}/roadmap`);
  expect(fixture.requestsFor("/docs/roadmap")).not.toHaveLength(0);
});

test("scenario 2: register a link with typed variables and watch the preview", async () => {
  const page = await newTab();
  await page.goto(`${DIRECTORY_URL}/-/new`);

  // Ana enters reports/{month}/{day}; the form lists month and day.
  const pattern = page.getByLabel("Pattern");
  await pattern.pressSequentially("reports/{month}/{day}");
  const month = page.getByRole("group", { name: "Variable {month}" });
  const day = page.getByRole("group", { name: "Variable {day}" });
  await expect(month).toBeVisible();
  await expect(day).toBeVisible();

  // She sets both to integers in the variables table; the form writes the
  // types into the pattern, and the server reads them back.
  await month.getByLabel("Type").selectOption("int");
  await expect(pattern).toHaveValue("reports/{month:int}/{day}");
  await expect(month.locator(".variable-accepts")).toHaveText("Accepts a whole number.");
  await day.getByLabel("Type").selectOption("int");
  await expect(pattern).toHaveValue("reports/{month:int}/{day:int}");
  await expect(day.locator(".variable-accepts")).toHaveText("Accepts a whole number.");
  await page.getByLabel("Destination").fill(fixture.url("/daily?month={month}&day={day}"));

  // She watches the preview of the final URL update as she types values.
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

  // An int has no range yet (VAR-9, L6): 13 is a month to it. Scenario 5
  // uses the regex of PRD 5.7 to refuse it.
  await preview.getByLabel("month").fill("13");
  await expect(output).toContainText(fixture.url("/daily?month=13&day=13"));
  await preview.getByLabel("month").fill("10");
  await expect(output).toContainText(fixture.url("/daily?month=10&day=13"));

  // A value an int refuses: the preview says why, in the server's words.
  await preview.getByLabel("day").fill("x");
  await expect(preview).toContainText(`day doesn't accept "x"`);
  await expect(open).toHaveAttribute("aria-disabled", "true");
  await preview.getByLabel("day").fill("13");
  await expect(output).toContainText(fixture.url("/daily?month=10&day=13"));

  // Nothing was saved by the preview.
  const before = await fetch(`${DIRECTORY_URL}/-/api/v1/links`).then((response) => response.json());
  expect(before.links).toEqual([]);

  await Promise.all([
    page.waitForURL(`${DIRECTORY_URL}/-/links/reports`),
    page.getByRole("button", { name: "Create link" }).click(),
  ]);
  const saved = await fetch(`${DIRECTORY_URL}/-/api/v1/links`).then((response) => response.json());
  expect(saved.links.map((/** @type {{ routes: { pattern: string }[] }} */ link) => link.routes.map((route) => route.pattern))).toEqual([
    ["reports/{month:int}/{day:int}"],
  ]);

  await openGoLink(page, "/reports/10/13");
  expect(page.url()).toBe(fixture.url("/daily?month=10&day=13"));
});

test("scenario 3: go/reports/10/13 lands on the report for October 13", async () => {
  await createLink(reportsDraft());
  const page = await newTab();
  const landing = "/daily?month=10&day=13";
  const before = fixture.requestsFor(landing).length;

  const response = await openGoLink(page, "/reports/10/13");

  expect(page.url()).toBe(fixture.url(landing));
  expect(response?.status()).toBe(200);
  const hop = response?.request().redirectedFrom();
  expect(hop?.url()).toBe(`${DIRECTORY_URL}/reports/10/13`);
  expect((await hop?.response())?.status()).toBe(302);
  expect(fixture.requestsFor(landing)).toHaveLength(before + 1);
});

test("scenario 4: go/reprots/10/13 offers go/reports/10/13, and Enter follows it", async () => {
  await createLink(reportsDraft());
  const page = await newTab();
  watch.allowStatus(`${DIRECTORY_URL}/reprots/10/13`);

  const response = await openGoLink(page, "/reprots/10/13");

  expect(page.url()).toBe(`${DIRECTORY_URL}/reprots/10/13`);
  expect(response?.status()).toBe(404);
  const main = page.getByRole("main");
  await expect(main).toContainText("No link go/reprots/10/13");
  await expect(main).toContainText("Did you mean");
  const suggestion = main.getByRole("link", { name: "go/reports/10/13" });
  await expect(suggestion).toBeFocused();

  await Promise.all([page.waitForURL(fixture.url("/daily?month=10&day=13")), page.keyboard.press("Enter")]);
});

test("scenario 5: go/reports/13/01 says that month doesn't accept 13, and what it accepts", async () => {
  await createLink(reportsDraft());
  const page = await newTab();
  watch.allowStatus(`${DIRECTORY_URL}/reports/13/01`);
  const before = fixture.requests.length;

  const response = await openGoLink(page, "/reports/13/01");

  // Not a broken report page: the Directory's own page, at the address typed.
  expect(page.url()).toBe(`${DIRECTORY_URL}/reports/13/01`);
  expect(response?.status()).toBe(404);
  const main = page.getByRole("main");
  await expect(main.getByRole("heading", { level: 1 })).toHaveText("go/reports can't open this address");
  await expect(main).toContainText("Daily report for one day");
  // The link model's sentence: the variable (with the owner's note on it),
  // the value, and what the variable accepts.
  const reason = main.locator(".miss-reason");
  await expect(reason).toContainText(/\bmonth\b.* doesn't accept "13"\./);
  await expect(reason).toContainText("It accepts text matching ^(0?[1-9]|1[0-2])$.");
  await expect(main.getByRole("link", { name: "View go/reports" })).toHaveAttribute("href", "/-/links/reports");
  expect(fixture.requests).toHaveLength(before);
});

test("scenario 6: discover go/reports by searching, fill in two fields, open the report", async () => {
  await createLink(reportsDraft());
  await createLink({ description: "Product roadmap", routes: [{ pattern: "roadmap", destination: fixture.url("/docs/roadmap") }] });
  await createLink({ description: "Weekly status of the platform", routes: [{ pattern: "status", destination: fixture.url("/status") }] });
  const page = await newTab();

  // Chen doesn't know the name. She searches the Directory for "daily report".
  await page.goto(`${DIRECTORY_URL}/-/`);
  const box = page.getByRole("searchbox", { name: "Search go links" });
  await expect(box).toBeFocused();
  await box.pressSequentially("daily report");
  await expect(page.locator("#search-status")).toHaveText("1 link matches.");
  const result = page.locator("#results").getByRole("link", { name: "go/reports" });
  await expect(result).toBeVisible();
  // The result shows the pattern with its variables marked.
  await expect(page.locator("#results .pattern-var")).toHaveText(["{month:regex(^(0?[1-9]|1[0-2])$)}", "{day:int}"]);

  // She finds go/reports and opens its page.
  await result.click();
  await expect(page).toHaveURL(`${DIRECTORY_URL}/-/links/reports#launch`);

  // She reads what its variables mean.
  const variables = page.getByRole("region", { name: /^Variables of go\/reports/ });
  await expect(variables.getByRole("row", { name: /^month\b/ })).toContainText("Month, 1 to 12");
  await expect(variables.getByRole("row", { name: /^day\b/ })).toContainText("Day of the month");

  // She fills in two fields, and the launcher shows where the link leads.
  const launcher = page.locator("#launcher");
  await expect(launcher.getByLabel("month")).toBeFocused();
  await launcher.getByLabel("month").fill("10");
  await launcher.getByLabel("day").fill("13");
  await expect(launcher.locator(".launcher-output")).toContainText(fixture.url("/daily?month=10&day=13"));

  // She opens the report.
  await Promise.all([
    page.waitForURL(fixture.url("/daily?month=10&day=13")),
    launcher.getByRole("button", { name: "Open" }).click(),
  ]);
});

test("scenario 7: edit the destination once, and every go/reports/... lands on the new URL", async () => {
  await createLink(reportsDraft());
  const page = await newTab();

  // The reports tool moves. Ana edits the destination once.
  await page.goto(`${DIRECTORY_URL}/-/links/reports`);
  await page.getByRole("link", { name: "Edit go/reports" }).click();
  await expect(page).toHaveURL(`${DIRECTORY_URL}/-/links/reports/edit`);
  const destination = page.getByLabel("Destination");
  await expect(destination).toHaveValue(fixture.url("/daily?month={month}&day={day}"));
  // The owner's notes on the variables are kept through the edit.
  await expect(page.getByRole("group", { name: "Variable {month}" }).getByLabel("Description")).toHaveValue("Month, 1 to 12");
  await destination.fill(fixture.url("/v2/daily?m={month}&d={day}"));
  await Promise.all([
    page.waitForURL(`${DIRECTORY_URL}/-/links/reports`),
    page.getByRole("button", { name: "Save changes" }).click(),
  ]);
  await expect(page.locator(".route-destination")).toHaveText(fixture.url("/v2/daily?m={month}&d={day}"));

  // Every go/reports/... lands on the new URL.
  const oldBefore = fixture.requests.filter((request) => request.url.startsWith("/daily")).length;
  for (const [goPath, landing] of [
    ["/reports/10/13", "/v2/daily?m=10&d=13"],
    ["/reports/1/2", "/v2/daily?m=1&d=2"],
    ["/Reports/12/31/", "/v2/daily?m=12&d=31"],
  ]) {
    await openGoLink(page, goPath);
    expect(page.url(), goPath).toBe(fixture.url(landing));
  }
  expect(fixture.requests.filter((request) => request.url.startsWith("/daily"))).toHaveLength(oldBefore);
  const check = await fetch(`${DIRECTORY_URL}/-/api/v1/links`).then((response) => response.json());
  expect(check.links[0].routes[0].variables.map((/** @type {{ description: string }} */ v) => v.description)).toEqual([
    "Month, 1 to 12",
    "Day of the month",
  ]);
});
