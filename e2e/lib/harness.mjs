// Helpers for the end-to-end tests: build and run the real server, run a
// fixture destination, create links through the API, and launch Playwright's
// bundled Chromium with or without the extension.
//
// Three rules hold for everything here:
//   - The server never sees the real per-user data directory. Its database
//     must be under the system temp directory, and its home directory is
//     pointed at a temp directory too.
//   - Only Playwright's own Chromium is launched (channel "chromium"), never
//     the installed Google Chrome.
//   - Nothing outlives the test process: servers are killed and temp
//     directories removed on exit, whatever the reason for the exit.

import { chromium } from "@playwright/test";
import { execFileSync, spawn } from "node:child_process";
import { existsSync, mkdtempSync, realpathSync, rmSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { DEFAULT_DIRECTORY_URL, FIRST_RUN_PAGE, SESSION_KEYS, STORAGE_KEYS, buildRedirectRule, sameRedirectRule } from "../../extension/lib/config.js";
import { SET_DIRECTORY } from "../../extension/lib/settings.js";

/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */
/** @typedef {import("@playwright/test").Worker} Worker */
/** @typedef {import("../../extension/lib/settings.js").SetDirectoryReply} SetDirectoryReply */

export const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
export const SERVER_BIN = path.join(REPO_ROOT, "bin", process.platform === "win32" ? "ogre.exe" : "ogre");
export const EXTENSION_DIR = path.join(REPO_ROOT, "extension");

/** The port of the extension's default Directory, 4655. */
export const DEFAULT_PORT = Number(new URL(DEFAULT_DIRECTORY_URL).port);

/**
 * The port the Directory under test listens on: OGRE_E2E_PORT when it is
 * set, otherwise the extension's default.
 *
 * Why it can be moved: from L4 on the developer's own Directory runs on 4655
 * (and from L5 it starts at login), and the tests must never write to it or
 * ask the developer to stop it without a choice. With the default, the
 * extension is tested exactly as installed, with nothing saved; with another
 * port, every browser launched with the extension is first pointed at the
 * test server the way a user points it at a Directory elsewhere
 * (pointExtensionAt).
 *
 * One request can still reach the default address under OGRE_E2E_PORT: the
 * first-run page, which opens at install, asks it whether it runs (a GET of
 * the meta endpoint, which reads and writes nothing) before the extension is
 * pointed elsewhere. launchWithExtension then closes that tab, except in
 * first-run.spec.mjs, where the page follows the saved address by itself.
 * Refusing the request with a Playwright route was tried and dropped: any
 * route makes Playwright intercept every request of the browser, and an
 * intercepted request is reported without its Sec-Fetch-* headers, which
 * redirect.spec.mjs reads.
 *
 * @param {string | undefined} value
 * @returns {number}
 */
function e2ePort(value) {
  if (value === undefined || value === "") return DEFAULT_PORT;
  const port = Number(value);
  if (!/^[0-9]+$/.test(value) || port < 1024 || port > 65535) {
    throw new Error(`OGRE_E2E_PORT must be a port number from 1024 to 65535, got ${JSON.stringify(value)}`);
  }
  return port;
}

const DIRECTORY_PORT = e2ePort(process.env.OGRE_E2E_PORT);

/** The address of the Directory under test, where the extension sends go links during the tests. */
export const DIRECTORY_URL = `http://localhost:${DIRECTORY_PORT}`;

/**
 * True when the Directory under test is at the extension's default address,
 * so the extension needs no setting to reach it. False under OGRE_E2E_PORT.
 */
export const AT_DEFAULT_ADDRESS = DIRECTORY_URL === DEFAULT_DIRECTORY_URL;

// ---------------------------------------------------------------------------
// Cleanup on exit

/** @type {Set<import("node:child_process").ChildProcess>} */
const liveChildren = new Set();
/** @type {Set<string>} */
const tempDirs = new Set();

function cleanupSync() {
  for (const child of liveChildren) {
    try {
      child.kill("SIGKILL");
    } catch {
      // Already gone.
    }
  }
  liveChildren.clear();
  for (const dir of tempDirs) rmSync(dir, { recursive: true, force: true });
  tempDirs.clear();
}

process.on("exit", cleanupSync);
for (const signal of /** @type {const} */ (["SIGINT", "SIGTERM", "SIGHUP"])) {
  // Clean up, then let the signal take its default course.
  process.once(signal, () => {
    cleanupSync();
    process.kill(process.pid, signal);
  });
}

/**
 * @param {number} ms
 * @returns {Promise<void>}
 */
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Creates an empty directory under the system temp directory. It is removed
 * when this process exits.
 *
 * @param {string} prefix for example "ogre-e2e-data-"
 * @returns {string} absolute path
 */
export function tempDir(prefix) {
  const dir = mkdtempSync(path.join(os.tmpdir(), prefix));
  tempDirs.add(dir);
  return dir;
}

/**
 * Throws unless the file would live under the system temp directory. This is
 * what keeps a test from ever opening a real Directory's database.
 *
 * @param {string} file
 */
function assertUnderTempDir(file) {
  const parent = path.dirname(path.resolve(file));
  if (!existsSync(parent)) throw new Error(`the directory of ${file} does not exist; create it with tempDir()`);
  const relative = path.relative(realpathSync(os.tmpdir()), realpathSync(parent));
  if (relative === "" || relative.startsWith("..") || path.isAbsolute(relative)) {
    throw new Error(`refusing to use ${file}: test databases must live in a directory under ${os.tmpdir()}`);
  }
}

// ---------------------------------------------------------------------------
// The server under test

/**
 * Builds bin/ogre from ./cmd/ogre, as `make` would.
 *
 * @returns {string} path of the binary
 * @throws {Error} with the compiler's output when the build fails
 */
export function buildServer() {
  try {
    execFileSync("go", ["build", "-o", path.relative(REPO_ROOT, SERVER_BIN), "./cmd/ogre"], {
      cwd: REPO_ROOT,
      stdio: ["ignore", "pipe", "pipe"],
      encoding: "utf8",
    });
  } catch (error) {
    const failure = /** @type {{ stdout?: string, stderr?: string, message?: string }} */ (error);
    throw new Error(`go build ./cmd/ogre failed:\n${failure.stderr || failure.stdout || failure.message}`);
  }
  return SERVER_BIN;
}

/**
 * True if something accepts TCP connections on this loopback port, over IPv4 or IPv6.
 *
 * @param {number} port
 * @returns {Promise<boolean>}
 */
export async function portInUse(port) {
  /** @type {(host: string) => Promise<boolean>} */
  const accepts = (host) =>
    new Promise((resolve) => {
      const socket = net.connect({ host, port });
      /** @param {boolean} answer */
      const done = (answer) => {
        socket.destroy();
        resolve(answer);
      };
      socket.setTimeout(1000);
      socket.once("connect", () => done(true));
      socket.once("error", () => done(false));
      socket.once("timeout", () => done(false));
    });
  const answers = await Promise.all(["127.0.0.1", "::1"].map(accepts));
  return answers.some(Boolean);
}

/**
 * Says who holds a loopback port, as well as can be told: whether it answers
 * as an Ogre server, and the process id when `lsof` knows it.
 *
 * @param {number} port
 * @returns {Promise<string>} for example "an Ogre server (version 0.1.0), pid 4242"
 */
async function describePortHolder(port) {
  let who = "another program";
  try {
    const response = await fetch(`http://localhost:${port}/-/api/v1/meta`, { signal: AbortSignal.timeout(1000) });
    const meta = /** @type {{ product?: unknown, version?: unknown }} */ (await response.json());
    if (response.ok && meta.product === "ogre") {
      who = `an Ogre server (version ${String(meta.version)}), perhaps your own or one a killed test run left behind`;
    }
  } catch {
    // Not an Ogre server, or not HTTP at all.
  }
  try {
    const pids = execFileSync("lsof", ["-nP", "-t", `-iTCP:${port}`, "-sTCP:LISTEN"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] })
      .split("\n")
      .filter(Boolean);
    if (pids.length > 0) who += `, pid ${[...new Set(pids)].join(" and ")}`;
  } catch {
    // No lsof here, or it found nothing; the name alone has to do.
  }
  return who;
}

