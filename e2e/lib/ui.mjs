// Helpers for the end-to-end tests of the web UI (plan L3.11): a watch on
// every page for content security policy violations and script errors, the
// accessibility scan, and a check for horizontal scrolling.
//
// The policy (plan section 3.6) forbids inline script and style. A page that
// broke it would mostly still look right, with a handler or a style silently
// dropped, so every test of the UI fails on any violation the browser
// reports, not only on what it can see.

import AxeBuilder from "@axe-core/playwright";
import { chromium, expect } from "@playwright/test";

import { DIRECTORY_URL } from "./harness.mjs";

/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */
/** @typedef {import("@playwright/test").Page} Page */

/** Prefix of the console message the init script writes for each violation. */
const VIOLATION = "CSP violation:";

/**
 * Collects, for every page of a context, what must never happen on the
 * Directory's pages: a content security policy violation (reported by the
 * securitypolicyviolation event, and by Chromium's own console message), an
 * uncaught script error, and any other console error.
 *
 * A console error the browser writes for a document that answered with an
 * error status ("Failed to load resource: ... 404") is expected on a miss
 * page; allowStatus(url) declares such a document, and only that exact
 * message for that exact URL is let through.
 *
 * Only the Directory's pages are watched. The fixture destination is not the
 * product: a browser asking it for /favicon.ico gets a 404, which is fine.
 *
 * @param {BrowserContext} context
 * @returns {Promise<{ problems: string[], allowStatus: (url: string) => void, assertClean: () => void }>}
 */
export async function watchPolicy(context) {
  /** @type {string[]} */
  const problems = [];
  /** @type {Set<string>} */
  const allowed = new Set();

  await context.addInitScript((prefix) => {
    document.addEventListener("securitypolicyviolation", (event) => {
      console.error(`${prefix} ${event.violatedDirective} blocked ${event.blockedURI || "(inline)"} at ${event.sourceFile}:${event.lineNumber}`);
    });
  }, VIOLATION);

  context.on("console", (message) => {
    if (message.type() !== "error" && message.type() !== "warning") return;
    const text = message.text();
    const where = message.location().url;
    const pageURL = message.page()?.url() ?? where;
    if (!pageURL.startsWith(`${DIRECTORY_URL}/`) && !where.startsWith(`${DIRECTORY_URL}/`)) return;
    if (message.type() === "warning" && !/Content Security Policy|Refused to/i.test(text)) return;
    if (allowed.has(where) && /^Failed to load resource: the server responded with a status of 4\d\d\b/.test(text)) return;
    problems.push(`${message.type()} on ${pageURL}: ${text}`);
  });
  context.on("weberror", (error) => {
    if (!(error.page()?.url() ?? "").startsWith(`${DIRECTORY_URL}/`)) return;
    problems.push(`uncaught on ${error.page()?.url() ?? "?"}: ${error.error().message}`);
  });

  return {
    problems,
    allowStatus: (url) => allowed.add(url),
    assertClean: () => expect(problems, "content security policy violations and console errors").toEqual([]),
  };
}

/**
 * Runs axe on the page with the WCAG 2.0 and 2.1 A and AA rules, and fails
 * on any serious or critical violation (NFR-21).
 *
 * @param {Page} page
 * @param {string} label names the page in the failure message
 */
export async function expectAccessible(page, label) {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(results.passes.length, `axe ran on ${label}`).toBeGreaterThan(0);
  const serious = results.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => `${violation.id} (${violation.impact}): ${violation.help} at ${violation.nodes.map((node) => node.target.join(" ")).join(", ")}`);
  expect(serious, `serious or critical accessibility issues on ${label}`).toEqual([]);
}

/**
 * Fails when the page scrolls sideways at its current width.
 *
 * @param {Page} page
 * @param {string} label
 */
export async function expectNoHorizontalScroll(page, label) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
  expect(scrollWidth, `${label} scrolls sideways (${scrollWidth}px of content in ${clientWidth}px)`).toBeLessThanOrEqual(clientWidth);
}

/**
 * Launches Playwright's bundled Chromium, headless and without the
 * extension, for tests that open the Directory at its own address and need
 * contexts of their own (a phone's width, a dark theme, no JavaScript).
 *
 * @returns {Promise<import("@playwright/test").Browser>}
 */
export function launchPlainBrowser() {
  return chromium.launch({ channel: "chromium", headless: true });
}
