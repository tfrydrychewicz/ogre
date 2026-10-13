// The automated part of the Chrome spike checklist
// (docs/IMPLEMENTATION_PLAN.md, Appendix B). One test per check; each writes
// its result to spike/results/automated.json through recordResult().
//
// Run:  cd spike/harness && npm install && npm run checks
// Needs ports 4655 and 4656 free, and https://example.com for checks 4 and 11.
//
// Rules these tests follow:
//  - A check that says "the extension redirects" also runs the same navigation
//    in a browser WITHOUT the extension (the control) and requires it to miss
//    the stub, so a pass cannot come from the test setup.
//  - A test fails only when its check's status is "fail" or it could not run.
//    "inconclusive" is recorded and annotated, not hidden.
//  - page.goto() stands in for typing in the address bar, which Playwright
//    cannot do. Checks that depend on that difference are recorded as "proxy".
//  - Every browser here is Playwright's headless Chromium with field trials
//    off (see launchNotes() in lib/env.mjs). A check whose answer could depend
//    on that is recorded as "proxy" too.

import { expect, test } from "@playwright/test";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  DECOY_URL,
  DIRECTORY_URL,
  EXTENSION_DIR,
  buildStub,
  chromiumVersion,
  clearStubLog,
  extensionIdForDir,
  freshProfileDir,
  launchNotes,
  launchWithExtension,
  launchWithoutExtension,
  recordResult,
  startChromiumWithUrl,
  startStub,
  stubLog,
} from "../lib/env.mjs";
import { THIRD_PARTY_URL, startThirdParty } from "../lib/thirdparty.mjs";

const DEST_URL = `${DIRECTORY_URL}/-/spike/dest`;
const PUBLIC_PAGE = "https://example.com/";
const MAP_GO_TO_DECOY = "--host-resolver-rules=MAP go 127.0.0.1:4656";
// A public-looking name that the resolver rule sends to the decoy stub, which
// speaks plain HTTP only: a stand-in for "a public site without HTTPS" that
// needs no network.
const HTTP_ONLY_SITE = "http://godir-spike-probe.com/probe";
const MAP_HTTP_ONLY_SITE = "--host-resolver-rules=MAP godir-spike-probe.com 127.0.0.1:4656";

const THIRD_PARTY_PAGE = `${THIRD_PARTY_URL}/page`;

/** The directory stub. Check 12 stops and restarts it, so it is not a const. */
let directory;
/** A plain-HTTP "other website" the extension has no access to (lib/thirdparty.mjs). */
let thirdParty;
/** The switch that makes thirdparty.example reach that server. */
let mapThirdParty;

test.beforeAll(async () => {
  buildStub();
  directory = await startStub();
  thirdParty = await startThirdParty();
  mapThirdParty = `--host-resolver-rules=${thirdParty.resolverRule}`;
});

test.afterAll(async () => {
  await directory?.stop();
  await thirdParty?.stop();
});

// ---------------------------------------------------------------------------
// Helpers

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const firstLine = (error) => String(error?.message ?? error).split("\n")[0];

/** Calls read() until ok(value) or the time is up; returns the last value either way. */
async function poll(read, ok, timeout = 5000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await read();
    if (ok(value) || Date.now() > deadline) return value;
    await sleep(100);
  }
}

/**
 * Defines the test for one check. body() returns {status, observed, evidence}.
 * With assert: false the result is recorded but never fails the test.
 * A body that throws or overruns its timeout is recorded as inconclusive, so
 * an older result for the same check can never be mistaken for this run's.
 */
function check(id, title, mode, body, { assert = true, timeout = 60_000 } = {}) {
  test(`check ${id}: ${title}`, async () => {
    test.setTimeout(timeout + 15_000);
    let timer;
    const overrun = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`not finished after ${timeout / 1000} s`)), timeout);
    });
    let result;
    try {
      result = await Promise.race([body(), overrun]);
    } catch (error) {
      clearTimeout(timer);
      recordResult({
        id,
        title,
        mode,
        status: "inconclusive",
        observed: `The check did not run to the end: ${firstLine(error)}`,
        evidence: { error: String(error?.stack ?? error) },
      });
      throw error;
    }
    clearTimeout(timer);
    recordResult({ id, title, mode, ...result });
    console.log(`check ${id}: ${result.status.toUpperCase()} - ${result.observed}`);
    if (result.status === "inconclusive") {
      test.info().annotations.push({ type: "inconclusive", description: result.observed });
    }
    if (assert) expect(result.status, result.observed).not.toBe("fail");
  });
}

/** A profile directory whose Preferences file already holds these settings. */
function profileWithPrefs(prefs) {
  const dir = freshProfileDir();
  mkdirSync(path.join(dir, "Default"), { recursive: true });
  writeFileSync(path.join(dir, "Default", "Preferences"), JSON.stringify(prefs));
  return dir;
}

/** Runs an extension API call in an extension page, so it works with the service worker stopped. */
async function inExtensionPage(context, extensionId, pageName, fn) {
  const page = await context.newPage();
  try {
    await page.goto(`chrome-extension://${extensionId}/${pageName}`);
    return await page.evaluate(fn);
  } finally {
    await page.close();
  }
}

/** Dynamic rules and what the spike extension has stored. */
function extensionState(context, extensionId) {
  return inExtensionPage(context, extensionId, "status.html", async () => ({
    rules: await chrome.declarativeNetRequest.getDynamicRules(),
    ...(await chrome.storage.local.get({ goSeen: [], directoryErrors: [], lifecycle: [] })),
  }));
}

/**
 * Launches Chromium with the extension (waiting until rule 1 is installed) or
 * without it, runs fn, and always closes the browser.
 */
async function withBrowser(
  { extension = true, extensionDir = EXTENSION_DIR, userDataDir, extraArgs = [], ignoreDefaultArgs, waitForRule = true },
  fn,
) {
  const launched = extension
    ? await launchWithExtension({ extensionDir, userDataDir, extraArgs, ignoreDefaultArgs })
    : await launchWithoutExtension({ userDataDir, extraArgs, ignoreDefaultArgs });
  try {
    if (extension && waitForRule) {
      const rules = await poll(
        () => launched.serviceWorker.evaluate(() => chrome.declarativeNetRequest.getDynamicRules()),
        (found) => found.some((rule) => rule.id === 1),
      );
      if (!rules.some((rule) => rule.id === 1)) throw new Error("the extension did not install redirect rule 1");
    }
    return await fn(launched);
  } finally {
    await launched.context.close();
  }
}

/**
 * page.goto that never throws: reports the error, the final URL, the HTTP
 * status of the final response (null when there was none) and the main-frame
 * request chain.
 *
 * goto reports a failure before Chromium has put its error page (or the
 * extension its unreachable page) into the tab. This waits for that page, for
 * two reasons: the caller reads the tab's address next, and a page.close() or
 * another goto sent in the middle of that swap is sometimes never answered.
 */
async function navigate(page, url) {
  const requests = [];
  let replacementPageArrived = false;
  const onRequest = (request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) requests.push(request.url());
  };
  const onNavigated = (frame) => {
    if (frame === page.mainFrame() && /^chrome-(error|extension):/.test(frame.url())) replacementPageArrived = true;
  };
  page.on("request", onRequest);
  page.on("framenavigated", onNavigated);
  let error = null;
  let httpStatus = null;
  try {
    const response = await page.goto(url, { timeout: 20_000 });
    httpStatus = response?.status() ?? null;
  } catch (caught) {
    error = firstLine(caught);
    await poll(() => replacementPageArrived, Boolean, 1000);
  }
  page.off("request", onRequest);
  page.off("framenavigated", onNavigated);
  return { asked: url, error, httpStatus, finalUrl: page.url(), requests };
}

/**
 * Opens a new tab, navigates it, and reports what the stubs saw.
 * Both logs are emptied first, so everything reported belongs to this navigation.
 */
async function visit(context, url, { decoy = false } = {}) {
  await clearStubLog();
  if (decoy) await clearStubLog(DECOY_URL);
  const page = await context.newPage();
  try {
    const result = await navigate(page, url);
    result.directoryLog = await stubLog();
    if (decoy) result.decoyLog = await stubLog(DECOY_URL);
    result.reachedDirectory = Boolean(documentRequest(result.directoryLog));
    return result;
  } finally {
    await page.close();
  }
}

/** The same visit with the extension and, as the control, without it. */
async function visitWithAndWithout(url, { extraArgs = [], decoy = false } = {}) {
  const withExtension = await withBrowser({ extraArgs }, ({ context }) => visit(context, url, { decoy }));
  const control = await withBrowser({ extension: false, extraArgs }, ({ context }) => visit(context, url, { decoy }));
  return { withExtension, control };
}

