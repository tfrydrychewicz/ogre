// Helpers for the cross-browser run of the web UI (plan L4.8, NFR-20):
// a Directory on a port of its own, and a watch for policy violations and
// script errors that works in Chromium, Firefox and WebKit alike.
//
// Why not lib/harness.mjs and lib/ui.mjs as they are: those serve the
// extension's tests, so their Directory listens at the address the
// extension redirects to (localhost:4655) and their watch looks at pages of
// that origin only. Firefox and WebKit cannot load the extension; their run
// drives go links through the Directory's own address, which is where the
// extension sends them. So the Directory here takes any free port, and a
// cross-browser run can never collide with a Chromium run, or with the
// developer's own Directory on 4655.
//
// The same three rules as the harness hold: the database and home
// directory are under the system temp directory, only Playwright's bundled
// browsers are launched (by the projects of playwright.config.mjs), and the
// server is killed when this process exits, whatever the reason.

import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import net from "node:net";
import path from "node:path";

import { SERVER_BIN, tempDir } from "./harness.mjs";

/** @typedef {import("@playwright/test").BrowserContext} BrowserContext */
/** @typedef {import("@playwright/test").Page} Page */

/** @type {Set<import("node:child_process").ChildProcess>} */
const live = new Set();
process.on("exit", () => {
  for (const child of live) {
    try {
      child.kill("SIGKILL");
    } catch {
      // Already gone.
    }
  }
});

/**
 * @param {number} ms
 * @returns {Promise<void>}
 */
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * Whether a port can be bound on one loopback address.
 *
 * @param {string} host
 * @param {number} port 0 asks the system for any free port
 * @returns {Promise<number | null>} the port bound, or null if it could not be
 */
function tryBind(host, port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once("error", () => resolve(null));
    server.listen({ host, port, exclusive: true }, () => {
      const bound = /** @type {import("node:net").AddressInfo} */ (server.address()).port;
      server.close(() => resolve(bound));
    });
  });
}

/**
 * A port free on both loopback addresses, since "localhost" makes the
 * Directory listen on both. Never 4655, which belongs to the extension's
 * tests and to the developer's own Directory.
 *
 * @returns {Promise<number>}
 */
export async function freeLoopbackPort() {
  for (let attempt = 0; attempt < 20; attempt++) {
    const port = await tryBind("127.0.0.1", 0);
    if (port === null || port === 4655) continue;
    // A machine without IPv6 loopback has nothing to collide with there.
    const v6 = await tryBind("::1", port);
    if (v6 !== null || !(await tryBind("::1", 0))) return port;
  }
  throw new Error("found no port free on both 127.0.0.1 and ::1 in 20 attempts");
}

/**
 * @typedef {object} PortDirectory
 * @property {string} url the Directory's origin, http://localhost:<port>
 * @property {() => string} output what the server printed so far (the last 64 KB)
 * @property {() => Promise<string>} stop asks for a graceful shutdown and resolves with how it ended ("exit code 0" when graceful)
 */

/**
 * Starts `ogre serve --addr localhost:<free port> --db <temp file>` and
 * resolves once GET /-/healthz answers. The binary must have been built
 * (buildServer() of the harness). A port taken between choosing it and the
 * server binding it ends the server at once; that is retried with another
 * port, twice at most.
 *
 * @param {object} [options]
 * @param {number} [options.readyTimeout=15000] milliseconds to wait for /-/healthz
 * @returns {Promise<PortDirectory>}
 */
export async function startDirectoryOnFreePort({ readyTimeout = 15_000 } = {}) {
  if (!existsSync(SERVER_BIN)) throw new Error(`server binary missing: ${SERVER_BIN} (call buildServer() first)`);
  let lastFailure = "";
  for (let attempt = 1; attempt <= 3; attempt++) {
    const port = await freeLoopbackPort();
    const started = await startOnce(port, readyTimeout);
    if (typeof started !== "string") return started;
    lastFailure = started;
  }
  throw new Error(`ogre did not start on a free port in 3 attempts. The last time:\n${lastFailure}`);
}

