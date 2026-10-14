// The walking skeleton, end to end (plan L1.9): the real server, the real
// extension, and a real browser. A link is created through the API, and
// opening its go link must land on the destination.
//
// Needs port 4655 free, because that is where the extension redirects to (or
// the port OGRE_E2E_PORT names, where the harness points the extension).

import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import path from "node:path";

import {
  DIRECTORY_URL,
  buildServer,
  createLink,
  launchWithExtension,
  launchWithoutExtension,
  startFixture,
  startServer,
  tempDir,
} from "./lib/harness.mjs";

/** Path on the fixture that the link `roadmap` points to. */
const DESTINATION_PATH = "/docs/roadmap";

// Pages of "another site" (the fixture is a different origin from the
// Directory): one that links to a go link, and one that tries to load go
// links into itself to find out which exist.
const LINKING_PAGE = "/other-site/links";
const PROBING_PAGE = "/other-site/probe";
const PAGES = {
  [LINKING_PAGE]: `<!doctype html><title>Another site</title>
<p><a id="go" href="http://go/roadmap">the roadmap</a></p>
`,
  [PROBING_PAGE]: `<!doctype html><title>Another site, probing</title>
<img id="hit" src="${DIRECTORY_URL}/roadmap" alt="">
<img id="miss" src="${DIRECTORY_URL}/no-such-link" alt="">
<iframe id="frame-hit" src="${DIRECTORY_URL}/roadmap?frame"></iframe>
<iframe id="frame-miss" src="${DIRECTORY_URL}/no-such-link?frame"></iframe>
`,
};

/** @type {string} */
let dbPath;
/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("./lib/harness.mjs").ExtensionBrowser | undefined} */
let browser;
/** @type {string} */
let destination;

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver

  buildServer();
  dbPath = path.join(tempDir("ogre-e2e-data-"), "ogre.db");
  server = await startServer({ dbPath });
  fixture = await startFixture({ pages: PAGES });
  destination = fixture.url(DESTINATION_PATH);

  const link = await createLink({ description: "Product roadmap", routes: [{ pattern: "roadmap", destination }] });
  expect(link.name).toBe("roadmap");

  browser = await launchWithExtension();
});

