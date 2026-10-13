// Shared helpers for the L0 spike checks: build and run the stub server, launch
// Playwright's bundled Chromium with or without the spike extension, and record
// one result per check in spike/results/automated.json.
//
// Never launches the user's installed Google Chrome: every launch uses
// channel "chromium", which is Playwright's own download.

import { chromium } from "@playwright/test";
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const SPIKE_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
export const STUB_BIN = path.join(SPIKE_DIR, "bin", "stub");
export const EXTENSION_DIR = path.join(SPIKE_DIR, "extension");
export const OMNIBOX_EXTENSION_DIR = path.join(SPIKE_DIR, "extension-omnibox");
// SPIKE_RESULTS_DIR exists so the helpers can be tried without touching real results.
export const RESULTS_DIR = process.env.SPIKE_RESULTS_DIR
  ? path.resolve(process.env.SPIKE_RESULTS_DIR)
  : path.join(SPIKE_DIR, "results");
export const RESULTS_FILE = path.join(RESULTS_DIR, "automated.json");

export const DIRECTORY_ADDR = "localhost:4655";
export const DECOY_ADDR = "localhost:4656";
export const DIRECTORY_URL = `http://${DIRECTORY_ADDR}`;
export const DECOY_URL = `http://${DECOY_ADDR}`;

// ---------------------------------------------------------------------------
// Cleanup on exit: no stub and no temporary profile may outlive this process.

const liveStubs = new Set(); // every child process: stubs, and a browser started by startChromiumWithUrl
const tempDirs = new Set();

function cleanupSync() {
  for (const child of liveStubs) {
    try {
      child.kill("SIGKILL");
    } catch {
      // Already gone.
    }
  }
  liveStubs.clear();
  if (!process.env.SPIKE_KEEP_PROFILES) {
    for (const dir of tempDirs) rmSync(dir, { recursive: true, force: true });
  }
  tempDirs.clear();
}

process.on("exit", cleanupSync);
for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  // Clean up, then let the signal take its default course.
  process.once(signal, () => {
    cleanupSync();
    process.kill(process.pid, signal);
  });
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// ---------------------------------------------------------------------------
// Stub server

/** Builds spike/bin/stub from spike/stub/main.go. Throws with the compiler output on failure. */
export function buildStub() {
  execFileSync("go", ["build", "-o", "bin/stub", "stub/main.go"], {
    cwd: SPIKE_DIR,
    stdio: ["ignore", "pipe", "pipe"],
    encoding: "utf8",
  });
  return STUB_BIN;
}