/**
 * @typedef {object} RunningServer
 * @property {string} url the Directory URL: DIRECTORY_URL, http://localhost:4655 unless OGRE_E2E_PORT moves it
 * @property {number | undefined} pid
 * @property {string} dbPath
 * @property {() => string} output everything the server has printed so far (the last 64 KB), for failure messages
 * @property {(signal?: NodeJS.Signals) => Promise<string>} stop ends the process, waits for it, and resolves with how it ended: "exit code 0" after a graceful shutdown, "signal SIGKILL" when it was killed. SIGTERM by default, which asks for a graceful shutdown; pass "SIGKILL" to simulate a crash. A server that ignores SIGTERM for 5 seconds is killed, and the answer says so
 */

/**
 * Starts `ogre serve --db <dbPath>` and resolves once GET /-/healthz answers.
 *
 * The server listens at DIRECTORY_URL: its default address, which is the
 * extension's default too, unless OGRE_E2E_PORT names another port. The call
 * refuses to start when that port is already taken:
 * the readiness probe, and every link the tests create, would otherwise go to
 * whatever is listening there, possibly the developer's own Directory.
 *
 * @param {object} options
 * @param {string} options.dbPath database file; must be in a directory made by tempDir()
 * @param {string} [options.bin] server binary; defaults to bin/ogre
 * @param {number} [options.readyTimeout=15000] milliseconds to wait for /-/healthz
 * @returns {Promise<RunningServer>}
 */
