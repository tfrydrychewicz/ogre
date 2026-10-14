// @ts-check
// Unit tests for the stored Directory and the calls the pages make
// (lib/settings.js), against a small stand-in for chrome.*.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_DIRECTORY_URL, STORAGE_KEYS } from "./config.js";
import { SET_DIRECTORY, loadDirectory, requestAccess, setDirectory } from "./settings.js";

/**
 * @param {object} parts
 * @param {Record<string, unknown>} [parts.stored]
 * @param {(request: unknown) => Promise<unknown>} [parts.sendMessage]
 */
function standIn({ stored = {}, sendMessage = async () => undefined } = {}) {
  /** @type {unknown[]} */
  const permissionRequests = [];
  /** @type {unknown[]} */
  const messages = [];
  /** @type {any} */ (globalThis).chrome = {
    storage: { local: { get: async (/** @type {string} */ key) => (key in stored ? { [key]: stored[key] } : {}) } },
    permissions: {
      request: async (/** @type {unknown} */ request) => {
        permissionRequests.push(request);
        return true;
      },
    },
    runtime: {
      sendMessage: async (/** @type {unknown} */ message) => {
        messages.push(message);
        return sendMessage(message);
      },
    },
  };
  return { permissionRequests, messages };
}

test("loadDirectory gives the default until the user saves one", async () => {
  standIn();
  assert.deepEqual(await loadDirectory(), { origin: DEFAULT_DIRECTORY_URL, saved: false });
});

test("loadDirectory gives the saved Directory, normalized", async () => {
  standIn({ stored: { [STORAGE_KEYS.directoryUrl]: "https://go.example.com" } });
  assert.deepEqual(await loadDirectory(), { origin: "https://go.example.com", saved: true });
  standIn({ stored: { [STORAGE_KEYS.directoryUrl]: "HTTPS://GO.EXAMPLE.COM/" } });
  assert.deepEqual(await loadDirectory(), { origin: "https://go.example.com", saved: true });
});

test("loadDirectory ignores a stored value that is not a valid Directory URL, and says so", async () => {
  for (const invalid of ["http://go", "file:///etc/passwd", "", 7, null, ["http://localhost:4656"]]) {
    standIn({ stored: { [STORAGE_KEYS.directoryUrl]: invalid } });
    assert.deepEqual(await loadDirectory(), { origin: DEFAULT_DIRECTORY_URL, saved: false, invalid }, JSON.stringify(invalid));
  }
});

test("requestAccess asks Chrome only for a Directory the manifest does not cover, and only for its origin", async () => {
  const world = standIn();
  assert.equal(await requestAccess(DEFAULT_DIRECTORY_URL), true);
  assert.deepEqual(world.permissionRequests, [], "the default Directory needs no prompt");

  assert.equal(await requestAccess("https://go.example.com"), true);
  assert.equal(await requestAccess("http://[::1]:4655"), true);
  assert.deepEqual(world.permissionRequests, [{ origins: ["https://go.example.com/*"] }, { origins: ["http://[::1]:4655/*"] }]);
});

test("setDirectory sends one message to the worker and passes on its answer", async () => {
  const world = standIn({ sendMessage: async () => ({ ok: true, origin: "http://localhost:4656", released: [] }) });
  assert.deepEqual(await setDirectory("http://localhost:4656"), { ok: true, origin: "http://localhost:4656", released: [] });
  assert.deepEqual(world.messages, [{ type: SET_DIRECTORY, url: "http://localhost:4656" }]);

  standIn({ sendMessage: async () => ({ ok: false, message: "No." }) });
  assert.deepEqual(await setDirectory("http://localhost:4656"), { ok: false, message: "No." });
});

test("setDirectory turns a missing or malformed answer, or a failed send, into a message", async () => {
  /** @type {[string, () => Promise<unknown>, RegExp][]} */
  const cases = [
    ["no answer", async () => undefined, /gave no answer/],
    ["a malformed answer", async () => ({ ok: true }), /gave no answer/],
    ["a send that fails", async () => Promise.reject(new Error("Could not establish connection. Receiving end does not exist.")), /could not be reached \(Could not establish connection/],
  ];
  for (const [name, sendMessage, message] of cases) {
    standIn({ sendMessage });
    const reply = await setDirectory("http://localhost:4656");
    assert.equal(reply.ok, false, name);
    assert.match(reply.ok ? "" : reply.message, message, name);
  }
});