/** True if something accepts TCP connections on this loopback port (IPv4 or IPv6). */
export async function portInUse(port) {
  const accepts = (host) =>
    new Promise((resolve) => {
      const socket = net.connect({ host, port });
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
 * Starts the stub and resolves once GET /-/spike/log answers.
 *
 * @param {object} [options]
 * @param {"directory"|"decoy"} [options.role="directory"]
 * @param {string} [options.addr] host:port; defaults to localhost:4655 (directory) or localhost:4656 (decoy)
 * @param {string} [options.bin] stub binary; defaults to spike/bin/stub
 * @param {string} [options.resultsDir] where the stub writes manual.json; defaults to spike/results
 * @returns {Promise<{baseUrl: string, addr: string, role: string, pid: number, output(): string, stop(): Promise<void>}>}
 */
export async function startStub({ role = "directory", addr, bin = STUB_BIN, resultsDir = RESULTS_DIR } = {}) {
  addr ??= role === "decoy" ? DECOY_ADDR : DIRECTORY_ADDR;
  const baseUrl = `http://${addr}`;
  const port = Number(addr.slice(addr.lastIndexOf(":") + 1));

  if (!existsSync(bin)) throw new Error(`stub binary missing: ${bin} (call buildStub() first)`);
  // Otherwise the readiness probe below could be answered by someone else's server.
  if (await portInUse(port)) throw new Error(`port ${port} is already in use; refusing to start the stub`);

  mkdirSync(resultsDir, { recursive: true });
  const child = spawn(bin, ["-addr", addr, "-role", role, "-results-dir", resultsDir], {
    cwd: SPIKE_DIR,
    stdio: ["ignore", "pipe", "pipe"],
  });
  liveStubs.add(child);

  // The stub prints one line per logged request; keep the tail for diagnostics
  // and keep the pipes drained so the stub never blocks on a full pipe.
  let output = "";
  const keep = (chunk) => {
    output = (output + chunk).slice(-64 * 1024);
  };
  child.stdout.setEncoding("utf8").on("data", keep);
  child.stderr.setEncoding("utf8").on("data", keep);

  const exited = new Promise((resolve) => {
    child.once("exit", (code, signal) => {
      liveStubs.delete(child);
      resolve({ code, signal });
    });
    child.once("error", (error) => {
      liveStubs.delete(child);
      resolve({ error });
    });
  });

  let stopped = false;
  const stop = async () => {
    if (stopped) return exited.then(() => undefined);
    stopped = true;
    if (child.exitCode === null && child.signalCode === null) {
      child.kill("SIGTERM");
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 2000);
      await exited;
      clearTimeout(killTimer);
    }
    await exited;
  };

  const deadline = Date.now() + 10_000;
  for (;;) {
    const early = await Promise.race([exited, sleep(50).then(() => null)]);
    if (early) {
      const why = early.error ? early.error.message : `code ${early.code}, signal ${early.signal}`;
      throw new Error(`stub exited before becoming ready (${why})\n${output}`);
    }
    try {
      const response = await fetch(`${baseUrl}/-/spike/log`, { signal: AbortSignal.timeout(1000) });
      await response.arrayBuffer();
      if (response.ok) break;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      await stop();
      throw new Error(`stub did not answer ${baseUrl}/-/spike/log within 10s\n${output}`);
    }
  }

  return { baseUrl, addr, role, pid: child.pid, output: () => output, stop };
}

/** Returns the stub's request log: the "entries" array of GET /-/spike/log, oldest first. */
export async function stubLog(baseUrl = DIRECTORY_URL) {
  const response = await fetch(`${baseUrl}/-/spike/log`);
  if (!response.ok) throw new Error(`GET ${baseUrl}/-/spike/log -> ${response.status}`);
  const body = await response.json();
  return body.entries ?? [];
}

/** Empties the stub's request log. */
export async function clearStubLog(baseUrl = DIRECTORY_URL) {
  const response = await fetch(`${baseUrl}/-/spike/log/clear`, { method: "POST" });
  await response.arrayBuffer();
  if (response.status !== 204) throw new Error(`POST ${baseUrl}/-/spike/log/clear -> ${response.status}`);
}

// ---------------------------------------------------------------------------
// Browser

/** Creates an empty profile directory under the system temp dir. Removed when this process exits. */
export function freshProfileDir() {
  const dir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-profile-"));
  tempDirs.add(dir);
  return dir;
}

/**
 * The id Chromium gives an unpacked extension loaded from this directory: the
 * first 16 bytes of SHA-256 of the real absolute path, written with a-p for 0-f.
 * Useful when the service worker is not running and so cannot be asked.
 */
export function extensionIdForDir(extensionDir) {
  const digest = createHash("sha256").update(realpathSync(extensionDir)).digest("hex").slice(0, 32);
  return [...digest].map((hex) => String.fromCharCode(97 + parseInt(hex, 16))).join("");
}

let lastChromiumVersion = null;
let lastCommandLine = null;

/** Version of the running browser, for example "156.0.8078.4". Works on persistent contexts. */
export async function chromiumVersion(context) {
  let version = context.browser()?.version();
  if (!version) {
    const page = context.pages()[0] ?? (await context.newPage());
    const session = await context.newCDPSession(page);
    const info = await session.send("Browser.getVersion");
    await session.detach();
    version = info.product.replace(/^[^/]*\//, "");
  }
  lastChromiumVersion = version;
  return version;
}

/** The browser process's command line: one string, read from chrome://version. */
export async function browserCommandLine(context) {
  const page = await context.newPage();
  try {
    await page.goto("chrome://version");
    return (await page.locator("#command_line").textContent()).trim();
  } finally {
    await page.close();
  }
}

// Playwright starts Chromium with a list of features switched off for test
// stability. One of them, HttpsUpgrades, is on in Chrome and decides how
// http:// navigations are treated, which is the subject of this spike. So the
// harness takes Playwright's list, removes that one feature, and passes the
// rest itself. The list is read from a first throwaway launch because
// ignoreDefaultArgs needs Playwright's switch character for character.
const KEEP_ENABLED = ["HttpsUpgrades"];
let featureSwitches = null;

async function discoverFeatureSwitches() {
  if (featureSwitches) return featureSwitches;
  const context = await chromium.launchPersistentContext(freshProfileDir(), { channel: "chromium", headless: true });
  try {
    const defaults = (await browserCommandLine(context)).match(/--disable-features=\S+/g) ?? [];
    if (defaults.length !== 1) throw new Error(`expected one --disable-features switch from Playwright, found ${defaults.length}`);
    const features = defaults[0].slice("--disable-features=".length).split(",");
    const kept = features.filter((feature) => !KEEP_ENABLED.includes(feature));
    featureSwitches = { playwrightDefault: defaults[0], replacement: `--disable-features=${kept.join(",")}` };
    return featureSwitches;
  } finally {
    await context.close();
  }
}

/**
 * How every browser of this harness differs from a default Chrome start-up.
 * Written into the results file so a reader does not have to guess.
 */
export function launchNotes() {
  return {
    browser: "Playwright's bundled Chromium (channel \"chromium\", Google Chrome for Testing), headless, fresh temporary profile per launch",
    featuresLeftEnabled: KEEP_ENABLED,
    fieldTrials:
      "--disable-field-trial-config and --disable-background-networking are passed (Playwright defaults), so the browser applies neither " +
      "Chromium's field-trial testing configuration nor a variations seed from Google: features are at their compiled-in defaults. " +
      "Chrome stable also applies server-side field trials, which can switch features on that are off here.",
    commandLine: lastCommandLine,
  };
}

async function launch({ userDataDir, args, headless, ignoreDefaultArgs = [] }) {
  const switches = await discoverFeatureSwitches();
  const context = await chromium.launchPersistentContext(userDataDir ?? freshProfileDir(), {
    channel: "chromium",
    headless,
    args: [switches.replacement, ...args],
    ignoreDefaultArgs: [switches.playwrightDefault, ...ignoreDefaultArgs],
  });
  try {
    await chromiumVersion(context);
    const commandLine = await browserCommandLine(context);
    const disabled = (commandLine.match(/--disable-features=\S+/g) ?? []).join(",");
    if (KEEP_ENABLED.some((feature) => disabled.split(/[=,]/).includes(feature))) {
      throw new Error(`the browser still runs with ${KEEP_ENABLED.join(", ")} disabled: ${disabled}`);
    }
    // Temporary paths differ per launch; keep the text comparable between runs.
    lastCommandLine = commandLine.replace(/(godir-spike-[a-z-]+-)[A-Za-z0-9]+/g, "$1XXXXXX");
  } catch (error) {
    await context.close();
    throw error;
  }
  return context;
}

/**
 * Launches Playwright's Chromium with one unpacked extension and waits for its
 * Manifest V3 service worker.
 *
 * @param {object} [options]
 * @param {string} [options.extensionDir] defaults to spike/extension
 * @param {string} [options.userDataDir] defaults to freshProfileDir(); pass the same one again to simulate a restart
 * @param {string[]} [options.extraArgs] extra Chromium switches
 * @param {boolean} [options.headless=true]
 * @param {string[]} [options.ignoreDefaultArgs] Playwright default switches to drop (exact strings)
 * @param {number} [options.serviceWorkerTimeout=15000]
 * @returns {Promise<{context: import("@playwright/test").BrowserContext, extensionId: string, serviceWorker: import("@playwright/test").Worker}>}
 */
export async function launchWithExtension({
  extensionDir = EXTENSION_DIR,
  userDataDir,
  extraArgs = [],
  headless = true,
  ignoreDefaultArgs,
  serviceWorkerTimeout = 15_000,
} = {}) {
  const dir = path.resolve(extensionDir);
  if (!existsSync(path.join(dir, "manifest.json"))) throw new Error(`no manifest.json in ${dir}`);

  const context = await launch({
    userDataDir,
    headless,
    ignoreDefaultArgs,
    args: [`--disable-extensions-except=${dir}`, `--load-extension=${dir}`, ...extraArgs],
  });
  try {
    const isOurs = (worker) => worker.url().startsWith("chrome-extension://");
    const serviceWorker =
      context.serviceWorkers().find(isOurs) ??
      (await context.waitForEvent("serviceworker", { predicate: isOurs, timeout: serviceWorkerTimeout }));
    return { context, extensionId: new URL(serviceWorker.url()).host, serviceWorker };
  } catch (error) {
    await context.close();
    throw error;
  }
}

/**
 * Launches the same Chromium with no extension, for control runs.
 *
 * @param {object} [options]
 * @param {string} [options.userDataDir] defaults to freshProfileDir()
 * @param {string[]} [options.extraArgs]
 * @param {boolean} [options.headless=true]
 * @param {string[]} [options.ignoreDefaultArgs]
 * @returns {Promise<{context: import("@playwright/test").BrowserContext}>}
 */
export async function launchWithoutExtension({
  userDataDir,
  extraArgs = [],
  headless = true,
  ignoreDefaultArgs,
} = {}) {
  return { context: await launch({ userDataDir, headless, ignoreDefaultArgs, args: [...extraArgs] }) };
}

/**
 * Starts Playwright's bundled Chromium directly, headless, with a URL on its
 * command line: the browser's very first navigation, as when another app opens
 * a link while the browser is closed. Playwright's own launch refuses a URL
 * argument, which is the only reason this does not go through it.
 *
 * Nothing can be asked of this browser; the stub's log is the evidence. Always
 * call stop().
 *
 * @param {object} options
 * @param {string} options.userDataDir an existing profile, from an earlier launch
 * @param {string} [options.extensionDir] unpacked extension to load; none for the control
 * @param {string} options.url
 * @returns {{args: string[], stop(): Promise<void>}}
 */
export function startChromiumWithUrl({ userDataDir, extensionDir, url }) {
  const args = [
    `--user-data-dir=${userDataDir}`,
    "--headless",
    "--no-first-run",
    "--no-default-browser-check",
    "--use-mock-keychain",
    "--password-store=basic",
    "--disable-search-engine-choice-screen",
    "--disable-background-networking",
    "--disable-component-update",
    "--disable-sync",
    "--disable-breakpad",
    ...(extensionDir ? [`--disable-extensions-except=${extensionDir}`, `--load-extension=${extensionDir}`] : []),
    url,
  ];
  const child = spawn(chromium.executablePath(), args, { stdio: "ignore" });
  liveStubs.add(child);
  const exited = new Promise((resolve) => {
    const gone = () => {
      liveStubs.delete(child);
      resolve();
    };
    child.once("exit", gone);
    child.once("error", gone);
  });
  const stop = async () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill("SIGTERM"); // lets Chromium flush the profile
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 5000);
      await exited;
      clearTimeout(killTimer);
    }
    await exited;
  };
  return { args: args.map((arg) => arg.replace(/(godir-spike-[a-z-]+-)[A-Za-z0-9]+/g, "$1XXXXXX")), stop };
}