export async function startServer({ dbPath, bin = SERVER_BIN, readyTimeout = 15_000 }) {
  assertUnderTempDir(dbPath);
  if (!existsSync(bin)) throw new Error(`server binary missing: ${bin} (call buildServer() first)`);
  if (await portInUse(DIRECTORY_PORT)) {
    throw new Error(
      `port ${DIRECTORY_PORT} is already in use by ${await describePortHolder(DIRECTORY_PORT)}. ` +
        "The tests need that address, and must not write to a real Directory. Stop it, or run the tests " +
        "on another port, for example: OGRE_E2E_PORT=46555 make e2e",
    );
  }

  // The flags decide everything: no OGRE_* setting of the developer's shell
  // reaches the server, and a home directory under temp means that even a
  // server bug could not touch the real data directory.
  const home = tempDir("ogre-e2e-home-");
  /** @type {NodeJS.ProcessEnv} */
  const env = { ...process.env, HOME: home, XDG_DATA_HOME: home, LOCALAPPDATA: home, USERPROFILE: home };
  for (const name of Object.keys(env)) {
    if (name.startsWith("OGRE_")) delete env[name];
  }

  // At the default port the server runs with no address flag, as a user
  // starts it, so the default itself is what is tested.
  const args = AT_DEFAULT_ADDRESS ? ["serve", "--db", dbPath] : ["serve", "--addr", `localhost:${DIRECTORY_PORT}`, "--db", dbPath];
  const child = spawn(bin, args, { cwd: home, env, stdio: ["ignore", "pipe", "pipe"] });
  liveChildren.add(child);

  // Keep the tail for failure messages, and keep the pipes drained so the
  // server never blocks on a full pipe.
  let output = "";
  /** @param {string} chunk */
  const keep = (chunk) => {
    output = (output + chunk).slice(-64 * 1024);
  };
  child.stdout.setEncoding("utf8").on("data", keep);
  child.stderr.setEncoding("utf8").on("data", keep);

  /** @type {Promise<string>} resolves, when the process is gone, with how it went */
  const exited = new Promise((resolve) => {
    child.once("exit", (code, signal) => {
      liveChildren.delete(child);
      resolve(signal ? `signal ${signal}` : `exit code ${code}`);
    });
    child.once("error", (error) => {
      liveChildren.delete(child);
      resolve(`could not start: ${error.message}`);
    });
  });

  /** @type {RunningServer["stop"]} */
  const stop = async (signal = "SIGTERM") => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill(signal);
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 5000);
      await exited;
      clearTimeout(killTimer);
    }
    return exited;
  };

  const healthUrl = `${DIRECTORY_URL}/-/healthz`;
  const deadline = Date.now() + readyTimeout;
  for (;;) {
    const early = await Promise.race([exited, sleep(50).then(() => null)]);
    if (early !== null) throw new Error(`ogre stopped before it was ready (${early}). Its output:\n${output || "(none)"}`);
    try {
      const response = await fetch(healthUrl, { signal: AbortSignal.timeout(1000) });
      await response.arrayBuffer();
      if (response.ok) break;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      await stop("SIGKILL");
      throw new Error(`ogre did not answer ${healthUrl} within ${readyTimeout} ms. Its output:\n${output || "(none)"}`);
    }
  }

  return { url: DIRECTORY_URL, pid: child.pid, dbPath, output: () => output, stop };
}