test.afterAll(async () => {
  // Each step runs whatever the others do: a browser that fails to close
  // must not leave an Ogre server behind on port 4655.
  const results = await Promise.allSettled([browser?.close(), fixture?.close(), server?.stop()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
  // A server that had to be killed after ignoring SIGTERM is a failed
  // shutdown, not a clean one.
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
 * Asserts that the tab ended on the fixture destination, sent there by the
 * Directory's own redirect for `directoryPath`.
 *
 * @param {import("@playwright/test").Page} page
 * @param {import("@playwright/test").Response | null} response
 * @param {string} directoryPath the path the Directory must have been asked for, for example "/roadmap"
 */
async function expectLanded(page, response, directoryPath) {
  expect(page.url()).toBe(destination);
  expect(response?.status()).toBe(200);
  await expect(page.locator("#fixture")).toHaveText("Fixture destination");

  // The hop before the destination is the Directory answering 302. The
  // extension's own redirect from http://go/... happens inside Chromium and is
  // not part of this chain.
  const hop = response?.request().redirectedFrom();
  expect(hop?.url()).toBe(DIRECTORY_URL + directoryPath);
  const hopResponse = await hop?.response();
  expect(hopResponse?.status()).toBe(302);
  expect(hopResponse?.headers()["cache-control"]).toBe("no-store");
}

test("a go link lands on its destination", async () => {
  const before = fixture.requestsFor(DESTINATION_PATH).length;

  await open("http://go/roadmap", async (page, response) => {
    await expectLanded(page, response, "/roadmap");
  });

  const received = fixture.requestsFor(DESTINATION_PATH).slice(before);
  expect(received.map((request) => request.method)).toEqual(["GET"]);
});

test("a go link ignores the case of its name", async () => {
  const before = fixture.requestsFor(DESTINATION_PATH).length;

  await open("http://go/RoadMap", async (page, response) => {
    await expectLanded(page, response, "/RoadMap");
  });

  expect(fixture.requestsFor(DESTINATION_PATH)).toHaveLength(before + 1);
});

test("a hyphen and an underscore are part of the name", async () => {
  // PRD Q5: names ignore case and nothing else, so these are three links.
  const names = ["on-call", "on_call", "oncall"];
  for (const name of names) {
    const link = await createLink({ routes: [{ pattern: name, destination: fixture.url(`/docs/${name}`) }] });
    expect(link.name).toBe(name);
  }

  for (const name of names) {
    await open(`http://go/${name.toUpperCase()}`, async (page) => {
      expect(page.url()).toBe(fixture.url(`/docs/${name}`));
    });
  }

  // And a separator is not optional: `roadmap` exists, `road-map` does not.
  for (const goLink of ["http://go/road-map", "http://go/road_map"]) {
    await open(goLink, async (page, response) => {
      expect(page.url()).toBe(goLink.replace("http://go", DIRECTORY_URL));
      expect(response?.status()).toBe(404);
    });
  }
});

test("a go link clicked on another site lands on its destination", async () => {
  // EXT-1 names clicked links. The click starts on a page of another origin,
  // so the Directory sees a cross-site navigation, which it must serve: its
  // guards against other sites are for writes and for embedding, not for
  // following a link (plan 3.8; L0 spike, check 4).
  if (!browser) throw new Error("the browser did not start");
  const before = fixture.requestsFor(DESTINATION_PATH).length;
  const page = await browser.context.newPage();
  try {
    await page.goto(fixture.url(LINKING_PAGE));
    const toDirectory = page.waitForRequest(`${DIRECTORY_URL}/roadmap`);
    await page.click("#go");
    const headers = await (await toDirectory).allHeaders();
    await page.waitForURL(destination);

    await expect(page.locator("#fixture")).toHaveText("Fixture destination");
    expect(headers["sec-fetch-site"]).toBe("cross-site");
    expect(headers["sec-fetch-mode"]).toBe("navigate");
    expect(headers["sec-fetch-dest"]).toBe("document");
  } finally {
    await page.close();
  }
  expect(fixture.requestsFor(DESTINATION_PATH)).toHaveLength(before + 1);
});

test("a page of another site cannot tell which go links exist by embedding them", async () => {
  // A hit answers 302 and a miss 404; a page that loads go links as images or
  // frames could tell them apart without reading either. The Directory gives
  // such requests one fixed refusal, the same for both.
  if (!browser || !server) throw new Error("the browser or the server did not start");
  const running = server;
  /** @param {string} needle */
  const logged = (needle) => running.output().split(needle).length - 1;
  const before = {
    destination: fixture.requestsFor(DESTINATION_PATH).length,
    refused: logged("decision=refused_cross_site"),
    hits: logged("decision=hit"),
    misses: logged("decision=miss_"),
  };

  const page = await browser.context.newPage();
  try {
    // The browser shows the frames' responses to the test. It withholds the
    // images' (a refusal is not an image), so those are counted in the
    // server's log below.
    /** @type {Map<string, { status: number, location: string | undefined, length: string | undefined }>} */
    const frames = new Map();
    page.on("response", (response) => {
      const url = response.url();
      if (url.startsWith(DIRECTORY_URL)) {
        const headers = response.headers();
        frames.set(url.slice(DIRECTORY_URL.length), {
          status: response.status(),
          location: headers["location"],
          length: headers["content-length"],
        });
      }
    });
    await page.goto(fixture.url(PROBING_PAGE));

    // Four probes, two for a link that exists and two for one that does not:
    // four refusals, and not one of them looked up.
    await expect.poll(() => logged("decision=refused_cross_site")).toBe(before.refused + 4);
    expect(logged("decision=hit")).toBe(before.hits);
    expect(logged("decision=miss_")).toBe(before.misses);

    await expect.poll(() => [...frames.keys()].sort()).toEqual(["/no-such-link?frame", "/roadmap?frame"]);
    const refusal = frames.get("/roadmap?frame");
    expect(refusal?.status).toBe(403);
    expect(refusal?.location).toBeUndefined();
    expect(frames.get("/no-such-link?frame")).toEqual(refusal);
  } finally {
    await page.close();
  }
  // Nothing was followed to the destination.
  expect(fixture.requestsFor(DESTINATION_PATH)).toHaveLength(before.destination);
});

test("the Directory's home page has no serious accessibility issue", async () => {
  // NFR-21. Every page, in both color schemes, is scanned by ui.spec.mjs;
  // this one is reached the way a user reaches it, through the extension.
  await open("http://go/", async (page, response) => {
    expect(page.url()).toBe(`${DIRECTORY_URL}/-/`);
    expect(response?.status()).toBe(200);
    await expect(page.locator("h1")).toHaveText("Find a go link");
    const results = await new AxeBuilder({ page }).analyze();
    expect(results.passes.length).toBeGreaterThan(0); // axe really ran against the page
    const serious = results.violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical");
    expect(serious.map((violation) => `${violation.id}: ${violation.help}`)).toEqual([]);
  });
});

test("an unknown name ends on the Directory's 404", async () => {
  const before = fixture.requests.length;

  await open("http://go/nope", async (page, response) => {
    expect(page.url()).toBe(`${DIRECTORY_URL}/nope`);
    expect(response?.status()).toBe(404);
    expect(response?.headers()["cache-control"]).toBe("no-store");
  });

  expect(fixture.requests).toHaveLength(before);
});

test("without the extension, a go link goes nowhere", async () => {
  // The control for every other test: same browser, same server, same link,
  // no extension. If this one landed, something other than the extension
  // would be doing the redirect.
  const before = fixture.requests.length;
  const control = await launchWithoutExtension();
  try {
    const page = await control.context.newPage();
    const outcome = await page.goto("http://go/roadmap").then(
      () => `loaded ${page.url()}`,
      (error) => `failed: ${String(error.message).split("\n")[0]}`,
    );

    // Normally the host `go` does not resolve. On a network where it does, the
    // browser loads whatever answers there; either way it is not the fixture.
    expect(outcome).toMatch(/^failed: .*net::ERR_|^loaded https?:\/\/go\//);
    expect(page.url()).not.toContain(fixture.origin);
    expect(page.url()).not.toContain(DIRECTORY_URL);
  } finally {
    await control.close();
  }
  expect(fixture.requests).toHaveLength(before);
});

test("saved links survive a restart of the server", async () => {
  if (!server) throw new Error("the server did not start");

  await test.step("after a graceful shutdown", async () => {
    // SIGTERM must end in a clean exit, not in the process simply dying from
    // the signal: exit code 0, and the server saying it has stopped.
    expect(await server?.stop("SIGTERM")).toBe("exit code 0");
    expect(server?.output()).toContain("ogre has stopped");
    // Proof that the server is really gone, so that the landing below can only
    // come from the new process.
    const whileStopped = await open("http://go/roadmap", async () => {}).then(
      () => "loaded",
      (error) => String(error.message).split("\n")[0],
    );
    expect(whileStopped).toContain("net::ERR_CONNECTION_REFUSED");

    server = await startServer({ dbPath });
    await open("http://go/roadmap", async (page, response) => {
      await expectLanded(page, response, "/roadmap");
    });
  });

  await test.step("after the process is killed", async () => {
    // A link saved just before the kill, to show that an acknowledged write is
    // on disk (NFR-7) and not only the one that a clean shutdown flushed.
    const lateDestination = fixture.url("/docs/late");
    await createLink({ description: "Saved just before the kill", routes: [{ pattern: "late", destination: lateDestination }] });
    expect(await server?.stop("SIGKILL")).toBe("signal SIGKILL");

    server = await startServer({ dbPath });
    await open("http://go/roadmap", async (page, response) => {
      await expectLanded(page, response, "/roadmap");
    });
    await open("http://go/late", async (page) => {
      expect(page.url()).toBe(lateDestination);
    });
  });
});
