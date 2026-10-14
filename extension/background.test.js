// @ts-check
// Unit tests for the service worker, against a stand-in for chrome.*.
//
// Chromium started with --load-extension never fires onStartup (L0 spike,
// check 14), and headless Chromium never answers Chrome's permission prompt,
// so the browser tests reach neither; these can.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_DIRECTORY_URL, FIRST_RUN_PAGE, REQUIRED_HOST_PATTERNS, SESSION_KEYS, STORAGE_KEYS, buildRedirectRule } from "./lib/config.js";
import { SET_DIRECTORY } from "./lib/settings.js";

/** @typedef {chrome.declarativeNetRequest.Rule} Rule */
/** @typedef {chrome.declarativeNetRequest.UpdateRuleOptions} UpdateRuleOptions */
/** @typedef {import("./lib/settings.js").SetDirectoryReply} SetDirectoryReply */

const EXTENSION_ID = "ogrestandinextensionidabcdefghij";
const EXTENSION_ORIGIN = `chrome-extension://${EXTENSION_ID}/`;
const OPTIONS_SENDER = { id: EXTENSION_ID, url: `${EXTENSION_ORIGIN}options/options.html` };

let imports = 0;

/**
 * @typedef {object} WorldOptions
 * @property {Rule[]} [rules] the dynamic rules installed at the start
 * @property {Record<string, unknown>} [stored] chrome.storage.local at the start
 * @property {string[]} [granted] optional host access granted at the start, as patterns
 * @property {string[]} [openPages] extension pages open in tabs, as URLs
 * @property {{ readRules?: boolean, updateRules?: boolean, storageSet?: boolean, removeAccess?: boolean, createTab?: boolean }} [fail] calls that reject
 */

/**
 * Evaluates background.js afresh against a stand-in chrome. The stand-in
 * behaves like Chrome where the worker relies on it: rule updates apply
 * removals first and refuse a duplicate id; storage.set notifies
 * storage.onChanged afterwards; a required host permission cannot be
 * removed, and asking to remove one fails the whole call.
 *
 * @param {WorldOptions} [options]
 */