/**
 * @typedef {object} LinkDraft the body of POST /-/api/v1/links
 * @property {string} [description]
 * @property {{ pattern: string, destination: string, variables?: Record<string, { description?: string, example?: string }> }[]} routes
 */

/**
 * @typedef {object} CreatedLink the fields of the API's link object that the tests read
 * @property {number} id
 * @property {string} name
 * @property {string} description
 * @property {number} version
 * @property {{ id: number, pattern: string, destination: string }[]} routes
 */

/**
 * Creates a link through the API, the way `curl` would.
 *
 * @param {LinkDraft} draft
 * @param {string} [directoryUrl] defaults to DIRECTORY_URL
 * @returns {Promise<CreatedLink>}
 * @throws {Error} naming the status and the response body unless the answer is 201
 */
export async function createLink(draft, directoryUrl = DIRECTORY_URL) {
  const response = await fetch(`${directoryUrl}/-/api/v1/links`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ description: "", ...draft }),
    signal: AbortSignal.timeout(5000),
  });
  const text = await response.text();
  if (response.status !== 201) {
    throw new Error(`POST /-/api/v1/links for ${JSON.stringify(draft.routes)} answered ${response.status}: ${text}`);
  }
  return JSON.parse(text);
}

// ---------------------------------------------------------------------------
// Fixture destination

/**
 * @typedef {object} FixtureRequest
 * @property {string} method
 * @property {string} url path and query as received, for example "/landed?x=1"
 * @property {import("node:http").IncomingHttpHeaders} headers
 */

/**
 * @typedef {object} Fixture
 * @property {string} origin for example "http://127.0.0.1:53124"
 * @property {(pathAndQuery?: string) => string} url an absolute URL on the fixture
 * @property {(pathAndQuery: string) => FixtureRequest[]} requestsFor the requests received for exactly this path and query, oldest first
 * @property {FixtureRequest[]} requests every request received, oldest first (browsers also ask for /favicon.ico)
 * @property {() => Promise<void>} close
 */

/**
 * Starts a web server on a random loopback port that stands in for the site a
 * go link points to. It answers every request with a small page and remembers
 * what it was asked.
 *
 * It is also "another site" as far as the Directory is concerned: a different
 * origin whose pages can link to a go link or try to embed one. `pages` gives
 * such pages by path.
 *
 * @param {object} [options]
 * @param {Record<string, string>} [options.pages] HTML documents to serve, by exact path
 * @returns {Promise<Fixture>}
 */
export async function startFixture({ pages = {} } = {}) {
  /** @type {FixtureRequest[]} */
  const requests = [];
  const server = http.createServer((request, response) => {
    requests.push({ method: request.method ?? "", url: request.url ?? "", headers: request.headers });
    if (request.url === "/favicon.ico") {
      response.writeHead(404).end();
      return;
    }
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" });
    const page = pages[request.url ?? ""];
    if (page !== undefined) {
      response.end(page);
      return;
    }
    response.end('<!doctype html><title>Fixture destination</title><h1 id="fixture">Fixture destination</h1>\n');
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(undefined));
  });
  const address = /** @type {import("node:net").AddressInfo} */ (server.address());
  const origin = `http://127.0.0.1:${address.port}`;

  return {
    origin,
    url: (pathAndQuery = "/") => origin + pathAndQuery,
    requestsFor: (pathAndQuery) => requests.filter((request) => request.url === pathAndQuery),
    requests,
    close: () =>
      new Promise((resolve) => {
        server.close(() => resolve());
        server.closeAllConnections();
      }),
  };
}

// ---------------------------------------------------------------------------
// Browser

// Playwright starts Chromium with a list of features switched off for test
// stability. One of them, HttpsUpgrades, is on in Chrome and decides how an
// http:// navigation such as http://go/x is treated, which is what these tests
// are about. So the harness takes Playwright's list, removes that one feature,
// and passes the rest itself. The list is read from a first throwaway launch
// because ignoreDefaultArgs needs Playwright's switch character for character.
// (Same approach as the L0 spike; see docs/spike-chrome.md, check 6.)
const KEEP_ENABLED = ["HttpsUpgrades"];

/** @type {Promise<{ playwrightDefault: string, replacement: string }> | null} */
let featureSwitches = null;