const uris = (log) => log.map((entry) => entry.requestURI);
/**
 * The navigation among the logged requests. A page without an icon (Go's own
 * "400 Bad Request", for one) makes Chromium ask for /favicon.ico a moment
 * later, and that request can land in the log of the next visit.
 */
const documentRequest = (log) => log.find((entry) => entry.secFetchDest === "document");
const fetchHeaders = (entry) =>
  entry && {
    secFetchSite: entry.secFetchSite,
    secFetchMode: entry.secFetchMode,
    secFetchDest: entry.secFetchDest,
    secFetchUser: entry.secFetchUser,
    referer: entry.referer,
    origin: entry.origin,
  };

/**
 * Opens a public HTTPS page, adds a link to it and clicks the link with a real
 * mouse click. Returns {unreachable} if the public page cannot be loaded.
 */
async function clickLinkOnPublicPage(context, href) {
  await clearStubLog();
  const page = await context.newPage();
  const consoleMessages = [];
  const failedRequests = [];
  page.on("console", (message) => consoleMessages.push(`${message.type()}: ${message.text()}`));
  page.on("pageerror", (error) => consoleMessages.push(`pageerror: ${firstLine(error)}`));
  page.on("requestfailed", (request) => failedRequests.push(`${request.url()} ${request.failure()?.errorText}`));
  try {
    const opened = await navigate(page, PUBLIC_PAGE);
    if (opened.error || !page.url().startsWith(PUBLIC_PAGE)) return { unreachable: opened };
    await page.evaluate((target) => {
      const link = document.createElement("a");
      link.id = "spike-link";
      link.href = target;
      link.textContent = "spike go link";
      // On top of everything: the public page may cover its own top-left corner.
      link.style.cssText = "position:fixed;top:40%;left:40%;z-index:2147483647;padding:1em;background:#fff";
      document.body.append(link);
    }, href);
    await page.click("#spike-link", { timeout: 10_000 });
    // Either the destination or Chromium's error page replaces the public page.
    const left = await page
      .waitForURL((url) => !url.href.startsWith(PUBLIC_PAGE), { timeout: 10_000, waitUntil: "commit" })
      .then(() => true, () => false);
    if (left) await page.waitForLoadState("load", { timeout: 10_000 }).catch(() => {});
    const directoryLog = await stubLog();
    return {
      clickedFrom: PUBLIC_PAGE,
      href,
      leftThePublicPage: left,
      finalUrl: page.url(),
      consoleMessages,
      failedRequests,
      directoryLog,
      reachedDirectory: directoryLog.length > 0,
    };
  } finally {
    await page.close();
  }
}

/** Service-worker targets the browser currently has, by URL. Empty when the extension's worker is stopped. */
async function serviceWorkerTargets(context) {
  const session = await context.browser().newBrowserCDPSession();
  try {
    const { targetInfos } = await session.send("Target.getTargets");
    return targetInfos.filter((target) => target.type === "service_worker").map((target) => target.url);
  } finally {
    await session.detach();
  }
}

/**
 * Stops the extension's service worker through the DevTools protocol and
 * reports the proof: the worker's running status and the absence of a target.
 * The returned statuses array keeps growing, so a later look shows a restart.
 */
async function stopServiceWorker(context, extensionId, pageName) {
  const page = await context.newPage();
  await page.goto(`chrome-extension://${extensionId}/${pageName}`);
  const session = await context.newCDPSession(page);
  const statuses = [];
  session.on("ServiceWorker.workerVersionUpdated", ({ versions }) => {
    for (const version of versions) statuses.push(version.runningStatus);
  });
  await session.send("ServiceWorker.enable");
  await poll(() => statuses.at(-1), (status) => status !== undefined);
  const before = statuses.at(-1);
  await session.send("ServiceWorker.stopAllWorkers");
  await poll(() => statuses.at(-1), (status) => status === "stopped");
  const targets = await poll(() => serviceWorkerTargets(context), (found) => found.length === 0);
  return {
    statuses,
    statusBeforeStop: before,
    statusAfterStop: statuses.at(-1),
    targetsAfterStop: targets,
    stopped: statuses.at(-1) === "stopped" && targets.length === 0,
  };
}

// ---------------------------------------------------------------------------
// Checks

// Other ways a page can send its user to a go link. Recorded, not judged:
// [name, the page it starts from, what happens there]. A null page means the
// navigation itself is the shape (a server redirect, a meta refresh).
const SHAPES = [
  ["https-page-link-new-tab", PUBLIC_PAGE, "blank"],
  ["https-page-window-open", PUBLIC_PAGE, "window-open"],
  ["https-page-location-href", PUBLIC_PAGE, "location-href"],
  ["https-page-form-get", PUBLIC_PAGE, "form-get"],
  ["https-page-form-post", PUBLIC_PAGE, "form-post"],
  ["https-page-iframe-link-target-top", PUBLIC_PAGE, "iframe-top"],
  ["http-page-link", THIRD_PARTY_PAGE, "link"],
  ["http-page-form-get", THIRD_PARTY_PAGE, "form-get"],
  ["http-page-form-post", THIRD_PARTY_PAGE, "form-post"],
  ["directory-page-link", `${DIRECTORY_URL}/`, "link"],
  ["server-302", null, `${THIRD_PARTY_URL}/redirect?to=`],
  ["meta-refresh", null, `${THIRD_PARTY_URL}/meta?to=`],
];

/** Adds one element to the page that leads to url in the given way, and clicks it with the mouse. */
async function actOnPage(page, kind, url) {
  await page.evaluate(
    ([kind, url]) => {
      let element;
      if (kind === "link" || kind === "blank") {
        element = Object.assign(document.createElement("a"), { href: url, textContent: "spike go link" });
        if (kind === "blank") Object.assign(element, { target: "_blank", rel: "noopener" });
      } else if (kind === "window-open" || kind === "location-href") {
        element = Object.assign(document.createElement("button"), { textContent: kind });
        element.addEventListener("click", () => (kind === "window-open" ? window.open(url) : (location.href = url)));
      } else if (kind === "form-get" || kind === "form-post") {
        const form = Object.assign(document.createElement("form"), { action: url, method: kind === "form-get" ? "get" : "post" });
        form.append(Object.assign(document.createElement("input"), { type: "hidden", name: "q", value: "a b" }));
        element = Object.assign(document.createElement("button"), { textContent: kind });
        form.append(element);
        document.body.append(form);
      } else {
        element = document.createElement("iframe");
        element.srcdoc = `<a id="inner" target="_top" href="${url}">inner link</a>`;
      }
      element.id = "spike-act";
      // On top of everything: the public page may cover its own top-left corner.
      element.style.cssText = "position:fixed;top:40%;left:40%;z-index:2147483647;padding:1em;background:#fff";
      if (!element.isConnected) document.body.append(element);
    },
    [kind, url],
  );
  if (kind === "iframe-top") await page.frameLocator("#spike-act").locator("#inner").click({ timeout: 10_000 });
  else await page.click("#spike-act", { timeout: 10_000 });
}

/**
 * Runs every shape in one browser and reports, per shape, the request the stub
 * logged for it (or that none came). The control gives up on a shape as soon
 * as a tab shows Chromium's error page.
 */
async function runShapes(context, { isControl }) {
  const results = [];
  for (const [name, from, kind] of SHAPES) {
    const target = `http://go/hit/c4-${name}`;
    const result = { shape: name, from: from ?? `${kind}${target}`, target };
    await clearStubLog();
    const before = new Set(context.pages());
    const page = await context.newPage();
    try {
      if (from) {
        const opened = await navigate(page, from);
        if (opened.error) {
          results.push({ ...result, unavailable: `${from} could not be loaded: ${opened.error}` });
          continue;
        }
        await actOnPage(page, kind, target);
      } else {
        result.gotoError = (await navigate(page, kind + encodeURIComponent(target))).error;
      }
      const opened = () => context.pages().filter((candidate) => !before.has(candidate));
      const seen = await poll(
        async () => ({
          entry: (await stubLog()).find((logged) => logged.escapedPath === `/hit/c4-${name}`),
          errorPage: opened().some((candidate) => candidate.url().startsWith("chrome-error://")),
        }),
        (now) => Boolean(now.entry) || (isControl && now.errorPage),
        isControl ? 3000 : 6000,
      );
      result.arrived = Boolean(seen.entry);
      result.request = seen.entry ? { method: seen.entry.method, requestURI: seen.entry.requestURI, ...fetchHeaders(seen.entry) } : null;
      if (seen.entry) await sleep(300); // let the tab reach the destination before its address is read
      result.tabsEndedOn = opened().map((candidate) => candidate.url());
      // What Chromium's error or warning page says, when a tab ended on one.
      for (const candidate of opened()) {
        if (!candidate.url().startsWith("chrome-error://")) continue;
        const text = await candidate.evaluate(() => document.body.innerText).catch(() => "");
        result.errorPageSays = text.replace(/\s+/g, " ").trim().slice(0, 200);
      }
    } catch (error) {
      result.error = firstLine(error);
    } finally {
      for (const candidate of context.pages()) if (!before.has(candidate)) await candidate.close().catch(() => {});
    }
    results.push(result);
  }
  return results;
}