// ---------------------------------------------------------------------------
// Results

const MODES = ["automated", "proxy"];
const STATUSES = ["pass", "fail", "inconclusive"];

/**
 * Records one check in spike/results/automated.json, replacing any earlier
 * result with the same id. "proxy" means the check was automated through a
 * stand-in for the real user action (for example page.goto in place of typing
 * in the address bar).
 *
 * @param {object} result
 * @param {number|string} result.id checklist number of Appendix B
 * @param {string} result.title
 * @param {"automated"|"proxy"} result.mode
 * @param {"pass"|"fail"|"inconclusive"} result.status
 * @param {string} result.observed what happened, in plain words
 * @param {unknown} [result.evidence] raw data backing "observed" (log entries, URLs, errors)
 * @returns {object} the whole results document as written
 */
export function recordResult({ id, title, mode, status, observed, evidence = null }) {
  if (id === undefined || id === null || id === "") throw new Error("recordResult: id is required");
  if (!MODES.includes(mode)) throw new Error(`recordResult: mode must be one of ${MODES.join(", ")}`);
  if (!STATUSES.includes(status)) throw new Error(`recordResult: status must be one of ${STATUSES.join(", ")}`);

  let previous = {};
  if (existsSync(RESULTS_FILE)) previous = JSON.parse(readFileSync(RESULTS_FILE, "utf8"));

  const now = new Date().toISOString();
  const checks = (previous.checks ?? []).filter((check) => String(check.id) !== String(id));
  // ranAt and chromiumVersion belong to this check alone: the file is merged by
  // id, so its other entries may come from an earlier run or another build.
  checks.push({ id, title, mode, status, ranAt: now, chromiumVersion: lastChromiumVersion, observed, evidence });
  checks.sort((a, b) => String(a.id).localeCompare(String(b.id), "en", { numeric: true }));

  const document = {
    generatedAt: now,
    lastWrittenByCheck: id,
    note:
      "generatedAt is the time of the last write, by the check named above. Each check carries its own ranAt and chromiumVersion. " +
      "chromiumVersion and launch below describe only the launch that preceded the last write; " +
      "every launch of the harness uses the same switches apart from the ones a check adds (named in its evidence).",
    chromiumVersion: lastChromiumVersion ?? previous.chromiumVersion ?? null,
    launch: launchNotes(),
    checks,
  };
  mkdirSync(RESULTS_DIR, { recursive: true });
  const temporary = `${RESULTS_FILE}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(document, null, 2)}\n`);
  renameSync(temporary, RESULTS_FILE);
  return document;
}