/**
 * The browser process's command line, read from chrome://version.
 *
 * @param {BrowserContext} context
 * @returns {Promise<string>}
 */
async function browserCommandLine(context) {
  const page = await context.newPage();
  try {
    await page.goto("chrome://version");
    return ((await page.locator("#command_line").textContent()) ?? "").trim();
  } finally {
    await page.close();
  }
}

/** @returns {Promise<{ playwrightDefault: string, replacement: string }>} */
function discoverFeatureSwitches() {
  featureSwitches ??= (async () => {
    const context = await chromium.launchPersistentContext(tempDir("ogre-e2e-probe-"), {
      channel: "chromium",
      headless: true,
    });
    try {
      const defaults = (await browserCommandLine(context)).match(/--disable-features=\S+/g) ?? [];
      if (defaults.length !== 1) {
        throw new Error(`expected one --disable-features switch from Playwright, found ${defaults.length}`);
      }
      const features = defaults[0].slice("--disable-features=".length).split(",");
      const kept = features.filter((feature) => !KEEP_ENABLED.includes(feature));
      return { playwrightDefault: defaults[0], replacement: `--disable-features=${kept.join(",")}` };
    } finally {
      await context.close();
    }
  })();
  return featureSwitches;
}

/**
 * @param {object} options
 * @param {string} [options.userDataDir]
 * @param {string[]} options.args
 * @returns {Promise<BrowserContext>}
 */
async function launch({ userDataDir, args }) {
  const switches = await discoverFeatureSwitches();
  return chromium.launchPersistentContext(userDataDir ?? tempDir("ogre-e2e-profile-"), {
    channel: "chromium",
    headless: true,
    args: [switches.replacement, ...args],
    ignoreDefaultArgs: [switches.playwrightDefault],
  });
}

/**
 * The extension's dynamic rules, asked of its service worker.
 *
 * @param {Worker} serviceWorker
 * @returns {Promise<chrome.declarativeNetRequest.Rule[]>}
 */
export function dynamicRules(serviceWorker) {
  return serviceWorker.evaluate(() => chrome.declarativeNetRequest.getDynamicRules());
}

/**
 * @typedef {object} ExtensionBrowser
 * @property {BrowserContext} context
 * @property {string} extensionId
 * @property {Worker} serviceWorker the worker as it was at launch; Chromium may stop it once it is idle
 * @property {string} userDataDir pass it to another launch to simulate a browser restart
 * @property {() => Promise<void>} close
 */

/**
 * Launches headless Chromium with the unpacked extension and resolves once the
 * extension's service worker runs and, by default, its redirect rule sends go
 * links to `directory`. Without that wait a test could open a go link before
 * the worker has finished installing the rule.
 *
 * When `directory` is not the extension's default, the extension is first
 * pointed there through its own settings (pointExtensionAt), so the browser
 * is in the state a user leaves it in after saving that address.
 *
 * The first-run page, which the worker opens at every launch until it is
 * finished (L0 spike, check 14), is closed again unless `keepFirstRunTab`:
 * left open, it would count go/ navigations of the test as the user's and
 * write that to storage, and under OGRE_E2E_PORT what it shows would
 * depend on whether something runs at the default address on this machine.
 *
 * @param {object} [options]
 * @param {string} [options.extensionDir] defaults to extension/
 * @param {string} [options.userDataDir] defaults to a fresh temp profile; pass an earlier one to simulate a restart
 * @param {string} [options.directory] where go links should go; defaults to DIRECTORY_URL. Pass
 *   DEFAULT_DIRECTORY_URL for a test about the extension as installed, whatever OGRE_E2E_PORT says
 * @param {boolean} [options.waitForRule=true] wait for the rule for `directory` to be the installed one.
 *   With false, and `directory` the default, the call returns as soon as the worker runs
 * @param {number} [options.timeout=15000] milliseconds for the worker, and again for the rule
 * @param {string[]} [options.args] more Chromium switches, such as "--restore-last-session"
 * @param {boolean} [options.keepFirstRunTab=false] leave the first-run page open (first-run.spec.mjs)
 * @returns {Promise<ExtensionBrowser>}
 */