check(4, "A go link clicked on a third-party HTTPS page redirects, with host access to go alone", "automated", async () => {
  const href = "http://go/hit/c4-click";
  const manifest = JSON.parse(readFileSync(path.join(EXTENSION_DIR, "manifest.json"), "utf8"));
  const withExtension = await withBrowser({}, ({ context }) => clickLinkOnPublicPage(context, href));
  const control = await withBrowser({ extension: false }, ({ context }) => clickLinkOnPublicPage(context, href));
  // Recorded, not judged: the other ways of getting from a page to a go link.
  const shapes = await withBrowser({ extraArgs: [mapThirdParty] }, ({ context }) => runShapes(context, { isControl: false }));
  const shapesControl = await withBrowser({ extension: false, extraArgs: [mapThirdParty] }, ({ context }) =>
    runShapes(context, { isControl: true }),
  );
  const variants = shapes.map((shape, index) => ({ ...shape, controlWithoutExtension: shapesControl[index] }));
  const evidence = { hostPermissions: manifest.host_permissions, withExtension, control, variants };

  if (withExtension.unreachable || control.unreachable) {
    return { status: "inconclusive", observed: `${PUBLIC_PAGE} could not be loaded, so no link could be clicked.`, evidence };
  }
  const entry = withExtension.directoryLog.find((logged) => logged.requestURI === "/hit/c4-click");
  const arrived = Boolean(entry) && withExtension.finalUrl === DEST_URL;
  evidence.headersSeenByStub = fetchHeaders(entry);
  if (control.reachedDirectory) {
    return { status: "inconclusive", observed: "The control without the extension also reached the stub, so the test proves nothing.", evidence };
  }
  const describeVariant = (variant) => {
    if (variant.unavailable) return `${variant.shape}: not run (${variant.unavailable})`;
    const controlNote = variant.controlWithoutExtension.arrived ? "CONTROL ALSO ARRIVED" : "control missed";
    if (!variant.arrived) {
      return (
        `${variant.shape}: did NOT arrive${variant.error ? ` (${variant.error})` : ""}, tabs ended on ${JSON.stringify(variant.tabsEndedOn)}` +
        `${variant.errorPageSays ? ` saying ${JSON.stringify(variant.errorPageSays)}` : ""}; ${controlNote}`
      );
    }
    const request = variant.request;
    return (
      `${variant.shape}: ${request.method} ${request.requestURI}, Sec-Fetch-Site "${request.secFetchSite}", Sec-Fetch-User "${request.secFetchUser}", ` +
      `Referer "${request.referer}", Origin "${request.origin}"; ${controlNote}`
    );
  };
  return {
    status: arrived ? "pass" : "fail",
    observed:
      (arrived
        ? `Host permissions ${JSON.stringify(manifest.host_permissions)}, none for the public page. ` +
          `A real click on <a href="${href}"> injected into ${PUBLIC_PAGE} ended on ${withExtension.finalUrl}; the stub logged GET ${entry.requestURI} ` +
          `with Sec-Fetch-Site "${entry.secFetchSite}", Sec-Fetch-Mode "${entry.secFetchMode}", Sec-Fetch-Dest "${entry.secFetchDest}", ` +
          `Sec-Fetch-User "${entry.secFetchUser}", Referer "${entry.referer}". Without the extension the same click ended on ${control.finalUrl} and the stub saw nothing. `
        : `The click did not reach the stub: final URL ${withExtension.finalUrl}, stub log ${JSON.stringify(uris(withExtension.directoryLog))}. `) +
      `Only that same-tab click from an HTTPS page is judged. Other ways to a go link, recorded as they arrived at the stub ` +
      `(the http page is a local server under the name ${new URL(THIRD_PARTY_URL).host}, which the extension has no access to): ` +
      variants.map(describeVariant).join(". ") +
      `. A link that navigates inside an iframe is covered by the check "extra-subresource".`,
    evidence,
  };
}, { timeout: 200_000 });

check(6, "https://go/ redirects, and http://go/ redirects with HTTPS-First mode on", "proxy", async () => {
  const decoy = await startStub({ role: "decoy" });
  try {
    const https = await visitWithAndWithout("https://go/hit/c6-https");
    const httpsOk = https.withExtension.reachedDirectory && https.withExtension.finalUrl === DEST_URL && !https.control.reachedDirectory;

    // Baseline: with the setting off, the HTTP-only site loads. (Chromium's
    // default, the HttpsUpgrades feature, first tries it over HTTPS and
    // quietly falls back; the request chain in the evidence shows that.)
    const baseline = await withBrowser({ extension: false, extraArgs: [MAP_HTTP_ONLY_SITE] }, ({ context }) =>
      visit(context, HTTP_ONLY_SITE, { decoy: true }),
    );
    const baselineLoads = baseline.error === null && baseline.decoyLog.length > 0;
    // The same default settings with the extension: what happens to http://go/ when nothing is switched on?
    const defaults = await withBrowser({ extraArgs: [MAP_HTTP_ONLY_SITE] }, async ({ context, extensionId }) => {
      const go = await visit(context, "http://go/hit/c6-defaults");
      return { go, goSeen: (await extensionState(context, extensionId)).goSeen.map((entry) => entry.url) };
    });
    const defaultsOk = defaults.go.reachedDirectory && defaults.go.finalUrl === DEST_URL;

    // "Always use secure connections" has two levels; each is one profile setting.
    const modes = {};
    for (const [name, pref] of [
      ["publicAndPrivateSites", "https_only_mode_enabled"],
      ["publicSitesOnly", "https_first_balanced_mode_enabled"],
    ]) {
      const run = async (extension) => {
        const userDataDir = profileWithPrefs({ [pref]: true });
        const seen = await withBrowser({ extension, userDataDir, extraArgs: [MAP_HTTP_ONLY_SITE] }, async ({ context, extensionId }) => {
          const httpOnlySite = await visit(context, HTTP_ONLY_SITE, { decoy: true });
          const go = await visit(context, `http://go/hit/c6-${name}`);
          const goSeen = extension ? (await extensionState(context, extensionId)).goSeen.map((entry) => entry.url) : undefined;
          return { httpOnlySite, go, goSeen };
        });
        // Chromium rewrites the file on exit; a pref it did not know would be dropped.
        const prefs = JSON.parse(readFileSync(path.join(userDataDir, "Default", "Preferences"), "utf8"));
        return { ...seen, prefAfterExit: { [pref]: prefs[pref] ?? null } };
      };
      const withExtension = await run(true);
      const control = await run(false);
      // The mode is on if the HTTP-only site is tried over HTTPS and then blocked (the warning page).
      const blocked = (seen) =>
        seen.httpOnlySite.error !== null &&
        seen.httpOnlySite.requests.some((url) => url.startsWith("https://godir-spike-probe.com/")) &&
        seen.httpOnlySite.decoyLog.length === 0;
      modes[name] = {
        pref,
        modeVerifiedOn: baselineLoads && blocked(withExtension) && blocked(control),
        redirected: withExtension.go.reachedDirectory && withExtension.go.finalUrl === DEST_URL,
        controlMissed: !control.go.reachedDirectory,
        withExtension,
        control,
      };
    }

    const evidence = { https, httpOnlySiteWithModeOff: baseline, goWithDefaultSettings: defaults, modes };
    const describe = (name) => {
      const mode = modes[name];
      if (!mode.modeVerifiedOn) return `"${mode.pref}": could not confirm the mode was on`;
      const chain = mode.withExtension.go.requests.join(" -> ");
      return (
        `"${mode.pref}" (confirmed on: ${HTTP_ONLY_SITE} was tried over HTTPS and blocked with ${mode.withExtension.httpOnlySite.error?.match(/net::\w+/)?.[0]}): ` +
        (mode.redirected ? `http://go/ reached the stub with no warning page (${chain})` : `http://go/ did NOT reach the stub (${mode.withExtension.go.error})`) +
        `; the extension's onBeforeRequest saw ${JSON.stringify(mode.withExtension.goSeen)}` +
        `; control without the extension: ${mode.control.go.error?.match(/net::\w+/)?.[0] ?? "reached the stub"}`
      );
    };
    const allVerified = Object.values(modes).every((mode) => mode.modeVerifiedOn);
    const anyBroken = Object.values(modes).some((mode) => mode.modeVerifiedOn && !(mode.redirected && mode.controlMissed));
    return {
      status: !httpsOk || !defaultsOk || anyBroken ? "fail" : allVerified ? "pass" : "inconclusive",
      observed:
        `https://go/hit/c6-https ${httpsOk ? "reached the stub (control without the extension: " + https.control.error?.match(/net::\w+/)?.[0] + ")" : "did NOT redirect cleanly"}. ` +
        `Default settings, with Chromium's HttpsUpgrades feature on as in Chrome (Playwright normally switches it off): ` +
        `the HTTP-only site ${HTTP_ONLY_SITE} was requested as ${baseline.requests.join(" -> ")} and ${baselineLoads ? "loaded" : "did NOT load"}; ` +
        `http://go/ ${defaultsOk ? "reached the stub" : "did NOT reach the stub"} (${defaults.go.requests.join(" -> ")}), and the extension's onBeforeRequest saw ${JSON.stringify(defaults.goSeen)}. ` +
        `HTTPS-First mode was switched on through the profile's Preferences file, not the settings page. ` +
        `${describe("publicAndPrivateSites")}. ${describe("publicSitesOnly")}.`,
      evidence,
    };
  } finally {
    await decoy.stop();
  }
}, { timeout: 150_000 });

