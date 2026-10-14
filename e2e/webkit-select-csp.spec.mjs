// The minimal repro of WebKit's <select> report (plan section 9, open point
// from L3), and the proof that the exception lib/browsers.mjs makes for it
// is no wider than the quirk.
//
// The finding. Under the Directory's content security policy, WebKit
// raises a style-src-attr violation ("Refused to apply a stylesheet because
// its hash, its nonce, or 'unsafe-inline' does not appear in the style-src
// directive") for every <select> it creates. The pages here have no
// stylesheet and none of the Directory's code; the one that raises it holds
// nothing but a <select> written in static HTML, with no script at all. An
// <input>, a <textarea> or an <option> raises nothing. The report names the
// select as its target (the host of its user-agent shadow tree) although
// the select has no style attribute, and the select draws pixel for pixel
// the same with and without the policy. So the inline style WebKit refuses
// is its own, put on an element of the select's shadow tree; the page's
// code has no part in it and cannot avoid it short of not using <select>,
// and loosening style-src would be the wrong answer to it. Chromium and
// Firefox raise nothing. Probed in Playwright's WebKit 27.2 (macOS); the
// same report appears with or without the Trusted Types directives, so they
// are not the cause.
//
// These tests run in all three projects. In WebKit they fail the day the
// engine stops raising the report: then the exception in lib/browsers.mjs
// should go. Elsewhere they fail if another engine starts doing it.

import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import http from "node:http";
import path from "node:path";

import { REPO_ROOT } from "./lib/harness.mjs";
import { watchPages } from "./lib/browsers.mjs";

/**
 * The Directory's policy, read from its one definition in the Go source so
 * the repro is always under the same policy as the product.
 *
 * @returns {string}
 */
function directoryPolicy() {
  const source = readFileSync(path.join(REPO_ROOT, "internal", "web", "headers.go"), "utf8");
  const declaration = source.match(/const ContentSecurityPolicy = ((?:\s*"[^"]*"\s*\+?)+)/);
  if (!declaration) throw new Error("ContentSecurityPolicy not found in internal/web/headers.go");
  const policy = [...declaration[1].matchAll(/"([^"]*)"/g)].map((part) => part[1]).join("");
  // Guards against the pattern reading something else.
  expect(policy).toContain("style-src 'self'");
  return policy;
}

/**
 * Pages of the repro, by path. Scripts are files, as the policy wants.
 *
 * @type {Record<string, { type: string, body: string }>}
 */
const FILES = {
  "/select.html": { type: "text/html", body: "<!doctype html><title>select</title><select><option>a</option></select>" },
  "/controls.html": { type: "text/html", body: "<!doctype html><title>controls</title><input><textarea></textarea><button>b</button>" },
  "/created.html": { type: "text/html", body: '<!doctype html><title>created</title><body><script src="/created.js"></script>' },
  "/created.js": { type: "text/javascript", body: "document.body.append(document.createElement('select'));" },
  "/styled-select.html": { type: "text/html", body: '<!doctype html><title>styled select</title><body><script src="/styled-select.js"></script>' },
  "/styled-select.js": {
    type: "text/javascript",
    body: "const s = document.createElement('select'); s.setAttribute('style', 'color: red'); document.body.append(s);",
  },
  "/styled-div.html": { type: "text/html", body: '<!doctype html><title>styled div</title><body><script src="/styled-div.js"></script>' },
  "/styled-div.js": {
    type: "text/javascript",
    body: "const d = document.createElement('div'); d.setAttribute('style', 'color: red'); document.body.append(d);",
  },
  // A select whose inline event handler the policy refuses: another
  // directive than the quirk's, on the quirk's element.
  "/handler-select.html": {
    type: "text/html",
    body: '<!doctype html><title>handler select</title><select onfocus="void 0"><option>a</option></select><script src="/handler-select.js"></script>',
  },
  "/handler-select.js": { type: "text/javascript", body: "document.querySelector('select').focus();" },
  // A div whose refused style attribute is gone by the time the report
  // arrives (reports are queued): the quirk's directive, on an element that,
  // like the quirk's select, carries no style attribute.
  "/cleared-div.html": { type: "text/html", body: '<!doctype html><title>cleared div</title><body><script src="/cleared-div.js"></script>' },
  "/cleared-div.js": {
    type: "text/javascript",
    body: "const d = document.createElement('div'); document.body.append(d); d.setAttribute('style', 'color: red'); d.removeAttribute('style');",
  },
};

/** @type {http.Server} */
let server;
/** @type {string} */
let origin;

test.beforeAll(async () => {
  const policy = directoryPolicy();
  server = http.createServer((request, response) => {
    const url = new URL(request.url ?? "/", "http://repro.invalid");
    const file = FILES[url.pathname];
    if (!file) {
      response.writeHead(404, { "Content-Security-Policy": policy }).end();
      return;
    }
    /** @type {Record<string, string>} */
    const headers = { "Content-Type": `${file.type}; charset=utf-8`, "Cache-Control": "no-store" };
    if (url.searchParams.get("policy") !== "none") headers["Content-Security-Policy"] = policy;
    response.writeHead(200, headers).end(file.body);
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(undefined));
  });
  origin = `http://127.0.0.1:${/** @type {import("node:net").AddressInfo} */ (server.address()).port}`;
});

test.afterAll(async () => {
  await new Promise((resolve) => {
    server?.close(() => resolve(undefined));
    server?.closeAllConnections();
  });
});

/**
 * Opens a page of the repro and returns every violation it reported, as
 * "directive target-element".
 *
 * @param {import("@playwright/test").Page} page
 * @param {string} pathAndQuery
 * @returns {Promise<string[]>}
 */
async function violationsOf(page, pathAndQuery) {
  await page.addInitScript(() => {
    /** @type {string[]} */
    const seen = [];
    Object.defineProperty(window, "__violations", { value: seen });
    document.addEventListener(
      "securitypolicyviolation",
      (event) => seen.push(`${event.violatedDirective} ${event.target instanceof Element ? event.target.localName : "document"}`),
      true,
    );
  });
  await page.goto(origin + pathAndQuery);
  // A violation event is queued as a task; let the page settle first.
  await page.waitForLoadState("load");
  await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 100)));
  return page.evaluate(() => /** @type {string[]} */ (/** @type {any} */ (window).__violations));
}