async function loadWorker({ rules: initialRules = [], stored: initialStored = {}, granted: initialGranted = [], openPages = [], fail = {} } = {}) {
  const listeners = {
    /** @type {((details: { reason: string }) => void)[]} */ onInstalled: [],
    /** @type {(() => void)[]} */ onStartup: [],
    /** @type {((message: unknown, sender: object, sendResponse: (reply: unknown) => void) => boolean | undefined)[]} */ onMessage: [],
    /** @type {((changes: Record<string, unknown>, area: string) => void)[]} */ onStorageChanged: [],
  };
  /** @type {UpdateRuleOptions[]} */
  const updates = [];
  /** @type {string[]} */
  const createdTabs = [];
  /** @type {string[][]} */
  const removals = [];
  let rules = structuredClone(initialRules);
  /** @type {Record<string, unknown>} */
  let stored = structuredClone(initialStored);
  let granted = [...initialGranted];
  /** @type {Record<string, unknown>} */
  let session = {};
  let reads = 0;
  const hostPermissions = [...REQUIRED_HOST_PATTERNS];

  /** @param {string | string[] | null | undefined} keys */
  const pick = (keys) => {
    if (keys === null || keys === undefined) return structuredClone(stored);
    const wanted = typeof keys === "string" ? [keys] : keys;
    return Object.fromEntries(wanted.filter((key) => key in stored).map((key) => [key, structuredClone(stored[key])]));
  };

  /** @type {any} */ (globalThis).chrome = {
    runtime: {
      id: EXTENSION_ID,
      getURL: (/** @type {string} */ path) => EXTENSION_ORIGIN + path,
      getManifest: () => ({ host_permissions: [...hostPermissions] }),
      getContexts: async (/** @type {{ documentUrls?: string[] }} */ filter) =>
        openPages.filter((url) => !filter.documentUrls || filter.documentUrls.includes(url)).map((documentUrl) => ({ contextType: "TAB", documentUrl })),
      onInstalled: { addListener: (/** @type {any} */ fn) => listeners.onInstalled.push(fn) },
      onStartup: { addListener: (/** @type {any} */ fn) => listeners.onStartup.push(fn) },
      onMessage: { addListener: (/** @type {any} */ fn) => listeners.onMessage.push(fn) },
    },
    storage: {
      local: {
        get: async (/** @type {string | string[] | null} */ keys) => {
          await tick();
          return pick(keys);
        },
        set: async (/** @type {Record<string, unknown>} */ items) => {
          await tick();
          if (fail.storageSet) throw new Error("storage is full");
          /** @type {Record<string, { oldValue?: unknown, newValue: unknown }>} */
          const changes = {};
          for (const [key, value] of Object.entries(items)) changes[key] = { oldValue: stored[key], newValue: value };
          stored = { ...stored, ...structuredClone(items) };
          setImmediate(() => listeners.onStorageChanged.forEach((fn) => fn(changes, "local")));
        },
      },
      session: {
        set: async (/** @type {Record<string, unknown>} */ items) => {
          await tick();
          session = { ...session, ...structuredClone(items) };
        },
      },
      onChanged: { addListener: (/** @type {any} */ fn) => listeners.onStorageChanged.push(fn) },
    },
    permissions: {
      contains: async (/** @type {{ origins?: string[] }} */ query) =>
        (query.origins ?? []).every((pattern) => granted.includes(pattern) || hostPermissions.includes(pattern)),
      getAll: async () => ({ origins: [...hostPermissions, ...granted], permissions: ["declarativeNetRequestWithHostAccess", "storage", "webRequest"] }),
      remove: async (/** @type {{ origins?: string[] }} */ query) => {
        await tick();
        const origins = query.origins ?? [];
        if (fail.removeAccess) throw new Error("permissions unavailable");
        if (origins.some((pattern) => hostPermissions.includes(pattern))) throw new Error("You cannot remove required permissions.");
        removals.push([...origins]);
        granted = granted.filter((pattern) => !origins.includes(pattern));
        return true;
      },
    },
    tabs: {
      create: async (/** @type {{ url: string }} */ properties) => {
        await tick();
        if (fail.createTab) throw new Error("no window");
        createdTabs.push(properties.url);
        return { id: createdTabs.length, url: properties.url };
      },
    },
    declarativeNetRequest: {
      getDynamicRules: async () => {
        reads++;
        if (fail.readRules) throw new Error("rules unavailable");
        await tick(); // a real call answers later, not at once
        return structuredClone(rules);
      },
      updateDynamicRules: async (/** @type {UpdateRuleOptions} */ change) => {
        await tick();
        if (fail.updateRules) throw new Error("Rule with id 1 specifies an incorrect value for the \"action.redirect.regexSubstitution\" key.");
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

  /**
   * Delivers a message the way Chrome does, and resolves with the listener's
   * synchronous return value and, when it promised one, its answer.
   *
   * @param {unknown} message
   * @param {object} [sender]
   * @returns {Promise<{ kept: boolean, reply: SetDirectoryReply | undefined }>}
   */
  const send = async (message, sender = OPTIONS_SENDER) => {
    /** @type {(reply: unknown) => void} */
    let answer = () => {};
    const replied = new Promise((resolve) => (answer = resolve));
    const kept = listeners.onMessage.map((fn) => fn(message, sender, answer)).some((value) => value === true);
    if (!kept) return { kept, reply: undefined };
    return { kept, reply: /** @type {SetDirectoryReply} */ (await replied) };
  };

  return {
    listeners,
    updates,
    createdTabs,
    removals,
    send,
    rules: () => rules,
    stored: () => stored,
    session: () => session,
    granted: () => granted,
    reads: () => reads,
    /** @param {Rule[]} next replaces the rules behind the worker's back */
    setRules: (next) => (rules = structuredClone(next)),
    /** @param {Record<string, unknown>} items writes storage as another page would, notifying the worker */
    writeStorage: (items) => /** @type {any} */ (globalThis).chrome.storage.local.set(items),
  };
}

const tick = () => new Promise((resolve) => setImmediate(resolve));

/** Lets every queued task finish. */
async function settle() {
  for (let i = 0; i < 30; i++) await tick();
}

/**
 * Runs `body` with console.info, console.warn and console.error captured.
 *
 * @param {() => Promise<void>} body
 * @returns {Promise<{ info: unknown[][], warn: unknown[][], error: unknown[][] }>}
 */
async function quietly(body) {
  const original = { info: console.info, warn: console.warn, error: console.error };
  /** @type {{ info: unknown[][], warn: unknown[][], error: unknown[][] }} */
  const logged = { info: [], warn: [], error: [] };
  console.info = (...args) => logged.info.push(args);
  console.warn = (...args) => logged.warn.push(args);
  console.error = (...args) => logged.error.push(args);
  try {
    await body();
  } finally {
    console.info = original.info;
    console.warn = original.warn;
    console.error = original.error;
  }
  return logged;
}

const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
const install = { removeRuleIds: [1], addRules: [wanted] };
const FIRST_RUN_URL = EXTENSION_ORIGIN + FIRST_RUN_PAGE;
const OTHER = "http://localhost:4656";
const THIRD = "https://go.example.com";

// ---------------------------------------------------------------------------
// The redirect rule

test("every listener is registered by the time the worker script has run", async () => {
  await quietly(async () => {
    // Read before anything asynchronous has had a chance to run: Chrome
    // delivers the event that woke the worker only to listeners registered
    // synchronously.
    const { listeners } = await loadWorker({ rules: [wanted] });
    assert.equal(listeners.onInstalled.length, 1);
    assert.equal(listeners.onStartup.length, 1);
    assert.equal(listeners.onMessage.length, 1);
    assert.equal(listeners.onStorageChanged.length, 1);
    await settle();
  });
});

test("starting the worker installs the default rule when none is there and nothing is stored", async () => {
  await quietly(async () => {
    const { updates, rules } = await loadWorker();
    await settle();
    assert.deepEqual(updates, [install]);
    assert.deepEqual(rules(), [wanted]);
  });
});

test("starting the worker installs the rule for the stored Directory", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], stored: { [STORAGE_KEYS.directoryUrl]: OTHER }, granted: [`${OTHER}/*`] });
    await settle();
    assert.deepEqual(worker.updates, [{ removeRuleIds: [1], addRules: [buildRedirectRule(OTHER)] }]);
    assert.deepEqual(worker.rules(), [buildRedirectRule(OTHER)]);
  });
});