check(7, "The redirect happens when go does not resolve, and when go resolves somewhere else", "proxy", async () => {
  const decoy = await startStub({ role: "decoy" });
  try {
    const unresolved = await visitWithAndWithout("http://go/hit/c7-unresolved");
    const mapped = await visitWithAndWithout("http://go/hit/c7-mapped", { extraArgs: [MAP_GO_TO_DECOY], decoy: true });
    const evidence = { resolverRule: MAP_GO_TO_DECOY, unresolved, mapped };

    const unresolvedOk = unresolved.withExtension.reachedDirectory && unresolved.withExtension.finalUrl === DEST_URL;
    const mappedOk =
      mapped.withExtension.reachedDirectory && mapped.withExtension.finalUrl === DEST_URL && mapped.withExtension.decoyLog.length === 0;
    // The controls prove the setup: go really does not resolve, and the resolver rule really sends go to the decoy.
    const controlsOk =
      !unresolved.control.reachedDirectory &&
      /ERR_NAME_NOT_RESOLVED/.test(unresolved.control.error ?? "") &&
      !mapped.control.reachedDirectory &&
      mapped.control.decoyLog.some((entry) => entry.host === "go" && entry.requestURI === "/hit/c7-mapped");
    if (!controlsOk) {
      return { status: "inconclusive", observed: "A control did not behave as the setup assumes; see evidence.", evidence };
    }
    return {
      status: unresolvedOk && mappedOk ? "pass" : "fail",
      observed:
        `go unresolved: with the extension the stub logged ${JSON.stringify(uris(unresolved.withExtension.directoryLog))}; without it, ${unresolved.control.error}. ` +
        `go mapped to the decoy on 4656 (a resolver rule standing in for a hosts entry): with the extension the Directory stub logged ` +
        `${JSON.stringify(uris(mapped.withExtension.directoryLog))} and the decoy logged ${mapped.withExtension.decoyLog.length} requests; ` +
        `without it the decoy logged ${JSON.stringify(mapped.control.decoyLog.map((entry) => entry.host + entry.requestURI))}.`,
      evidence,
    };
  } finally {
    await decoy.stop();
  }
});

check(8, "The query reaches the stub and the fragment survives to the destination", "proxy", async () => {
  // Two shapes: the stub answers 302 (the fragment must survive two redirects) or 200 (only the extension's).
  const viaRedirect = await visitWithAndWithout("http://go/hit/foo?x=1#frag");
  const direct = await withBrowser({}, ({ context }) => visit(context, "http://go/m8?x=1#frag"));
  const evidence = { viaRedirect, direct };

  const viaRedirectURI = documentRequest(viaRedirect.withExtension.directoryLog)?.requestURI;
  const directURI = documentRequest(direct.directoryLog)?.requestURI;
  const queryOk = viaRedirectURI === "/hit/foo?x=1" && directURI === "/m8?x=1";
  const fragmentOk = viaRedirect.withExtension.finalUrl === `${DEST_URL}#frag` && direct.finalUrl === `${DIRECTORY_URL}/m8?x=1#frag`;
  if (viaRedirect.control.reachedDirectory) {
    return { status: "inconclusive", observed: "The control without the extension also reached the stub.", evidence };
  }
  return {
    status: queryOk && fragmentOk ? "pass" : "fail",
    observed:
      `http://go/hit/foo?x=1#frag: the stub received requestURI ${JSON.stringify(viaRedirectURI)} and the tab ended on ${viaRedirect.withExtension.finalUrl}. ` +
      `http://go/m8?x=1#frag (stub answers 200): requestURI ${JSON.stringify(directURI)}, tab on ${direct.finalUrl}. ` +
      `Control without the extension: ${viaRedirect.control.error}.`,
    evidence,
  };
});

check(9, "go/a%20b%2Fc reaches the stub byte for byte", "proxy", async () => {
  const main = await visitWithAndWithout("http://go/a%20b%2Fc");
  // Recorded, not judged: what Chromium sends for awkward paths and hosts.
  const hostile = [
    "http://go//double",
    "http://go/a/../b",
    "http://go/a/./b",
    "http://go/%2e%2e/x",
    "http://go/a/%2e/b",
    "http://go/a/%2E%2E/b",
    "http://go/a/%2E%2E%2Fb",
    "http://go/a/..%2Fb",
    "http://go/a%2fb%2Fc",
    "http://go/CamelCase/UPPER",
    "http://go/zażółć/日本",
    "http://go/a+b?q=c+d",
    "http://go/a b?q=c d",
    "http://go/trailing-space ",
    "http://go/a\tb",
    "http://go/a%3Fb%23c",
    "http://go/a%2520b",
    "http://go/a\\b",
    'http://go/a|"<>b',
    "http://go/a%00b",
    "http://go/a%0Ab",
    "http://go/a?x=%2F&y=a%26b",
    // Not valid percent-encoding: Go's HTTP server refuses these before any handler runs.
    "http://go/a%zzb",
    "http://go/%",
    `http://go/${"long".repeat(1000)}`,
    // Other spellings of the host.
    "http://GO/upper-host",
    "http://go:80/default-port",
    "http://go./trailing-dot",
    "http://go:8080/other-port",
    "http://user@go/userinfo",
  ];
  const variants = await withBrowser({}, async ({ context }) => {
    const seen = [];
    for (const url of hostile) {
      const result = await visit(context, url);
      const requestURI = documentRequest(result.directoryLog)?.requestURI ?? null;
      let outcome = `not redirected (${result.error?.match(/net::\w+/)?.[0] ?? result.error ?? "no error"})`;
      if (requestURI !== null) outcome = "arrived";
      else if (result.httpStatus !== null && result.finalUrl.startsWith(DIRECTORY_URL)) {
        // The tab is on the Directory's origin, yet the stub's handler logged nothing.
        outcome = `redirected, but answered HTTP ${result.httpStatus} before the stub's handler ran`;
      }
      seen.push({ asked: url, outcome, requestURI, httpStatus: result.httpStatus, finalUrl: result.finalUrl, error: result.error });
    }
    return seen;
  });
  const evidence = { main, variants };

  const received = documentRequest(main.withExtension.directoryLog)?.requestURI;
  if (main.control.reachedDirectory) {
    return { status: "inconclusive", observed: "The control without the extension also reached the stub.", evidence };
  }
  const short = (text) => JSON.stringify(text.length > 80 ? `${text.slice(0, 20)}… (${text.length} characters)` : text);
  const show = (variant) =>
    `${short(variant.asked.slice("http://".length))} -> ${variant.requestURI !== null ? short(variant.requestURI) : variant.outcome}`;
  return {
    status: received === "/a%20b%2Fc" ? "pass" : "fail",
    observed:
      `http://go/a%20b%2Fc arrived as requestURI ${JSON.stringify(received)} (control without the extension: ${main.control.error}). ` +
      `Other inputs, recorded and not judged, as "asked -> requestURI the stub received" (or what happened instead): ` +
      variants.map(show).join(", ") +
      `. These went through page.goto, so they show Chromium's URL handling, not what the address bar does with typed text.`,
    evidence,
  };
}, { timeout: 120_000 });

