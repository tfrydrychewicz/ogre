// Unit tests for the service worker, against a stand-in for chrome.*.
//
// Chromium started with --load-extension never fires onStartup (L0 spike,
// check 14), so the browser tests cannot reach that listener; these can.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_DIRECTORY_URL, buildRedirectRule } from "./lib/config.js";

/** @typedef {chrome.declarativeNetRequest.Rule} Rule */
/** @typedef {chrome.declarativeNetRequest.UpdateRuleOptions} UpdateRuleOptions */

let imports = 0;

/**
 * Evaluates background.js afresh against a stand-in chrome whose dynamic
 * rules start as `installed`. The stand-in applies updates the way Chrome
 * does: removals first, and adding an id that is still there is an error.
 *
 * @param {Rule[]} installed
 * @param {{ failRead?: boolean }} [options]
 */
async function loadWorker(installed, options = {}) {
  /** @type {{ onInstalled: ((details: { reason: string }) => void)[], onStartup: (() => void)[] }} */
  const listeners = { onInstalled: [], onStartup: [] };
  /** @type {UpdateRuleOptions[]} */
  const updates = [];
  let rules = structuredClone(installed);
  let reads = 0;
  /** @type {any} */ (globalThis).chrome = {
    runtime: {
      onInstalled: { addListener: (/** @type {(details: { reason: string }) => void} */ fn) => listeners.onInstalled.push(fn) },
      onStartup: { addListener: (/** @type {() => void} */ fn) => listeners.onStartup.push(fn) },
    },
    declarativeNetRequest: {
      getDynamicRules: async () => {
        reads++;
        if (options.failRead) throw new Error("rules unavailable");
        await tick(); // a real call answers later, not at once
        return structuredClone(rules);
      },
      updateDynamicRules: async (/** @type {UpdateRuleOptions} */ change) => {
        await tick();
        updates.push(structuredClone(change));
        const removed = new Set(change.removeRuleIds ?? []);
        const kept = rules.filter((rule) => !removed.has(rule.id));
        for (const rule of change.addRules ?? []) {
          if (kept.some((existing) => existing.id === rule.id)) throw new Error(`Rule with id ${rule.id} does not have a unique ID.`);
          kept.push(structuredClone(rule));
        }
        rules = kept;
      },
    },
  };
  await import(`./background.js?load=${++imports}`);
  return { listeners, updates, rules: () => rules, reads: () => reads };
}

const tick = () => new Promise((resolve) => setImmediate(resolve));

/** Lets every queued check finish. */
async function settle() {
  for (let i = 0; i < 20; i++) await tick();
}

/**
 * Runs `body` with console.info and console.error captured.
 *
 * @param {() => Promise<void>} body
 * @returns {Promise<{ info: unknown[][], error: unknown[][] }>}
 */
async function quietly(body) {
  const original = { info: console.info, error: console.error };
  /** @type {{ info: unknown[][], error: unknown[][] }} */
  const logged = { info: [], error: [] };
  console.info = (...args) => logged.info.push(args);
  console.error = (...args) => logged.error.push(args);
  try {
    await body();
  } finally {
    console.info = original.info;
    console.error = original.error;
  }
  return logged;
}

const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
const install = { removeRuleIds: [1], addRules: [wanted] };

test("both listeners are registered by the time the worker script has run", async () => {
  await quietly(async () => {
    // Read before anything asynchronous has had a chance to run: Chrome
    // delivers the event that woke the worker only to listeners registered
    // synchronously.
    const { listeners } = await loadWorker([wanted]);
    assert.equal(listeners.onInstalled.length, 1);
    assert.equal(listeners.onStartup.length, 1);
    await settle();
  });
});

test("starting the worker installs the rule when none is there", async () => {
  await quietly(async () => {
    const { updates, rules } = await loadWorker([]);
    await settle();
    assert.deepEqual(updates, [install]);
    assert.deepEqual(rules(), [wanted]);
  });
});

test("onInstalled installs the rule, once, although the worker start asks too", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, rules } = await loadWorker([]);
    listeners.onInstalled[0]({ reason: "install" });
    await settle();
    // Two checks were asked for in the same moment. They ran one after the
    // other, so the second found the rule in place and wrote nothing.
    assert.deepEqual(updates, [install]);
    assert.deepEqual(rules(), [wanted]);
  });
  assert.equal(logged.error.length, 0);
  assert.equal(logged.info.length, 1);
});

test("onStartup repairs a missing, foreign or extra rule", async () => {
  /** @type {Rule[][]} */
  const broken = [
    [],
    [buildRedirectRule("http://localhost:4656")],
    [wanted, { ...buildRedirectRule("http://localhost:4656"), id: 7 }],
  ];
  for (const installed of broken) {
    await quietly(async () => {
      const worker = await loadWorker([wanted]);
      await settle(); // the worker start found nothing to do
      assert.deepEqual(worker.updates, []);

      // The rule goes wrong while the worker is alive; the next browser
      // start must put it right.
      const ids = [...new Set([1, ...installed.map((rule) => rule.id)])];
      await chrome.declarativeNetRequest.updateDynamicRules({ removeRuleIds: [1], addRules: installed });
      worker.updates.length = 0;
      worker.listeners.onStartup[0]();
      await settle();
      assert.deepEqual(worker.updates, [{ removeRuleIds: ids, addRules: [wanted] }]);
      assert.deepEqual(worker.rules(), [wanted]);
    });
  }
});

test("nothing is written when the rule is already installed", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, reads } = await loadWorker([structuredClone(wanted)]);
    listeners.onStartup[0]();
    listeners.onInstalled[0]({ reason: "update" });
    await settle();
    assert.deepEqual(updates, []);
    assert.equal(reads(), 3); // worker start, onStartup, onInstalled: a read each
  });
  assert.deepEqual(logged, { info: [], error: [] });
});

test("a failing chrome call is logged, not thrown, and the next event tries again", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, reads } = await loadWorker([], { failRead: true });
    await settle();
    listeners.onStartup[0]();
    await settle();
    assert.deepEqual(updates, []);
    assert.equal(reads(), 2);
  });
  assert.equal(logged.error.length, 2);
  assert.match(String(logged.error[0][0]), /could not install the redirect rule \(worker start\)/);
  assert.match(String(logged.error[1][0]), /could not install the redirect rule \(onStartup\)/);
});
