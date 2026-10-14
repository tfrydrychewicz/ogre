// What survives a go link's two hops (plan L4.5): the extension's redirect
// from go/… to the Directory, and the Directory's redirect to the
// destination. The real extension, the real server, and a fixture
// destination that records each request as it arrived.
//
// The extension's rule must hand the Directory the path and query exactly as
// the browser sent them, percent-encoding included (EXT-1; L0 spike, checks 8
// and 9). The Directory decodes each segment once, and encodes each value
// again into the destination (PRD 5.4, 5.5), so an encoded slash stays one
// value and never becomes a path separator. The fragment never reaches a
// server; the browser carries it to the destination.

import { expect, test } from "@playwright/test";
import path from "node:path";

import { DIRECTORY_URL, buildServer, createLink, launchWithExtension, startFixture, startServer, tempDir } from "./lib/harness.mjs";
import { watchPolicy } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("./lib/harness.mjs").ExtensionBrowser | undefined} */
let browser;
/** @type {Awaited<ReturnType<typeof watchPolicy>>} */
let watch;

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  fixture = await startFixture();
  await createLink({ routes: [{ pattern: "files/{path}", destination: fixture.url("/files/{path}") }] });
  await createLink({ routes: [{ pattern: "search/{q}", destination: fixture.url("/search?q={q}") }] });
  await createLink({ routes: [{ pattern: "wiki/{title}", destination: fixture.url("/wiki/{title}#top") }] });
  browser = await launchWithExtension();
  watch = await watchPolicy(browser.context);
});

test.afterAll(async () => {
  const results = await Promise.allSettled([browser?.close(), fixture?.close(), server?.stop()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
  const ended = results[2].status === "fulfilled" ? results[2].value : undefined;
  if (server) expect(ended, server.output()).toBe("exit code 0");
});

/**
 * @typedef {object} Case
 * @property {string} open the go link as the test opens it
 * @property {string} directory the path and query the Directory must have been asked for
 * @property {string} destination the path and query the destination must have received
 * @property {string} landed the final address, relative to the fixture: destination plus any fragment
 */

/** @type {[string, Case][]} */
const CASES = [
  [
    "an encoded slash stays inside one value",
    { open: "http://go/files/a%2Fb", directory: "/files/a%2Fb", destination: "/files/a%2Fb", landed: "/files/a%2Fb" },
  ],
  [
    "an encoded space and an encoded slash",
    { open: "http://go/files/a%20b%2Fc", directory: "/files/a%20b%2Fc", destination: "/files/a%20b%2Fc", landed: "/files/a%20b%2Fc" },
  ],
  [
    "a space typed as a space",
    { open: "http://go/search/hello world", directory: "/search/hello%20world", destination: "/search?q=hello%20world", landed: "/search?q=hello%20world" },
  ],
  [
    "characters outside ASCII",
    { open: "http://go/wiki/Zürich", directory: "/wiki/Z%C3%BCrich", destination: "/wiki/Z%C3%BCrich", landed: "/wiki/Z%C3%BCrich#top" },
  ],
  [
    "a value that looks like a query, encoded",
    { open: "http://go/search/a%3Fb%3D1%26c", directory: "/search/a%3Fb%3D1%26c", destination: "/search?q=a%3Fb%3D1%26c", landed: "/search?q=a%3Fb%3D1%26c" },
  ],
  [
    // The query reaches the Directory. The Directory does not pass it on
    // yet: that is RES-8, query passthrough (L6.2), and fixture row 18 of
    // the PRD examples, which waits for it. The fragment survives both hops.
    "a query and a fragment",
    { open: "http://go/files/x?y=1&z=%C3%BC#frag", directory: "/files/x?y=1&z=%C3%BC", destination: "/files/x", landed: "/files/x#frag" },
  ],
];

for (const [label, c] of CASES) {
  test(`${label}: ${c.open}`, async () => {
    if (!browser) throw new Error("the browser did not start");
    watch.problems.length = 0;
    const before = fixture.requestsFor(c.destination).length;
    const page = await browser.context.newPage();
    try {
      const response = await page.goto(c.open);
      expect(page.url()).toBe(fixture.url(c.landed));
      expect(response?.status()).toBe(200);
      // The hop before the destination: the Directory, asked for the path
      // and query the extension's rule handed it, answering 302.
      const hop = response?.request().redirectedFrom();
      expect(hop?.url()).toBe(DIRECTORY_URL + c.directory);
      expect((await hop?.response())?.status()).toBe(302);
      // And the destination received exactly what the Directory built.
      expect(fixture.requestsFor(c.destination)).toHaveLength(before + 1);
    } finally {
      await page.close();
    }
    watch.assertClean();
  });
}