check(11, "A go link clicked on a public page reaches the loopback stub with no Local Network Access prompt or block", "proxy", async () => {
  /** Is Local Network Access enforced at all? Ask for a loopback sub-resource from the public page. */
  const probeSubResource = async (context) => {
    await clearStubLog();
    const page = await context.newPage();
    const consoleMessages = [];
    page.on("console", (message) => consoleMessages.push(`${message.type()}: ${message.text()}`));
    await page.goto(PUBLIC_PAGE);
    // Nobody can answer a permission prompt here, so a prompt shows up as "no answer".
    const probe = await page.evaluate(async (url) => {
      const result = { userAgent: navigator.userAgent };
      try {
        result.permissionState = (await navigator.permissions.query({ name: "local-network-access" })).state;
      } catch (error) {
        result.permissionState = `query failed: ${error.message}`;
      }
      result.fetch = await Promise.race([
        fetch(url, { mode: "no-cors" }).then((response) => `resolved (${response.type})`, (error) => `rejected: ${error.message}`),
        new Promise((resolve) => setTimeout(() => resolve("no answer after 5 s"), 5000)),
      ]);
      return result;
    }, `${DIRECTORY_URL}/c11-lna-fetch-probe`);
    probe.consoleMessages = consoleMessages;
    probe.stubSawIt = (await stubLog()).some((entry) => entry.requestURI === "/c11-lna-fetch-probe");
    await page.close();
    return probe;
  };
  const run = (label, options) =>
    withBrowser(options, async ({ context }) => {
      const version = await chromiumVersion(context);
      const commandLine = launchNotes().commandLine;
      const click = await clickLinkOnPublicPage(context, `http://go/hit/c11-${label}`);
      return {
        version,
        fieldTrialSwitches: commandLine.match(/--(?:enable|disable)-field-trial-config/g) ?? [],
        click,
        subResourceProbe: click.unreachable ? null : await probeSubResource(context),
      };
    });

  const seen = await run("click", {});
  // The same again with Chromium's field-trial testing configuration switched
  // on: features under trial get the state their trials would give them. It is
  // not Chrome stable's server-side configuration, only a step nearer to it.
  const withTrials = await run("trials", {
    ignoreDefaultArgs: ["--disable-field-trial-config"],
    extraArgs: ["--enable-field-trial-config"],
  });
  // For comparison: a plain link from the public page straight to the loopback stub, no extension involved.
  const directLink = await withBrowser({ extension: false }, ({ context }) =>
    clickLinkOnPublicPage(context, `${DIRECTORY_URL}/hit/c11-direct`),
  );
  const { version, ...defaults } = seen;
  const evidence = { chromiumVersion: version, ...defaults, withFieldTrialTestingConfig: withTrials, directLinkWithoutExtension: directLink };

  if (seen.click.unreachable || withTrials.click.unreachable) {
    return { status: "inconclusive", observed: `${PUBLIC_PAGE} could not be loaded, so no link could be clicked.`, evidence };
  }
  const arrivedAt = (click, uri) => click.finalUrl === DEST_URL && click.directoryLog.some((entry) => entry.requestURI === uri);
  const quietly = (click) => click.failedRequests.length === 0 && !click.consoleMessages.some((message) => /^(error|warning|pageerror)/.test(message));
  const { click, subResourceProbe: probe } = seen;
  const arrived = arrivedAt(click, "/hit/c11-click");
  const trialsArrived = arrivedAt(withTrials.click, "/hit/c11-trials");
  const quiet = quietly(click) && quietly(withTrials.click);
  return {
    status: arrived && trialsArrived && quiet ? "pass" : arrived && trialsArrived ? "inconclusive" : "fail",
    observed:
      `PROXY ONLY: headless Chromium ${version}, which cannot show a permission prompt, runs with field trials off ` +
      `(${seen.fieldTrialSwitches.join(" ")}), and is not the Chrome stable build; Local Network Access rules differ between versions and are ` +
      `partly switched on by field trials. A person must confirm this in Chrome stable. ` +
      `Click on ${PUBLIC_PAGE} to http://go/hit/c11-click: ` +
      (arrived ? `arrived at ${click.finalUrl} with no interaction` : `did NOT arrive (final URL ${click.finalUrl})`) +
      `; failed requests: ${click.failedRequests.length}; console messages: ${JSON.stringify(click.consoleMessages)}. ` +
      `A navigation that needed the permission could not have been granted it here, so it would not have arrived. ` +
      `Comparison, same public page: fetch() of a loopback URL ${probe.fetch} (stub saw it: ${probe.stubSawIt}; ` +
      `permission state "${probe.permissionState}"; console: ${JSON.stringify(probe.consoleMessages[0] ?? null)}), ` +
      `so Local Network Access is enforced for sub-resources in this build. ` +
      `With Chromium's field-trial testing configuration on (${withTrials.fieldTrialSwitches.join(" ")}): the click ` +
      `${trialsArrived ? "arrived the same way" : `did NOT arrive (final URL ${withTrials.click.finalUrl})`}, and fetch() ${withTrials.subResourceProbe.fetch}. ` +
      `A plain link to ${DIRECTORY_URL} without the extension ended on ${directLink.finalUrl}.`,
    evidence,
  };
}, { timeout: 120_000 });

/** A copy of the spike extension whose only host access is the Directory's origin, in a new temp directory. */
function writeOriginOnlyExtension() {
  const dir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-origin-only-"));
  cpSync(EXTENSION_DIR, dir, { recursive: true });
  const manifestPath = path.join(dir, "manifest.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  manifest.name = "godir spike (Directory origin only)";
  manifest.host_permissions = [`${DIRECTORY_URL}/*`];
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2));
  return dir;
}

