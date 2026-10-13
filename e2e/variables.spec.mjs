// Links with variables, end to end (plan L2): the real server, the real
// extension, and a real browser. The links are the PRD's own examples (section
// 5.7), created through the API with the fixture as their destination.
//
// What only a browser can show is covered here: that what the user types
// after go/ reaches the Directory as typed, escapes included, and that the
// redirect the Directory builds arrives at the destination byte for byte.
// The rules themselves are tested in Go, at three levels, from spec.json.
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

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("./lib/harness.mjs").ExtensionBrowser | undefined} */
let browser;

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver

  buildServer();
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  fixture = await startFixture();

  const reports = await createLink({
    description: "Daily reports",
    routes: [
      {
        pattern: "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
        destination: fixture.url("/daily?month={month}&day={day}"),
      },
    ],
  });
  expect(reports.name).toBe("reports");
  const search = await createLink({
    description: "Search",
    routes: [{ pattern: "search/{q}", destination: fixture.url("/search?q={q}") }],
  });
  expect(search.name).toBe("search");

  browser = await launchWithExtension();
});

test.afterAll(async () => {
  // Each step runs whatever the others do: a browser that fails to close
  // must not leave an Ogre server behind on port 4655.
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
 * Opens a URL in a new tab of the browser that has the extension, hands the
 * tab and the final response to `check`, and closes the tab.
 *
 * @param {string} url
 * @param {(page: import("@playwright/test").Page, response: import("@playwright/test").Response | null) => Promise<void>} check
 */
async function open(url, check) {
  if (!browser) throw new Error("the browser did not start");
  const page = await browser.context.newPage();
  try {
    await check(page, await page.goto(url));
  } finally {
    await page.close();
  }
}

/**
 * Opens a go link that must end on the Directory's own 404 page, and returns
 * the page's text.
 *
 * @param {string} goPath what follows http://go, for example "/reports/13/01"
 * @returns {Promise<string>}
 */
async function openMiss(goPath) {
  const before = fixture.requests.length;
  let text = "";
  await open(`http://go${goPath}`, async (page, response) => {
    expect(page.url()).toBe(DIRECTORY_URL + goPath);
    expect(response?.status()).toBe(404);
    expect(response?.headers()["content-type"]).toContain("text/plain");
    expect(response?.headers()["cache-control"]).toBe("no-store");
    text = (await response?.text()) ?? "";
    // What the user sees is that text, shown as text.
    expect(await page.locator("body").innerText()).toContain(text.trim().split("\n")[0]);
  });
  // A miss sends nothing anywhere.
  expect(fixture.requests).toHaveLength(before);
  return text;
}

test("a go link with typed variables lands on its destination with the values in place", async () => {
  const landing = "/daily?month=10&day=13";

  await open("http://go/reports/10/13", async (page, response) => {
    expect(page.url()).toBe(fixture.url(landing));
    expect(response?.status()).toBe(200);
    await expect(page.locator("#fixture")).toHaveText("Fixture destination");

    const query = new URL(page.url()).searchParams;
    expect([...query.entries()]).toEqual([
      ["month", "10"],
      ["day", "13"],
    ]);

    // The hop before the destination is the Directory answering 302.
    const hop = response?.request().redirectedFrom();
    expect(hop?.url()).toBe(`${DIRECTORY_URL}/reports/10/13`);
    expect((await hop?.response())?.status()).toBe(302);
  });

  expect(fixture.requestsFor(landing).map((request) => request.method)).toEqual(["GET"]);
});

test("a value its variable refuses ends on a 404 that names the variable", async () => {
  // 13 is not a month. The page says which variable refused which value,
  // and what it accepts (MISS-2).
  const text = await openMiss("/reports/13/01");

  expect(text).toContain("go/reports exists");
  expect(text).toContain("go/reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}");
  expect(text).toMatch(/\bmonth\b.*"13"/);
  expect(text).toContain("^(0?[1-9]|1[0-2])$");
});

test("a misspelled name ends on a 404 that offers the corrected go link", async () => {
  // Two swapped letters, and the values are carried over (MISS-1).
  const text = await openMiss("/reprots/10/13");

  expect(text).toContain("go/reprots is not a go link yet");
  expect(text).toContain("Did you mean go/reports/10/13?");

  // The offer is good: opening it lands.
  await open("http://go/reports/10/13", async (page) => {
    expect(page.url()).toBe(fixture.url("/daily?month=10&day=13"));
  });
});

test("an encoded value arrives at the destination encoded, as one value", async () => {
  // "a b&c", typed as a%20b%26c. Decoded on the way it would become
  // ?q=a b&c, that is a second parameter "c" the link's owner never wrote
  // (NFR-9); PRD 5.7, row 14.
  const landing = "/search?q=a%20b%26c";
  const before = fixture.requests.length;

  await open("http://go/search/a%20b%26c", async (page, response) => {
    expect(page.url()).toBe(fixture.url(landing));
    expect(response?.request().redirectedFrom()?.url()).toBe(`${DIRECTORY_URL}/search/a%20b%26c`);
    expect([...new URL(page.url()).searchParams.entries()]).toEqual([["q", "a b&c"]]);
  });

  // The fixture records the request line as it came off the wire.
  const received = fixture.requests.slice(before).filter((request) => request.url !== "/favicon.ico");
  expect(received.map((request) => request.url)).toEqual([landing]);
});