test("a stored value that is not a valid Directory URL is ignored, said, and the default used", async () => {
  for (const bad of ["http://go", "javascript:alert(1)", "localhost:4655", 42, null, { url: OTHER }]) {
    const logged = await quietly(async () => {
      const worker = await loadWorker({ stored: { [STORAGE_KEYS.directoryUrl]: bad } });
      await settle();
      assert.deepEqual(worker.rules(), [wanted], JSON.stringify(bad));
    });
    assert.equal(logged.warn.length, 1, JSON.stringify(bad));
    assert.match(String(logged.warn[0][0]), /is not valid; using http:\/\/localhost:4655/);
  }
});

test("onInstalled installs the rule, once, although the worker start asks too", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, rules } = await loadWorker();
    listeners.onInstalled[0]({ reason: "update" });
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
  const broken = [[], [buildRedirectRule(OTHER)], [wanted, { ...buildRedirectRule(OTHER), id: 7 }]];
  for (const installed of broken) {
    await quietly(async () => {
      const worker = await loadWorker({ rules: [wanted] });
      await settle(); // the worker start found nothing to do
      assert.deepEqual(worker.updates, []);

      // The rule goes wrong while the worker is alive; the next browser
      // start must put it right.
      const ids = [...new Set([1, ...installed.map((rule) => rule.id)])];
      worker.setRules(installed);
      worker.listeners.onStartup[0]();
      await settle();
      assert.deepEqual(worker.updates, [{ removeRuleIds: ids, addRules: [wanted] }]);
      assert.deepEqual(worker.rules(), [wanted]);
    });
  }
});

test("nothing is written when the rule is already installed", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, reads } = await loadWorker({ rules: [structuredClone(wanted)] });
    listeners.onStartup[0]();
    listeners.onInstalled[0]({ reason: "update" });
    await settle();
    assert.deepEqual(updates, []);
    assert.equal(reads(), 3); // worker start, onStartup, onInstalled: a read each
  });
  assert.deepEqual(logged, { info: [], warn: [], error: [] });
});

