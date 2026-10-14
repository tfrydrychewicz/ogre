// Unit tests for the web UI's lib/api.js: what every call made while typing
// relies on (plan section 3.6). Calls are delayed by 200 ms, each new call
// aborts the one before, and an answer that arrives after a newer call's is
// never reported as current. Run with: npm run test:web

import assert from "node:assert/strict";
import { test } from "node:test";

import { debounced } from "../../internal/web/static/js/lib/api.js";

/**
 * A promise and the functions that settle it, so a test decides when an
 * answer arrives.
 *
 * @template T
 * @returns {{ promise: Promise<T>, resolve: (value: T) => void, reject: (reason: unknown) => void }}
 */
function deferred() {
  /** @type {(value: T) => void} */
  let resolve = () => {};
  /** @type {(reason: unknown) => void} */
  let reject = () => {};
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

/** Lets every promise callback that is ready run. */
async function settle() {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

test("a call waits 200 ms before it runs", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  /** @type {string[]} */
  const calls = [];
  const search = debounced(async (_signal, /** @type {string} */ q) => {
    calls.push(q);
    return q.toUpperCase();
  });
  const outcome = search("a");
  t.mock.timers.tick(199);
  assert.deepEqual(calls, [], "called before 200 ms");
  t.mock.timers.tick(1);
  assert.deepEqual(calls, ["a"]);
  assert.deepEqual(await outcome, { current: true, value: "A" });
});

test("calls within 200 ms of each other run once, with the last arguments", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  /** @type {string[]} */
  const calls = [];
  const search = debounced(async (_signal, /** @type {string} */ q) => {
    calls.push(q);
    return q;
  });
  const first = search("r");
  t.mock.timers.tick(150);
  const second = search("ro");
  t.mock.timers.tick(150);
  assert.deepEqual(calls, [], "the first call ran although a newer one came within 200 ms");
  t.mock.timers.tick(50);
  assert.deepEqual(calls, ["ro"]);
  assert.deepEqual(await first, { current: false });
  assert.deepEqual(await second, { current: true, value: "ro" });
});

test("a new call aborts the one that is running", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  /** @type {AbortSignal[]} */
  const signals = [];
  const search = debounced((signal, /** @type {string} */ q) => {
    signals.push(signal);
    return new Promise((resolve, reject) => {
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      if (q === "ro") resolve(q);
    });
  });
  const first = search("r");
  t.mock.timers.tick(200);
  assert.equal(signals.length, 1);
  assert.equal(signals[0].aborted, false);

  const second = search("ro");
  assert.equal(signals[0].aborted, true, "the running call was not aborted");
  assert.deepEqual(await first, { current: false });
  t.mock.timers.tick(200);
  assert.deepEqual(await second, { current: true, value: "ro" });
  assert.equal(signals[1].aborted, false);
});

test("an answer that arrives after a newer one is not current", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  /** @type {Record<string, ReturnType<typeof deferred<string>>>} */
  const answers = { r: deferred(), roadmap: deferred() };
  // fn ignores its signal: the server answers anyway, late.
  const search = debounced((_signal, /** @type {"r" | "roadmap"} */ q) => answers[q].promise);
  /** @type {unknown[]} */
  const outcomes = [];
  search("r").then((o) => outcomes.push(["r", o]));
  t.mock.timers.tick(200);
  search("roadmap").then((o) => outcomes.push(["roadmap", o]));
  t.mock.timers.tick(200);

  answers.roadmap.resolve("roadmap results");
  await settle();
  answers.r.resolve("r results");
  await settle();
  assert.deepEqual(outcomes, [
    ["roadmap", { current: true, value: "roadmap results" }],
    ["r", { current: false }],
  ]);
});

test("cancel ends a pending call as not current, and the call never runs", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let ran = 0;
  const search = debounced(async () => {
    ran++;
    return 1;
  });
  const pending = search();
  search.cancel();
  assert.deepEqual(await pending, { current: false });
  t.mock.timers.tick(1000);
  await settle();
  assert.equal(ran, 0);
});

test("an error of the newest call is reported; one of a replaced call is not", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const failures = { old: deferred(), now: deferred() };
  const search = debounced((_signal, /** @type {"old" | "now"} */ which) => failures[which].promise);
  const old = search("old");
  t.mock.timers.tick(200);
  const now = search("now");
  t.mock.timers.tick(200);
  failures.old.reject(new TypeError("network down"));
  failures.now.reject(new TypeError("network down"));
  assert.deepEqual(await old, { current: false });
  await assert.rejects(now, TypeError);
});
