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

import { DEFAULT_DIRECTORY_URL, buildRedirectRule, sameRedirectRule } from "../../extension/lib/config.js";

/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */
/** @typedef {import("@playwright/test").Worker} Worker */

export const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
export const SERVER_BIN = path.join(REPO_ROOT, "bin", process.platform === "win32" ? "ogre.exe" : "ogre");
export const EXTENSION_DIR = path.join(REPO_ROOT, "extension");

/** The address the extension redirects to. The server under test must listen exactly here. */
export const DIRECTORY_URL = DEFAULT_DIRECTORY_URL;
const DIRECTORY_PORT = Number(new URL(DIRECTORY_URL).port);

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
 * @returns {Promise<string>} for example "an Ogre server (version 0.1.0-dev), pid 4242"
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
 * @property {string} url the Directory URL, http://localhost:4655
 * @property {number | undefined} pid
 * @property {string} dbPath
 * @property {() => string} output everything the server has printed so far (the last 64 KB), for failure messages
 * @property {(signal?: NodeJS.Signals) => Promise<string>} stop ends the process, waits for it, and resolves with how it ended: "exit code 0" after a graceful shutdown, "signal SIGKILL" when it was killed. SIGTERM by default, which asks for a graceful shutdown; pass "SIGKILL" to simulate a crash. A server that ignores SIGTERM for 5 seconds is killed, and the answer says so
 */

/**
 * Starts `ogre serve --db <dbPath>` and resolves once GET /-/healthz answers.
 *
 * The server listens on its default address, which is the one the extension
 * redirects to. The call refuses to start when that port is already taken:
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
        "Stop it and run the tests again: they need that address, and must not write to a real Directory.",
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

  const child = spawn(bin, ["serve", "--db", dbPath], { cwd: home, env, stdio: ["ignore", "pipe", "pipe"] });
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
 * @param {string} [directoryUrl] defaults to http://localhost:4655
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
 * extension's service worker runs and, by default, its redirect rule is
 * installed. Without that second wait a test could open a go link before the
 * worker has finished installing the rule.
 *
 * @param {object} [options]
 * @param {string} [options.extensionDir] defaults to extension/
 * @param {string} [options.userDataDir] defaults to a fresh temp profile; pass an earlier one to simulate a restart
 * @param {boolean} [options.waitForRule=true] wait for the default redirect rule to be the installed one
 * @param {number} [options.timeout=15000] milliseconds for the worker, and again for the rule
 * @returns {Promise<ExtensionBrowser>}
 */
export async function launchWithExtension({
  extensionDir = EXTENSION_DIR,
  userDataDir = tempDir("ogre-e2e-profile-"),
  waitForRule = true,
  timeout = 15_000,
} = {}) {
  const dir = path.resolve(extensionDir);
  if (!existsSync(path.join(dir, "manifest.json"))) throw new Error(`no manifest.json in ${dir}`);

  const context = await launch({
    userDataDir,
    args: [`--disable-extensions-except=${dir}`, `--load-extension=${dir}`],
  });
  try {
    /** @param {Worker} worker */
    const isOurs = (worker) => worker.url().startsWith("chrome-extension://");
    const serviceWorker =
      context.serviceWorkers().find(isOurs) ??
      (await context.waitForEvent("serviceworker", { predicate: isOurs, timeout }));

    if (waitForRule) {
      const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
      const deadline = Date.now() + timeout;
      for (;;) {
        const rules = await dynamicRules(serviceWorker);
        if (rules.length === 1 && sameRedirectRule(rules[0], wanted)) break;
        if (Date.now() > deadline) {
          throw new Error(`the extension did not install its redirect rule within ${timeout} ms; rules: ${JSON.stringify(rules)}`);
        }
        await sleep(50);
      }
    }
    return {
      context,
      extensionId: new URL(serviceWorker.url()).host,
      serviceWorker,
      userDataDir,
      close: () => context.close(),
    };
  } catch (error) {
    await context.close();
    throw error;
  }
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