test("a failing chrome call is logged, not thrown, and the next event tries again", async () => {
  const logged = await quietly(async () => {
    const { listeners, updates, reads } = await loadWorker({ fail: { readRules: true } });
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

test("a Directory URL written to storage by other means re-points the rule", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted] });
    await settle();
    await worker.writeStorage({ [STORAGE_KEYS.directoryUrl]: OTHER });
    await settle();
    assert.deepEqual(worker.rules(), [buildRedirectRule(OTHER)]);
    // Other keys leave the rule alone.
    worker.updates.length = 0;
    await worker.writeStorage({ [STORAGE_KEYS.firstRunDone]: true });
    await settle();
    assert.deepEqual(worker.updates, []);
  });
});

// ---------------------------------------------------------------------------
// The first-run page

test("access allowed for a test that was never saved is given up when the browser starts, and on install", async () => {
  const tested = "http://localhost:4791/*";
  for (const event of ["onStartup", "onInstalled"]) {
    await quietly(async () => {
      const worker = await loadWorker({ stored: { [STORAGE_KEYS.directoryUrl]: OTHER }, granted: [`${OTHER}/*`, tested] });
      if (event === "onStartup") worker.listeners.onStartup[0]();
      else worker.listeners.onInstalled[0]({ reason: "update" });
      await settle();
      assert.deepEqual(worker.granted(), [`${OTHER}/*`], event);
      assert.deepEqual(worker.removals, [[tested]], event);
    });
  }
  // With the default Directory, every optional grant is unused.
  await quietly(async () => {
    const worker = await loadWorker({ granted: [tested] });
    worker.listeners.onStartup[0]();
    await settle();
    assert.deepEqual(worker.granted(), []);
  });
  // Nothing to give up: no call at all.
  await quietly(async () => {
    const worker = await loadWorker({ stored: { [STORAGE_KEYS.directoryUrl]: OTHER }, granted: [`${OTHER}/*`] });
    worker.listeners.onStartup[0]();
    await settle();
    assert.deepEqual(worker.removals, []);
  });
  // The worker starting by itself (any event wakes it) gives nothing up:
  // the settings page may be between a test and a save.
  await quietly(async () => {
    const worker = await loadWorker({ granted: [tested] });
    await settle();
    assert.deepEqual(worker.granted(), [tested]);
  });
  // A failure is logged, and nothing else happens.
  const logged = await quietly(async () => {
    const worker = await loadWorker({ granted: [tested], fail: { removeAccess: true } });
    worker.listeners.onStartup[0]();
    await settle();
    assert.deepEqual(worker.granted(), [tested]);
  });
  assert.ok(logged.warn.some((args) => String(args[0]).includes("could not release unused access (onStartup)")));
});

test("installing opens the first-run page once, then marks the install handled", async () => {
  await quietly(async () => {
    const worker = await loadWorker();
    worker.listeners.onInstalled[0]({ reason: "install" });
    await settle();
    assert.deepEqual(worker.createdTabs, [FIRST_RUN_URL]);
    assert.equal(typeof worker.session()[SESSION_KEYS.installHandled], "number");
    assert.deepEqual(worker.stored(), {}, "the mark is not kept in local storage");
  });
});

test("the install is marked handled only once the rule and the page are", async () => {
  await quietly(async () => {
    const worker = await loadWorker();
    worker.listeners.onInstalled[0]({ reason: "install" });
    // The mark comes last: the moment it appears, the tab and the rule exist.
    for (let i = 0; i < 30 && worker.session()[SESSION_KEYS.installHandled] === undefined; i++) await tick();
    assert.notEqual(worker.session()[SESSION_KEYS.installHandled], undefined);
    assert.deepEqual(worker.createdTabs, [FIRST_RUN_URL]);
    assert.deepEqual(worker.rules(), [wanted]);
  });
  // Also when nothing opens, and when opening fails.
  for (const world of [{ stored: { [STORAGE_KEYS.firstRunDone]: true } }, { fail: { createTab: true } }]) {
    await quietly(async () => {
      const worker = await loadWorker(world);
      worker.listeners.onInstalled[0]({ reason: "install" });
      await settle();
      assert.equal(typeof worker.session()[SESSION_KEYS.installHandled], "number", JSON.stringify(world));
    });
  }
});