test("a <select> in static HTML, with no script and no stylesheet, is reported by WebKit only", async ({ page, browserName }) => {
  const reported = await violationsOf(page, "/select.html");
  expect(reported).toEqual(browserName === "webkit" ? ["style-src-attr select"] : []);
});

test("so is a <select> created by script; other form controls are not", async ({ page, browserName }) => {
  expect(await violationsOf(page, "/created.html")).toEqual(browserName === "webkit" ? ["style-src-attr select"] : []);
  expect(await violationsOf(page, "/controls.html")).toEqual([]);
});

test("the refused style changes nothing that is drawn", async ({ browser }) => {
  /** @param {string} query */
  const shot = async (query) => {
    const page = await browser.newPage();
    try {
      await page.goto(`${origin}/select.html${query}`);
      return await page.locator("select").screenshot();
    } finally {
      await page.close();
    }
  };
  const withPolicy = await shot("");
  const withoutPolicy = await shot("?policy=none");
  expect(withPolicy.equals(withoutPolicy), "the select draws the same with and without the policy").toBe(true);
});

test("the WebKit exception lets the quirk through and nothing else", async ({ browser, browserName }) => {
  const context = await browser.newContext();
  try {
    const watch = await watchPages(context, { origin, browserName });
    for (const file of ["/select.html", "/created.html", "/styled-select.html", "/styled-div.html"]) {
      const page = await context.newPage();
      await page.goto(origin + file);
      await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 100)));
      await page.close();
    }
    const problems = watch.problemsNow();
    const count = (/** @type {RegExp} */ pattern) => problems.filter((problem) => pattern.test(problem)).length;
    // A style attribute set by the page is reported in every engine, on a
    // select as on any other element.
    expect(count(/CSP violation: style-src-attr blocked inline on <div>/), problems.join("\n")).toBe(1);
    if (browserName === "webkit") {
      // The two bare selects are the quirk; nothing else was taken for it.
      expect(watch.quirks(), problems.join("\n")).toBe(2);
      // A select with a style attribute of the page's own raises two
      // reports in WebKit, the page's and the engine's, which cannot be
      // told apart: both are reported, so nothing of the page's is hidden.
      expect(count(/CSP violation: style-src-attr blocked inline on <select>/), problems.join("\n")).toBe(2);
      // WebKit writes a console line for each of its five refusals: two are
      // explained by the quirk reports of their pages, three are reported.
      expect(count(/Refused to apply a stylesheet/), problems.join("\n")).toBe(3);
      expect(problems, problems.join("\n")).toHaveLength(6);
    } else {
      expect(watch.quirks()).toBe(0);
      expect(count(/CSP violation: style-src-attr blocked inline on <select>/), problems.join("\n")).toBe(1);
    }
  } finally {
    await context.close();
  }
});

test("the exception holds only for style-src-attr, and only on a select", async ({ browser, browserName }) => {
  /** @param {string} file */
  const watched = async (file) => {
    const context = await browser.newContext();
    try {
      const watch = await watchPages(context, { origin, browserName });
      const page = await context.newPage();
      await page.goto(origin + file);
      await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 100)));
      await page.close();
      return { problems: watch.problemsNow(), quirks: watch.quirks() };
    } finally {
      await context.close();
    }
  };

  // Another directive on a select is reported in every engine. (WebKit
  // also raises its quirk for the select itself, which is let through.)
  const handler = await watched("/handler-select.html");
  expect(handler.problems.filter((p) => /CSP violation: script-src-attr blocked inline on <select>/.test(p)), handler.problems.join("\n")).toHaveLength(1);
  expect(handler.quirks).toBe(browserName === "webkit" ? 1 : 0);

  // The quirk's directive on another element is reported in every engine,
  // even with no style attribute left on it.
  const cleared = await watched("/cleared-div.html");
  expect(cleared.problems.filter((p) => /CSP violation: style-src-attr blocked inline on <div>/.test(p)), cleared.problems.join("\n")).toHaveLength(1);
  expect(cleared.quirks).toBe(0);
});