/**
 * @param {number} port
 * @param {number} readyTimeout
 * @returns {Promise<PortDirectory | string>} the server, or why it stopped before it was ready
 */
async function startOnce(port, readyTimeout) {
  // As in the harness: the flags decide everything, no OGRE_* setting of
  // the developer's shell reaches the server, and its home is a temp
  // directory, so not even a server bug could touch the real data directory.
  const home = tempDir("ogre-e2e-home-");
  const dbPath = path.join(tempDir("ogre-e2e-data-"), "ogre.db");
  /** @type {NodeJS.ProcessEnv} */
  const env = { ...process.env, HOME: home, XDG_DATA_HOME: home, LOCALAPPDATA: home, USERPROFILE: home };
  for (const name of Object.keys(env)) {
    if (name.startsWith("OGRE_")) delete env[name];
  }
  const child = spawn(SERVER_BIN, ["serve", "--addr", `localhost:${port}`, "--db", dbPath], {
    cwd: home,
    env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  live.add(child);
  let output = "";
  /** @param {string} chunk */
  const keep = (chunk) => {
    output = (output + chunk).slice(-64 * 1024);
  };
  child.stdout.setEncoding("utf8").on("data", keep);
  child.stderr.setEncoding("utf8").on("data", keep);
  /** @type {Promise<string>} */
  const exited = new Promise((resolve) => {
    child.once("exit", (code, signal) => {
      live.delete(child);
      resolve(signal ? `signal ${signal}` : `exit code ${code}`);
    });
    child.once("error", (error) => {
      live.delete(child);
      resolve(`could not start: ${error.message}`);
    });
  });
  const stop = async () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill("SIGTERM");
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 5000);
      await exited;
      clearTimeout(killTimer);
    }
    return exited;
  };

  const url = `http://localhost:${port}`;
  const deadline = Date.now() + readyTimeout;
  for (;;) {
    const early = await Promise.race([exited, sleep(50).then(() => null)]);
    if (early !== null) return `ogre stopped before it was ready (${early}). Its output:\n${output || "(none)"}`;
    try {
      const response = await fetch(`${url}/-/healthz`, { signal: AbortSignal.timeout(1000) });
      await response.arrayBuffer();
      if (response.ok) break;
    } catch {
      // Not listening yet.
    }
    if (Date.now() > deadline) {
      child.kill("SIGKILL");
      await exited;
      throw new Error(`ogre did not answer ${url}/-/healthz within ${readyTimeout} ms. Its output:\n${output || "(none)"}`);
    }
  }
  return { url, output: () => output, stop };
}

// ---------------------------------------------------------------------------
// The policy watch

/** Prefix of the console message the init script writes for each violation. */
const VIOLATION = "CSP violation:";
/** Prefix for a report that has the exact shape of WebKit's select quirk. */
const SELECT_QUIRK = "CSP report of WebKit's own <select> style:";
/** WebKit's console line for any refused inline style, ours or its own. */
const WEBKIT_REFUSED_STYLE = /^Refused to apply a stylesheet because its hash, its nonce, or 'unsafe-inline' does not appear in the style-src directive of the Content Security Policy\.$/;

/**
 * Collects, for every page of the context that belongs to `origin`, what
 * must never happen on the Directory's pages: a content security policy
 * violation, an uncaught script error, and any other console error. Like
 * watchPolicy() of lib/ui.mjs, but for any origin and any of the three
 * engines.
 *
 * One report is let through, in WebKit only, because WebKit raises it on
 * its own (e2e/webkit-select-csp.spec.mjs is the minimal repro, and fails
 * the day WebKit stops doing it). Every <select> WebKit creates, including
 * one written in static HTML with no script and no stylesheet on the page,
 * raises a style-src-attr violation: the engine puts an inline style on an
 * element of the select's user-agent shadow tree, and checks it against the
 * page's policy. The page's own code has no part in it, the select renders
 * the same with and without the policy, and Chromium and Firefox raise
 * nothing. A report is taken for that quirk only when all of these hold:
 *
 *   - the engine is WebKit;
 *   - the directive is style-src-attr and the blocked resource "inline";
 *   - the event's target is a <select> (the shadow tree's host, as the
 *     event is retargeted to it) that carries no style attribute of its own.
 *
 * A style attribute the page sets on a select keeps that attribute in the
 * DOM, so it is still reported; so is any inline style on any other element.
 * WebKit also writes one console error per refused inline style, which does
 * not say which element it was about; each such line is matched against
 * the quirk reports of its page, and only lines beyond that count are left
 * as they are: reported.
 *
 * A console error the browser writes for a document that answered with an
 * error status ("Failed to load resource: ... 404") is expected on a miss
 * page; allowStatus(url) declares such a document.
 *
 * @param {BrowserContext} context
 * @param {object} options
 * @param {string} options.origin the Directory's origin; pages elsewhere (the fixture destination) are not watched
 * @param {string} options.browserName "chromium", "firefox" or "webkit"
 * @returns {Promise<{ problems: string[], quirks: () => number, allowStatus: (url: string) => void, problemsNow: () => string[] }>}
 */