test("the first-run page does not open again once finished, beside an open copy, or on an update", async () => {
  /** @type {[string, WorldOptions, string][]} */
  const cases = [
    ["finished", { stored: { [STORAGE_KEYS.firstRunDone]: true } }, "install"],
    ["already open, as after a restored session", { openPages: [FIRST_RUN_URL] }, "install"],
    ["an update of the extension", {}, "update"],
    ["an update of Chrome", {}, "chrome_update"],
  ];
  for (const [name, world, reason] of cases) {
    await quietly(async () => {
      const worker = await loadWorker(world);
      worker.listeners.onInstalled[0]({ reason });
      await settle();
      assert.deepEqual(worker.createdTabs, [], name);
    });
  }
  // The settings page being open is no reason not to open the first-run page.
  await quietly(async () => {
    const worker = await loadWorker({ openPages: [`${EXTENSION_ORIGIN}options/options.html`] });
    worker.listeners.onInstalled[0]({ reason: "install" });
    await settle();
    assert.deepEqual(worker.createdTabs, [FIRST_RUN_URL]);
  });
});

test("a first-run page that cannot be opened is logged, and the rule is still installed", async () => {
  const logged = await quietly(async () => {
    const worker = await loadWorker({ fail: { createTab: true } });
    worker.listeners.onInstalled[0]({ reason: "install" });
    await settle();
    assert.deepEqual(worker.rules(), [wanted]);
  });
  assert.equal(logged.error.length, 1);
  assert.match(String(logged.error[0][0]), /could not open the first-run page/);
});

// ---------------------------------------------------------------------------
// Changing the Directory (SET_DIRECTORY, from the settings page)

test("changing the Directory replaces the rule in one call, stores the URL and gives up the old access", async () => {
  await quietly(async () => {
    const worker = await loadWorker({
      rules: [buildRedirectRule(OTHER)],
      stored: { [STORAGE_KEYS.directoryUrl]: OTHER },
      granted: [`${OTHER}/*`, `${THIRD}/*`],
    });
    await settle();
    assert.deepEqual(worker.updates, []);

    const { kept, reply } = await worker.send({ type: SET_DIRECTORY, url: " HTTPS://Go.Example.com:443/ " });
    assert.equal(kept, true, "the listener promised an answer");
    assert.deepEqual(reply, { ok: true, origin: THIRD, released: [`${OTHER}/*`] });
    // One update: the old rule removed and the new one added together.
    assert.deepEqual(worker.updates, [{ removeRuleIds: [1], addRules: [buildRedirectRule(THIRD)] }]);
    assert.deepEqual(worker.rules(), [buildRedirectRule(THIRD)]);
    assert.equal(worker.stored()[STORAGE_KEYS.directoryUrl], THIRD);
    assert.deepEqual(worker.granted(), [`${THIRD}/*`]);
    await settle();
    // The storage change that followed found the rule already right.
    assert.equal(worker.updates.length, 1);
  });
});

test("going back to the default Directory releases every optional origin and never the default one", async () => {
  await quietly(async () => {
    const worker = await loadWorker({
      rules: [buildRedirectRule(OTHER)],
      stored: { [STORAGE_KEYS.directoryUrl]: OTHER },
      granted: [`${OTHER}/*`, `${THIRD}/*`],
    });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: DEFAULT_DIRECTORY_URL });
    assert.deepEqual(reply, { ok: true, origin: DEFAULT_DIRECTORY_URL, released: [`${OTHER}/*`, `${THIRD}/*`] });
    assert.deepEqual(worker.removals, [[`${OTHER}/*`, `${THIRD}/*`]]);
    assert.deepEqual(worker.rules(), [wanted]);
    assert.deepEqual(worker.granted(), []);
  });
});

test("saving the Directory already in use changes nothing and releases nothing", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted] });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: DEFAULT_DIRECTORY_URL });
    assert.deepEqual(reply, { ok: true, origin: DEFAULT_DIRECTORY_URL, released: [] });
    assert.deepEqual(worker.updates, []);
    assert.deepEqual(worker.removals, []);
  });
});

test("an invalid Directory URL is refused by the worker too, and nothing changes", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted] });
    await settle();
    for (const url of ["http://go", "javascript:alert(1)", "http://localhost:4655/x", "localhost:4655", ""]) {
      const { reply } = await worker.send({ type: SET_DIRECTORY, url });
      assert.equal(reply?.ok, false, url);
      assert.ok(reply && !reply.ok && reply.message.length > 0, url);
    }
    assert.deepEqual(worker.updates, []);
    assert.equal(worker.stored()[STORAGE_KEYS.directoryUrl], undefined);
  });
});