export async function launchWithExtension({
  extensionDir = EXTENSION_DIR,
  userDataDir = tempDir("ogre-e2e-profile-"),
  directory = DIRECTORY_URL,
  waitForRule = true,
  timeout = 15_000,
  args = [],
  keepFirstRunTab = false,
} = {}) {
  const dir = path.resolve(extensionDir);
  if (!existsSync(path.join(dir, "manifest.json"))) throw new Error(`no manifest.json in ${dir}`);

  const context = await launch({
    userDataDir,
    args: [`--disable-extensions-except=${dir}`, `--load-extension=${dir}`, ...args],
  });
  try {
    /** @param {Worker} worker */
    const isOurs = (worker) => worker.url().startsWith("chrome-extension://");
    const serviceWorker =
      context.serviceWorkers().find(isOurs) ??
      (await context.waitForEvent("serviceworker", { predicate: isOurs, timeout }));
    const extensionId = new URL(serviceWorker.url()).host;

    // The install event's work comes first, as on a real install: it gives
    // up access no Directory uses, which a grant made meanwhile would lose.
    await waitForInstallHandled(serviceWorker, timeout);
    if (!keepFirstRunTab) await closeFirstRunTab(context, extensionId, serviceWorker);
    if (directory !== DEFAULT_DIRECTORY_URL) {
      const reply = await pointExtensionAt(context, extensionId, directory);
      if (!reply.ok) throw new Error(`the extension refused the Directory ${directory}: ${reply.message}`);
    }
    if (waitForRule || directory !== DEFAULT_DIRECTORY_URL) {
      await waitForRedirectRule(serviceWorker, directory, timeout);
    }
    return { context, extensionId, serviceWorker, userDataDir, close: () => context.close() };
  } catch (error) {
    await context.close();
    throw error;
  }
}

/**
 * Waits until the service worker has handled the install event that Chromium
 * fires at every launch with --load-extension: the redirect rule checked and
 * the first-run page opened, or found not needed (SESSION_KEYS in
 * extension/lib/config.js). Tests wait for this instead of sleeping.
 *
 * @param {Worker} serviceWorker
 * @param {number} [timeout=15000] milliseconds
 * @returns {Promise<void>}
 */
export async function waitForInstallHandled(serviceWorker, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const handled = await serviceWorker.evaluate((key) => chrome.storage.session.get(key).then((items) => items[key]), SESSION_KEYS.installHandled);
    if (handled !== undefined) return;
    if (Date.now() > deadline) throw new Error(`the extension did not finish handling its install within ${timeout} ms`);
    await sleep(50);
  }
}

/**
 * Playwright reports a tab a moment after Chrome creates it: how long to
 * wait for one the worker has just opened.
 */
export const NEW_TAB_GRACE_MS = 2000;

/**
 * Closes the first-run page the worker opened at launch, if it opened one.
 * Call it once the install event is handled (waitForInstallHandled).
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @param {Worker} serviceWorker
 * @returns {Promise<void>}
 */
async function closeFirstRunTab(context, extensionId, serviceWorker) {
  const url = extensionUrl(extensionId, FIRST_RUN_PAGE);
  const done = await serviceWorker.evaluate((key) => chrome.storage.local.get(key).then((items) => items[key] === true), STORAGE_KEYS.firstRunDone);
  const deadline = Date.now() + (done ? 0 : NEW_TAB_GRACE_MS);
  for (;;) {
    const open = context.pages().filter((page) => page.url() === url);
    if (open.length > 0) {
      for (const page of open) await page.close();
      return;
    }
    if (Date.now() >= deadline) return;
    await sleep(50);
  }
}

/**
 * Waits until the extension's only dynamic rule sends go links to `directory`.
 *
 * @param {Worker} serviceWorker
 * @param {string} directory an origin, such as "http://localhost:4655"
 * @param {number} [timeout=15000] milliseconds
 * @returns {Promise<void>}
 */
export async function waitForRedirectRule(serviceWorker, directory, timeout = 15_000) {
  const wanted = buildRedirectRule(directory);
  const deadline = Date.now() + timeout;
  for (;;) {
    const rules = await dynamicRules(serviceWorker);
    if (rules.length === 1 && sameRedirectRule(rules[0], wanted)) return;
    if (Date.now() > deadline) {
      throw new Error(`the extension did not install its redirect rule for ${directory} within ${timeout} ms; rules: ${JSON.stringify(rules)}`);
    }
    await sleep(50);
  }
}