check(12, "With webRequest and host access to the Directory, onErrorOccurred fires when the stub is stopped, and the tab is replaced by an extension page", "automated", async () => {
  const manifest = JSON.parse(readFileSync(path.join(EXTENSION_DIR, "manifest.json"), "utf8"));
  const originOnlyDir = writeOriginOnlyExtension();
  // The tab's address is polled: a waitForURL would give up at the first failed navigation.
  const settlesOn = async (page, prefix, timeout = 10_000) => (await poll(() => page.url(), (url) => url.startsWith(prefix), timeout)).startsWith(prefix);
  const errorsOf = async (context, extensionId) => (await extensionState(context, extensionId)).directoryErrors;

  let seen;
  try {
    seen = await withBrowser({ extraArgs: [mapThirdParty] }, async ({ context, extensionId }) => {
      const unreachablePrefix = `chrome-extension://${extensionId}/unreachable.html?`;
      // Each tab starts on a page that does not depend on the Directory, so Back has somewhere to go.
      const tab = await context.newPage();
      await tab.goto(THIRD_PARTY_PAGE);
      await directory.stop();
      try {
        // 1. A go link while the Directory is down.
        const navigation = await navigate(tab, "http://go/hit/c12-down");
        const replaced = await settlesOn(tab, unreachablePrefix);
        const tabUrl = tab.url();
        const shown = replaced
          ? {
              title: await tab.locator("#unreachable-title").textContent(),
              originalUrl: await tab.locator("#original-url").textContent(),
              error: await tab.locator("#error").textContent(),
            }
          : null;
        const errorsAfterFirst = await poll(() => errorsOf(context, extensionId), (errors) => errors.length > 0);

        // 2. Retry while it is still down: one more error, the same page again, and no loop.
        let retryWhileDown = null;
        if (replaced) {
          await tab.click("#retry");
          await sleep(3000);
          const errors = await errorsOf(context, extensionId);
          retryWhileDown = { tabUrl: tab.url(), backOnUnreachablePage: tab.url().startsWith(unreachablePrefix), errorsAdded: errors.length - errorsAfterFirst.length };
        }

        // 3. The Back button, in a second tab with the same history.
        const backTab = await context.newPage();
        await backTab.goto(THIRD_PARTY_PAGE);
        await navigate(backTab, "http://go/hit/c12-back");
        let back = null;
        if (await settlesOn(backTab, unreachablePrefix)) {
          const errorsBefore = (await errorsOf(context, extensionId)).length;
          const goBackError = await backTab.goBack({ timeout: 5000 }).then(() => null, firstLine);
          await sleep(3000);
          back = {
            goBackError,
            tabUrl: backTab.url(),
            endedOn: backTab.url().startsWith(unreachablePrefix) ? "the unreachable page again" : backTab.url() === THIRD_PARTY_PAGE ? "the page before" : "somewhere else",
            errorsAdded: (await errorsOf(context, extensionId)).length - errorsBefore,
          };
        }
        await backTab.close();

        // 4. The Directory's own address, without a go redirect in front.
        const directTab = await context.newPage();
        const directNavigation = await navigate(directTab, `${DIRECTORY_URL}/hit/c12-direct`);
        const direct = { navigation: directNavigation, replaced: await settlesOn(directTab, unreachablePrefix), tabUrl: directTab.url() };
        await directTab.close();

        // 5. The checklist's wording, literally: an extension with host access to the Directory's origin and nothing else.
        const originOnly = await withBrowser({ extensionDir: originOnlyDir }, async ({ context: other, extensionId: otherId }) => {
          const page = await other.newPage();
          const navigation = await navigate(page, `${DIRECTORY_URL}/hit/c12-origin-only`);
          const replaced = await settlesOn(page, `chrome-extension://${otherId}/unreachable.html?`);
          const errors = await poll(() => errorsOf(other, otherId), (found) => found.length > 0);
          const permissions = await inExtensionPage(other, otherId, "status.html", () => chrome.permissions.getAll());
          return { permissions, navigation, replaced, tabUrl: page.url(), directoryErrors: errors };
        });

        // 6. Bring the Directory back and use the page's Retry link.
        directory = await startStub();
        let retry = null;
        if (replaced) {
          await tab.click("#retry");
          await settlesOn(tab, DEST_URL);
          retry = { finalUrl: tab.url(), directoryLog: uris(await stubLog()) };
        }
        return {
          extensionId, navigation, replaced, tabUrl, shown, retryWhileDown, back, direct, originOnly, retry,
          directoryErrors: await errorsOf(context, extensionId),
        };
      } finally {
        // Later checks need the Directory whatever happened above.
        if (!(await fetch(`${DIRECTORY_URL}/-/spike/log`).then((response) => response.ok, () => false))) directory = await startStub();
      }
    });
  } finally {
    rmSync(originOnlyDir, { recursive: true, force: true });
  }
  const evidence = { permissions: manifest.permissions, hostPermissions: manifest.host_permissions, ...seen };

  const recorded = seen.directoryErrors.find((entry) => entry.url === `${DIRECTORY_URL}/hit/c12-down`);
  const originOnly = seen.originOnly;
  const originOnlyRecorded = originOnly.directoryErrors.find((entry) => entry.url === `${DIRECTORY_URL}/hit/c12-origin-only`);
  const mainOk = Boolean(recorded) && seen.replaced;
  const originOnlyOk = Boolean(originOnlyRecorded) && originOnly.replaced;
  return {
    status: mainOk && originOnlyOk ? "pass" : "fail",
    observed:
      `Spike extension (host access ${JSON.stringify(manifest.host_permissions)}, no "tabs" permission), go link while the stub was stopped: ` +
      (recorded ? `onErrorOccurred recorded "${recorded.error}" for ${recorded.url}; ` : `onErrorOccurred recorded nothing for the Directory URL; `) +
      (seen.replaced
        ? `chrome.tabs.update replaced the tab with ${seen.tabUrl}, which shows "${seen.shown.title}", ${seen.shown.originalUrl}, ${seen.shown.error}. `
        : `the tab was NOT replaced; it stayed on ${seen.tabUrl}. `) +
      (recorded
        ? `At the moment of the error chrome.tabs.get gave url ${JSON.stringify(recorded.tabUrlAtError)} and pendingUrl ${JSON.stringify(recorded.tabPendingUrlAtError)} ` +
          `for the tab: it still held the page before (${THIRD_PARTY_PAGE}, which the extension may not read) and no pending address, ` +
          `so "does the tab show the failed URL" cannot be decided inside the event handler. `
        : "") +
      (seen.retryWhileDown
        ? `Retry while still down: ${seen.retryWhileDown.errorsAdded} more error(s) in 3 s and the tab ` +
          `${seen.retryWhileDown.backOnUnreachablePage ? "was on the unreachable page again" : `ended on ${seen.retryWhileDown.tabUrl}`}. `
        : "") +
      (seen.back
        ? `Back button, from a tab whose history was ${THIRD_PARTY_PAGE} then the go link: the tab ended on ${seen.back.endedOn} ` +
          `(${seen.back.errorsAdded} more error(s); goBack reported ${JSON.stringify(seen.back.goBackError)}). `
        : `Back button: not tried, the second tab was not replaced. `) +
      `Typing the Directory's own address (${DIRECTORY_URL}/hit/c12-direct): tab ${seen.direct.replaced ? "replaced" : `NOT replaced (${seen.direct.tabUrl})`}. ` +
      `Copy of the extension with host access to ${JSON.stringify(originOnly.permissions.origins)} only, same direct address: ` +
      (originOnlyRecorded ? `recorded "${originOnlyRecorded.error}" and ` : `recorded NOTHING and `) +
      `the tab was ${originOnly.replaced ? "replaced" : `NOT replaced (${originOnly.tabUrl})`}. ` +
      (seen.retry ? `After the stub was restarted, the page's Retry link ended on ${seen.retry.finalUrl}.` : ""),
    evidence,
  };
}, { timeout: 120_000 });

check(13, "webRequest.onBeforeRequest on *://go/* sees go navigations", "proxy", async () => {
  const urls = ["http://go/hit/seen", "https://go/hit/seen-https"];
  const clicked = "http://go/hit/seen-click";
  const seen = await withBrowser({ extraArgs: [mapThirdParty] }, async ({ context, extensionId }) => {
    const page = await context.newPage();
    const navigations = [];
    for (const url of urls) navigations.push(await navigate(page, url));
    // A link click, the nearest thing to a user-initiated navigation a script
    // can make, from a page the extension has no host access to.
    await page.goto(THIRD_PARTY_PAGE);
    await actOnPage(page, "link", clicked);
    await page.waitForURL(DEST_URL, { timeout: 10_000 }).catch(() => {});
    const state = await poll(() => extensionState(context, extensionId), (current) => current.goSeen.length >= 3);
    return { navigations, clickedFrom: THIRD_PARTY_PAGE, tabAfterClick: page.url(), goSeen: state.goSeen };
  });

  const wanted = [...urls, clicked];
  const seenUrls = seen.goSeen.map((entry) => entry.url);
  const missing = wanted.filter((url) => !seenUrls.includes(url));
  return {
    status: missing.length === 0 ? "pass" : "fail",
    observed:
      `After page.goto of ${urls.join(" and ")} and a link click to ${clicked} on ${THIRD_PARTY_PAGE} (a page the extension has no host access to), ` +
      `storage key goSeen held ${JSON.stringify(seenUrls)}` +
      (missing.length ? `; missing: ${JSON.stringify(missing)}. ` : `. `) +
      `Typed navigations cannot be automated: this shows the listener sees programmatic and clicked go navigations before the redirect, not that it sees typed ones.`,
    evidence: seen,
  };
});

// The rule exactly as background.js installs it, for the rule-only variant below.
const RULE_ONLY_BACKGROUND = `// Generated by checks.spec.mjs for check 14. Installs rule 1 once, ever, and
// registers nothing that could wake the worker on a navigation.
let queue = Promise.resolve();
function log(event) {
  queue = queue.then(async () => {
    const { lifecycle } = await chrome.storage.local.get({ lifecycle: [] });
    await chrome.storage.local.set({ lifecycle: [...lifecycle, event] });
  });
  return queue;
}
log("workerStarted");
chrome.runtime.onInstalled.addListener(async (details) => {
  await log("onInstalled:" + details.reason);
  const { lifecycle } = await chrome.storage.local.get({ lifecycle: [] });
  if (lifecycle.includes("ruleInstalled")) return;
  await chrome.declarativeNetRequest.updateDynamicRules({
    removeRuleIds: [1],
    addRules: [{
      id: 1,
      priority: 1,
      condition: { regexFilter: "^https?://go/(.*)$", resourceTypes: ["main_frame"] },
      action: { type: "redirect", redirect: { regexSubstitution: "http://localhost:4655/\\\\1" } },
    }],
  });
  await log("ruleInstalled");
});
`;

/** Writes the rule-only extension to a new temp directory and returns the path. */
function writeRuleOnlyExtension() {
  const dir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-rule-only-"));
  writeFileSync(
    path.join(dir, "manifest.json"),
    JSON.stringify({
      manifest_version: 3,
      name: "godir spike (rule only)",
      version: "0.0.1",
      permissions: ["declarativeNetRequestWithHostAccess", "storage"],
      host_permissions: ["*://go/*", "http://localhost:4655/*"],
      background: { service_worker: "background.js", type: "module" },
    }),
  );
  writeFileSync(path.join(dir, "background.js"), RULE_ONLY_BACKGROUND);
  writeFileSync(path.join(dir, "page.html"), "<!doctype html><title>rule only</title>\n");
  return dir;
}