export async function watchPages(context, { origin, browserName }) {
  /** @type {string[]} */
  const problems = [];
  /** @type {Set<string>} */
  const allowed = new Set();
  /** Per page: WebKit's refused-style console lines, and the quirk reports seen. */
  /** @type {Map<Page | null, { refused: string[], quirks: number }>} */
  const perPage = new Map();
  /** @param {Page | null} page */
  const tally = (page) => {
    let entry = perPage.get(page);
    if (!entry) {
      entry = { refused: [], quirks: 0 };
      perPage.set(page, entry);
    }
    return entry;
  };

  await context.addInitScript(
    ({ violation, quirk, quirkApplies }) => {
      document.addEventListener(
        "securitypolicyviolation",
        (event) => {
          const target = event.target;
          const where = `${event.sourceFile || "(document)"}:${event.lineNumber}`;
          if (
            quirkApplies &&
            event.violatedDirective === "style-src-attr" &&
            event.blockedURI === "inline" &&
            target instanceof HTMLSelectElement &&
            !target.hasAttribute("style")
          ) {
            console.info(`${quirk} at ${where}`);
            return;
          }
          const element = target instanceof Element ? ` on <${target.localName}>` : "";
          console.error(`${violation} ${event.violatedDirective} blocked ${event.blockedURI || "(inline)"}${element} at ${where}`);
        },
        true,
      );
    },
    { violation: VIOLATION, quirk: SELECT_QUIRK, quirkApplies: browserName === "webkit" },
  );

  const ours = (/** @type {string} */ url) => url === origin || url.startsWith(`${origin}/`);

  context.on("console", (message) => {
    const text = message.text();
    const where = message.location().url;
    const pageURL = message.page()?.url() ?? where;
    if (!ours(pageURL) && !ours(where)) return;
    if (text.startsWith(SELECT_QUIRK)) {
      tally(message.page()).quirks++;
      return;
    }
    if (message.type() !== "error" && message.type() !== "warning") return;
    if (message.type() === "warning" && !/Content[- ]Security[- ]Policy|Refused to/i.test(text)) return;
    if (allowed.has(where) && /^Failed to load resource: the server responded with a status of 4\d\d\b/.test(text)) return;
    if (browserName === "webkit" && message.type() === "error" && WEBKIT_REFUSED_STYLE.test(text)) {
      tally(message.page()).refused.push(`error on ${pageURL}: ${text}`);
      return;
    }
    problems.push(`${message.type()} on ${pageURL}: ${text}`);
  });
  context.on("weberror", (error) => {
    const url = error.page()?.url() ?? "";
    if (!ours(url)) return;
    problems.push(`uncaught on ${url || "?"}: ${error.error().message}`);
  });

  /** Everything reported so far, with refused-style lines beyond each page's quirk count. */
  const problemsNow = () => {
    const unexplained = [...perPage.values()].flatMap((entry) => entry.refused.slice(entry.quirks));
    return [...problems, ...unexplained];
  };
  return {
    problems,
    quirks: () => [...perPage.values()].reduce((sum, entry) => sum + entry.quirks, 0),
    allowStatus: (url) => allowed.add(url),
    problemsNow,
  };
}