/**
 * The URL of one of the extension's own pages.
 *
 * @param {string} extensionId
 * @param {string} page relative to the extension's root, for example "options/options.html"
 * @returns {string}
 */
export function extensionUrl(extensionId, page) {
  return `chrome-extension://${extensionId}/${page}`;
}

/**
 * Makes Chrome answer "Allow" the next time the extension asks for host
 * access to `origin`, without showing its prompt.
 *
 * The prompt itself cannot be answered in headless Chromium: a
 * chrome.permissions.request that would show it stays pending forever. What
 * stands in for the user's click is the call Chrome's own extensions page
 * makes when the user adds a site under "Site access":
 * chrome.developerPrivate.addHostPermission, run on chrome://extensions, the
 * one page allowed to make it. For a host the extension lists as optional,
 * that records the user's approval but does not activate it; the
 * extension's own chrome.permissions.request for that origin then resolves
 * true at once, with no prompt, and only from then on does
 * chrome.permissions.contains report it (seen in Chromium of Playwright
 * 1.64). So the extension's request code still runs, as it would after a
 * real "Allow". Whether the real prompt appears, and what Deny does, is on
 * the manual list (docs/manual-tests.md).
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @param {string} origin for example "http://localhost:46551"
 * @returns {Promise<void>}
 */
export async function approveHostAccess(context, extensionId, origin) {
  const page = await context.newPage();
  try {
    await page.goto("chrome://extensions/");
    await page.evaluate(
      ([id, pattern]) => {
        // Not in @types/chrome: the API exists only for Chrome's own pages.
        const developerPrivate = /** @type {{ addHostPermission(id: string, host: string): Promise<void> }} */ (
          /** @type {any} */ (globalThis).chrome.developerPrivate
        );
        return developerPrivate.addHostPermission(id, pattern);
      },
      [extensionId, `${origin}/*`],
    );
  } finally {
    await page.close();
  }
}

/**
 * Gives the extension host access to one origin: approveHostAccess, then the
 * request the settings page makes (requestAccess in extension/lib/settings.js
 * calls exactly this), from one of the extension's pages.
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @param {string} origin
 * @returns {Promise<void>}
 * @throws {Error} when Chrome did not grant it
 */
export async function grantHostAccess(context, extensionId, origin) {
  await approveHostAccess(context, extensionId, origin);
  const page = await context.newPage();
  try {
    await page.goto(extensionUrl(extensionId, "options/options.html"));
    const granted = await page.evaluate((pattern) => chrome.permissions.request({ origins: [pattern] }), `${origin}/*`);
    if (!granted) throw new Error(`Chrome did not grant the extension access to ${origin}`);
  } finally {
    await page.close();
  }
}

/**
 * Sends the settings page's own request to the service worker: make `url`
 * the Directory. It is the message the page's Save button sends, from the
 * page itself, so the worker's checks of its sender apply.
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @param {string} url
 * @returns {Promise<SetDirectoryReply>}
 */
export async function setDirectoryThroughWorker(context, extensionId, url) {
  const page = await context.newPage();
  try {
    await page.goto(extensionUrl(extensionId, "options/options.html"));
    return await page.evaluate(([type, value]) => chrome.runtime.sendMessage({ type, url: value }), [SET_DIRECTORY, url]);
  } finally {
    await page.close();
  }
}

/**
 * Points the extension at a Directory the way the settings page's Save does:
 * access to its origin is asked for and granted (grantHostAccess, with the
 * prompt approved in advance), then the worker is asked to save it. The
 * redirect rule follows. settings.spec.mjs drives the page itself.
 *
 * @param {BrowserContext} context
 * @param {string} extensionId
 * @param {string} origin
 * @returns {Promise<SetDirectoryReply>}
 */
export async function pointExtensionAt(context, extensionId, origin) {
  if (origin !== DEFAULT_DIRECTORY_URL) await grantHostAccess(context, extensionId, origin);
  return setDirectoryThroughWorker(context, extensionId, origin);
}

/**
 * Launches the same Chromium with no extension, for control runs.
 *
 * @param {object} [options]
 * @param {string} [options.userDataDir] defaults to a fresh temp profile
 * @returns {Promise<{ context: BrowserContext, close: () => Promise<void> }>}
 */
export async function launchWithoutExtension({ userDataDir } = {}) {
  const context = await launch({ userDataDir, args: [] });
  return { context, close: () => context.close() };
}