/**
 * A go link as the browser's very first navigation: Chromium is started with
 * the URL on its command line (see startChromiumWithUrl), on a profile that
 * already ran once, with or without the spike extension. The stub's log says
 * whether the navigation was redirected; what the extension stored is read in
 * a third launch.
 */
async function startUpNavigation({ extension }) {
  const userDataDir = freshProfileDir();
  const uri = `/hit/c14-startup-${extension ? "extension" : "control"}`;
  // First session: creates the profile and, with the extension, installs the rule.
  await withBrowser({ extension, userDataDir }, () => sleep(300));

  await clearStubLog();
  const started = Date.now();
  const browser = startChromiumWithUrl({ userDataDir, extensionDir: extension ? EXTENSION_DIR : undefined, url: `http://go${uri}` });
  let log;
  try {
    // With the extension, wait for the request; the control gets a fixed 5 s to show that none comes.
    log = await poll(stubLog, (entries) => extension && entries.length > 0, extension ? 15_000 : 5000);
  } finally {
    await browser.stop();
  }
  const result = {
    commandLineArguments: browser.args,
    millisecondsWatched: Date.now() - started,
    directoryLog: log.map((entry) => ({ requestURI: entry.requestURI, secFetchSite: entry.secFetchSite, secFetchUser: entry.secFetchUser })),
    reachedDirectory: log.some((entry) => entry.requestURI === uri),
  };
  if (extension) {
    result.storedByExtension = await withBrowser({ userDataDir }, async ({ context, extensionId }) => {
      const state = await extensionState(context, extensionId);
      return { goSeen: state.goSeen, lifecycle: state.lifecycle };
    });
  }
  return result;
}

check(14, "The redirect works with the service worker stopped, and after a browser restart", "automated", async () => {
  // Runs one extension through both halves of the check in one profile:
  // stop the worker and navigate; then close, relaunch and navigate first thing.
  const run = async (extensionDir, pageName, label, readState) => {
    const userDataDir = freshProfileDir();
    const extensionId = extensionIdForDir(extensionDir);

    const workerStopped = await withBrowser({ extensionDir, userDataDir }, async ({ context }) => {
      const stop = await stopServiceWorker(context, extensionId, pageName);
      const navigation = stop.stopped ? await visit(context, `http://go/hit/c14-${label}-stopped`) : null;
      await sleep(500); // time for the navigation to wake the worker, if anything does
      return {
        stop: { ...stop, statuses: [...stop.statuses] },
        navigation,
        workerTargetsAfterNavigation: await serviceWorkerTargets(context),
        workerStatusesAfterNavigation: stop.statuses.slice(stop.statuses.lastIndexOf("stopped") + 1),
      };
    });

    // Relaunch without waiting for the service worker, and navigate the first tab at once.
    const started = Date.now();
    const { context } = await launchWithoutExtension({
      userDataDir,
      extraArgs: [`--disable-extensions-except=${extensionDir}`, `--load-extension=${extensionDir}`],
    });
    let afterRestart;
    try {
      await clearStubLog();
      const page = context.pages()[0] ?? (await context.newPage());
      const workerTargetsAtNavigation = await serviceWorkerTargets(context);
      const millisecondsFromLaunchToNavigation = Date.now() - started;
      const navigation = await navigate(page, `http://go/hit/c14-${label}-restart`);
      navigation.directoryLog = await stubLog();
      navigation.reachedDirectory = navigation.directoryLog.length > 0;
      afterRestart = { millisecondsFromLaunchToNavigation, workerTargetsAtNavigation, navigation, state: await readState(context, extensionId) };
    } finally {
      await context.close();
    }
    const arrived = (navigation) => Boolean(navigation?.reachedDirectory) && navigation.finalUrl === DEST_URL;
    return {
      extensionId,
      workerStopped,
      afterRestart,
      stoppedOk: workerStopped.stop.stopped && arrived(workerStopped.navigation),
      restartOk: arrived(afterRestart.navigation),
    };
  };

  const spike = await run(EXTENSION_DIR, "status.html", "spike", extensionState);
  const ruleOnlyDir = writeRuleOnlyExtension();
  let ruleOnly;
  try {
    ruleOnly = await run(ruleOnlyDir, "page.html", "rule-only", (context, extensionId) =>
      inExtensionPage(context, extensionId, "page.html", async () => ({
        rules: await chrome.declarativeNetRequest.getDynamicRules(),
        ...(await chrome.storage.local.get({ lifecycle: [] })),
      })),
    );
  } finally {
    rmSync(ruleOnlyDir, { recursive: true, force: true });
  }
  const control = await withBrowser({ extension: false }, ({ context }) => visit(context, "http://go/hit/c14-control"));
  const startUp = await startUpNavigation({ extension: true });
  const startUpControl = await startUpNavigation({ extension: false });
  const evidence = { spikeExtension: spike, ruleOnlyExtension: ruleOnly, control, startUpNavigation: startUp, startUpNavigationControl: startUpControl };

  if (control.reachedDirectory || startUpControl.reachedDirectory) {
    return { status: "inconclusive", observed: "A control without the extension also reached the stub.", evidence };
  }
  if (!spike.workerStopped.stop.stopped || !ruleOnly.workerStopped.stop.stopped) {
    return { status: "inconclusive", observed: "The service worker could not be confirmed stopped before navigating; see evidence.", evidence };
  }
  const lifecycle = ruleOnly.afterRestart.state.lifecycle;
  const ruleInstalls = lifecycle.filter((event) => event === "ruleInstalled").length;
  const ruleOnlyStayedStopped = ruleOnly.workerStopped.workerTargetsAfterNavigation.length === 0;
  const spikeWoke = spike.workerStopped.workerTargetsAfterNavigation.length > 0;
  // The spike extension installs its rule only when it is missing and writes down what it found each time.
  const spikeLifecycle = spike.afterRestart.state.lifecycle.map((entry) => `${entry.event}: rulePresent ${entry.rulePresent}, installedNow ${entry.installedNow}`);
  const spikeInstalls = spike.afterRestart.state.lifecycle.filter((entry) => entry.installedNow).length;
  const allRedirected = spike.stoppedOk && spike.restartOk && ruleOnly.stoppedOk && ruleOnly.restartOk;
  // The proof that the worker is not needed comes from the rule-only extension:
  // nothing wakes its worker. Both extensions must show one rule install over two sessions.
  const proven = ruleOnlyStayedStopped && ruleInstalls === 1 && spikeInstalls === 1;
  return {
    status: !allRedirected ? "fail" : proven && startUp.reachedDirectory ? "pass" : "inconclusive",
    observed:
      `Spike extension: worker stopped through the DevTools protocol (status "${spike.workerStopped.stop.statusAfterStop}", no worker target), ` +
      `then http://go/ ${spike.stoppedOk ? "redirected" : "did NOT redirect"}; the navigation ${spikeWoke ? "woke the worker again (its webRequest listener)" : "did not wake the worker"}. ` +
      `After closing and relaunching on the same profile, the first navigation (${spike.afterRestart.millisecondsFromLaunchToNavigation} ms after launch) ` +
      `${spike.restartOk ? "redirected" : "did NOT redirect"}; worker targets at that moment: ${spike.afterRestart.workerTargetsAtNavigation.length}. ` +
      `Its own log over both sessions: ${JSON.stringify(spikeLifecycle)}, so the rule was ${spikeInstalls === 1 ? "installed once and found present at the second launch" : `installed ${spikeInstalls} times`}. ` +
      `Rule-only extension (installs the rule once, no webRequest listeners): with the worker stopped http://go/ ${ruleOnly.stoppedOk ? "redirected" : "did NOT redirect"} ` +
      `and the worker ${ruleOnlyStayedStopped ? "stayed stopped" : "was running afterwards"}; after the relaunch the first navigation ` +
      `${ruleOnly.restartOk ? "redirected" : "did NOT redirect"}, and its lifecycle log ${JSON.stringify(lifecycle)} shows ${ruleInstalls} rule install(s) over both sessions. ` +
      `Control without the extension: ${control.error}. ` +
      `Go link as the browser's first navigation (Chromium started headless with http://go/... on its command line, on a profile from an earlier session): ` +
      (startUp.reachedDirectory
        ? `redirected, the stub logged ${JSON.stringify(startUp.directoryLog.map((entry) => entry.requestURI))}`
        : `NOT redirected, the stub logged ${JSON.stringify(startUp.directoryLog.map((entry) => entry.requestURI))} in ${startUp.millisecondsWatched} ms`) +
      `; the same start without the extension: stub log ${JSON.stringify(startUpControl.directoryLog.map((entry) => entry.requestURI))} after ${startUpControl.millisecondsWatched} ms. ` +
      `Limits: every launch here loads the extension with --load-extension, which fires onInstalled (reason "install") on each launch and never onStartup, ` +
      `so start-up timing differs from an extension installed the normal way; and a tab restored from the previous session was not tried. ` +
      `The manual run covers the normally installed case (steps L1 to L3).`,
    evidence,
  };
}, { timeout: 240_000 });