test("a Directory the extension has no access to is refused, and nothing changes", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted] });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: OTHER });
    assert.deepEqual(reply, {
      ok: false,
      message: "Chrome has not given the extension access to http://localhost:4656, so nothing was changed. Save again and allow access when Chrome asks.",
    });
    assert.deepEqual(worker.updates, []);
    assert.deepEqual(worker.rules(), [wanted]);
  });
});

test("only this extension's own pages can change the Directory", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], granted: [`${OTHER}/*`] });
    await settle();
    /** @type {[string, unknown, object][]} */
    const cases = [
      ["another extension", { type: SET_DIRECTORY, url: OTHER }, { id: "someoneelse", url: "chrome-extension://someoneelse/x.html" }],
      ["a web page claiming the id", { type: SET_DIRECTORY, url: OTHER }, { id: EXTENSION_ID, url: "https://evil.example/" }],
      ["no sender URL", { type: SET_DIRECTORY, url: OTHER }, { id: EXTENSION_ID }],
      ["another message type", { type: "ogre:something", url: OTHER }, OPTIONS_SENDER],
      ["a URL that is not a string", { type: SET_DIRECTORY, url: 42 }, OPTIONS_SENDER],
      ["not an object", SET_DIRECTORY, OPTIONS_SENDER],
      ["null", null, OPTIONS_SENDER],
    ];
    for (const [name, message, sender] of cases) {
      const { kept } = await worker.send(message, sender);
      assert.equal(kept, false, name);
    }
    await settle();
    assert.deepEqual(worker.updates, []);
    assert.deepEqual(worker.rules(), [wanted]);
  });
});

test("a rule Chrome refuses leaves the stored URL and the access as they were", async () => {
  const logged = await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], granted: [`${OTHER}/*`], fail: { updateRules: true } });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: OTHER });
    assert.equal(reply?.ok, false);
    assert.match(reply && !reply.ok ? reply.message : "", /^Chrome refused the new redirect rule \(.+\)\. Nothing was changed\.$/);
    assert.equal(worker.stored()[STORAGE_KEYS.directoryUrl], undefined);
    assert.deepEqual(worker.granted(), [`${OTHER}/*`]);
  });
  assert.ok(logged.error.length >= 1);
});

test("a URL that cannot be stored puts the previous rule back", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], granted: [`${OTHER}/*`], fail: { storageSet: true } });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: OTHER });
    assert.deepEqual(reply, { ok: false, message: "The new address could not be stored (storage is full). Nothing was changed." });
    assert.deepEqual(worker.rules(), [wanted]);
    assert.deepEqual(worker.granted(), [`${OTHER}/*`]);
  });
});

test("access that cannot be given up is logged, and the change still counts", async () => {
  const logged = await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], granted: [`${OTHER}/*`, `${THIRD}/*`], fail: { removeAccess: true } });
    await settle();
    const { reply } = await worker.send({ type: SET_DIRECTORY, url: OTHER });
    assert.deepEqual(reply, { ok: true, origin: OTHER, released: [] });
    assert.deepEqual(worker.rules(), [buildRedirectRule(OTHER)]);
  });
  assert.equal(logged.warn.length, 1);
  assert.match(String(logged.warn[0][0]), /could not release access/);
});

test("two saves in quick succession apply in order, and the last one wins", async () => {
  await quietly(async () => {
    const worker = await loadWorker({ rules: [wanted], granted: [`${OTHER}/*`, `${THIRD}/*`] });
    const [first, second] = await Promise.all([
      worker.send({ type: SET_DIRECTORY, url: OTHER }),
      worker.send({ type: SET_DIRECTORY, url: THIRD }),
    ]);
    await settle();
    assert.deepEqual(first.reply, { ok: true, origin: OTHER, released: [`${THIRD}/*`] });
    // The second save found the third origin's access already released by
    // the first one, so it was refused, and the first one stands.
    assert.equal(second.reply?.ok, false);
    assert.deepEqual(worker.rules(), [buildRedirectRule(OTHER)]);
    assert.equal(worker.stored()[STORAGE_KEYS.directoryUrl], OTHER);
  });
});