check(
  "extra-extension-fetch",
  "Proposed check 17: the extension itself can fetch the loopback Directory (the connection test of L4.2)",
  "proxy",
  async () => {
    const seen = await withBrowser({}, async ({ context, extensionId }) => {
      const page = await context.newPage();
      const consoleMessages = [];
      page.on("console", (message) => consoleMessages.push(`${message.type()}: ${message.text()}`));
      await page.goto(`chrome-extension://${extensionId}/status.html`);
      // status.js asks on load: once from the page, and once through the service worker.
      const meta = await poll(
        async () => {
          try {
            return JSON.parse(await page.locator("#state").textContent()).meta;
          } catch {
            return null; // still "loading"
          }
        },
        (found) => Boolean(found?.page && found?.worker),
        10_000,
      );
      const permissionState = await page.evaluate(() =>
        navigator.permissions.query({ name: "local-network-access" }).then((status) => status.state, (error) => `query failed: ${error.message}`),
      );
      return { meta, permissionState, consoleMessages };
    });
    const ok = (probe) => probe?.ok === true && probe.status === 200 && probe.body.includes('"product":"godir-spike"');
    const describe = (probe) => (probe ? (probe.ok ? `HTTP ${probe.status} ${probe.body.trim()}` : `FAILED: ${probe.error ?? `HTTP ${probe.status}`}`) : "no answer");
    return {
      status: ok(seen.meta?.page) && ok(seen.meta?.worker) ? "pass" : "fail",
      observed:
        `fetch("${DIRECTORY_URL}/-/api/v1/meta") from the extension's status page: ${describe(seen.meta?.page)}; from its service worker: ${describe(seen.meta?.worker)}. ` +
        `Permission state "local-network-access" as the extension page sees it: "${seen.permissionState}". ` +
        `PROXY ONLY: headless Chromium cannot show a permission prompt, so a fetch that needed one would have failed here; that it worked says no prompt was needed in this build. ` +
        `Chrome stable must confirm it (manual step C3).`,
      evidence: seen,
    };
  },
  { assert: false },
);

// For "extra-broad-filter": the listener shape the product needs when the
// Directory's address is only known from storage. The filter is broad and
// static; host permissions decide which requests are delivered at all.
const BROAD_FILTER_BACKGROUND = `// Generated by checks.spec.mjs for the check "extra-broad-filter".
let queue = Promise.resolve();
chrome.webRequest.onErrorOccurred.addListener(
  (details) => {
    queue = queue.then(async () => {
      const { allErrors } = await chrome.storage.local.get({ allErrors: [] });
      await chrome.storage.local.set({ allErrors: [...allErrors, { url: details.url, error: details.error }] });
    });
  },
  { urls: ["http://*/*", "https://*/*"], types: ["main_frame"] },
);
`;

check(
  "extra-broad-filter",
  "Recorded only: onErrorOccurred with a broad static filter is delivered only for hosts the extension has access to",
  "automated",
  async () => {
    // Nothing listens on the decoy's port in this check, so anything sent there is refused.
    const granted = `${DECOY_URL}/granted`;
    const otherHost = "http://no-access.example/refused";
    const unresolved = "http://no-access-unresolved.invalid/";
    const dir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-broad-filter-"));
    writeFileSync(
      path.join(dir, "manifest.json"),
      JSON.stringify({
        manifest_version: 3,
        name: "godir spike (broad filter)",
        version: "0.0.1",
        permissions: ["webRequest", "storage"],
        host_permissions: [`${DECOY_URL}/*`],
        background: { service_worker: "background.js", type: "module" },
      }),
    );
    writeFileSync(path.join(dir, "background.js"), BROAD_FILTER_BACKGROUND);
    writeFileSync(path.join(dir, "page.html"), "<!doctype html><title>broad filter</title>\n");
    try {
      const seen = await withBrowser(
        { extensionDir: dir, waitForRule: false, extraArgs: ["--host-resolver-rules=MAP no-access.example 127.0.0.1:4656"] },
        async ({ context, extensionId }) => {
          const page = await context.newPage();
          const navigations = [];
          for (const url of [otherHost, unresolved, granted]) navigations.push(await navigate(page, url));
          await page.close();
          const read = () => inExtensionPage(context, extensionId, "page.html", async () => (await chrome.storage.local.get({ allErrors: [] })).allErrors);
          const allErrors = await poll(read, (errors) => errors.some((entry) => entry.url.endsWith("/granted")));
          return { navigations, allErrors };
        },
      );
      const allFailed = seen.navigations.every((navigation) => navigation.error !== null);
      const delivered = seen.allErrors.map((entry) => entry.url);
      const onlyGranted = delivered.length > 0 && delivered.every((url) => url === granted);
      return {
        status: !allFailed ? "inconclusive" : onlyGranted ? "pass" : "fail",
        observed:
          `An extension with host access to ${DECOY_URL}/* only, and onErrorOccurred registered at the top level with the filter ` +
          `{urls: ["http://*/*", "https://*/*"], types: ["main_frame"]}. Three navigations failed: ` +
          seen.navigations.map((navigation) => `${navigation.asked} (${navigation.error?.match(/net::\w+/)?.[0]})`).join(", ") +
          `. Events delivered: ${JSON.stringify(seen.allErrors)}. ` +
          (onlyGranted ? "Only the host with access was delivered, so a broad static filter does not widen what the extension sees." : "NOT limited to the host with access."),
        evidence: seen,
      };
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  },
  { assert: false },
);

check(
  "extra-subresource",
  "Recorded only: a sub-frame or fetch to http://go/ is not redirected (the rule lists main_frame only)",
  "automated",
  async () => {
    // go is mapped to the decoy, so a request that is NOT redirected is visible there.
    const decoy = await startStub({ role: "decoy" });
    try {
      const seen = await withBrowser({ extraArgs: [MAP_GO_TO_DECOY] }, async ({ context }) => {
        await clearStubLog();
        await clearStubLog(DECOY_URL);
        const page = await context.newPage();
        await page.goto(`${DIRECTORY_URL}/-/spike/dest`);
        const inPage = await page.evaluate(async () => {
          const frame = document.createElement("iframe");
          const frameLoaded = new Promise((resolve) => {
            frame.addEventListener("load", () => resolve("load event"));
            setTimeout(() => resolve("no load event after 5 s"), 5000);
          });
          frame.src = "http://go/hit/sub-frame";
          document.body.append(frame);
          const fetched = await fetch("http://go/hit/sub-fetch", { mode: "no-cors" }).then(
            (response) => `resolved (${response.type})`,
            (error) => `rejected: ${error.message}`,
          );
          return { frame: await frameLoaded, fetch: fetched };
        });
        const frameUrls = page.frames().map((frame) => frame.url());
        await page.close();
        // Sorted: the frame and the fetch race each other, and the order means nothing.
        const decoyLog = (await stubLog(DECOY_URL)).map((entry) => entry.host + entry.requestURI).sort();
        return { inPage, frameUrls, directoryLog: uris(await stubLog()), decoyLog };
      });
      const redirected = seen.directoryLog.filter((uri) => uri.startsWith("/hit/sub-"));
      const untouched = ["go/hit/sub-frame", "go/hit/sub-fetch"].every((request) => seen.decoyLog.includes(request));
      return {
        status: redirected.length === 0 && untouched ? "pass" : "inconclusive",
        observed:
          `From a page on ${DIRECTORY_URL}, with go mapped to the decoy: an <iframe src="http://go/hit/sub-frame"> (${seen.inPage.frame}) and ` +
          `fetch("http://go/hit/sub-fetch") (${seen.inPage.fetch}) produced Directory log ${JSON.stringify(seen.directoryLog)} and decoy log ${JSON.stringify(seen.decoyLog)}: ` +
          (redirected.length === 0 ? "neither was redirected." : `REDIRECTED: ${JSON.stringify(redirected)}.`),
        evidence: seen,
      };
    } finally {
      await decoy.stop();
    }
  },
  { assert: false },
);
