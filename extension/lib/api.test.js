// @ts-check
// Unit tests for the connection test (lib/api.js), against a stand-in fetch.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import { API_VERSION, META_TIMEOUT_MS, PRODUCT, fetchMeta } from "./api.js";

const ORIGIN = "http://localhost:4655";

/**
 * A stand-in fetch that answers every request with `response` and records
 * what it was asked.
 *
 * @param {() => Response | Promise<Response>} answer
 */
function stubFetch(answer) {
  /** @type {{ url: string, init: RequestInit | undefined }[]} */
  const calls = [];
  /** @type {typeof fetch} */
  const fetchImpl = async (input, init) => {
    calls.push({ url: String(input), init });
    return answer();
  };
  return { fetch: fetchImpl, calls };
}

/**
 * @param {unknown} body
 * @param {ResponseInit} [init]
 */
const json = (body, init = {}) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" }, ...init });

test("the extension speaks the Directory's API version 1 and expects the product ogre", () => {
  assert.equal(PRODUCT, "ogre");
  assert.equal(API_VERSION, 1);
  assert.equal(META_TIMEOUT_MS, 5000);
});

test("an Ogre server's meta answer is accepted, with its version", async () => {
  const stub = stubFetch(() => json({ product: "ogre", version: "0.1.0", api: 1 }));
  assert.deepEqual(await fetchMeta(ORIGIN, { fetch: stub.fetch }), { ok: true, version: "0.1.0", api: 1 });

  assert.equal(stub.calls.length, 1);
  const [{ url, init }] = stub.calls;
  assert.equal(url, "http://localhost:4655/-/api/v1/meta");
  assert.equal(init?.method, "GET");
  // No cookies, no cache, and a redirect is reported rather than followed.
  assert.equal(init?.credentials, "omit");
  assert.equal(init?.cache, "no-store");
  assert.equal(init?.redirect, "manual");
  assert.ok(init?.signal instanceof AbortSignal, "a timeout signal is passed");
});

test("a server that reports no version is still accepted, with an empty version", async () => {
  const stub = stubFetch(() => json({ product: "ogre", api: 1 }));
  assert.deepEqual(await fetchMeta(ORIGIN, { fetch: stub.fetch }), { ok: true, version: "", api: 1 });
  // A version that is not text is not shown either.
  for (const version of [1, null, { major: 0 }, ["0.1.0"]]) {
    const odd = stubFetch(() => json({ product: "ogre", api: 1, version }));
    assert.deepEqual(await fetchMeta(ORIGIN, { fetch: odd.fetch }), { ok: true, version: "", api: 1 }, JSON.stringify(version));
  }
});

test("without a timeout of its own, a check waits META_TIMEOUT_MS", async () => {
  const original = AbortSignal.timeout;
  /** @type {number[]} */
  const asked = [];
  AbortSignal.timeout = (ms) => {
    asked.push(ms);
    return original.call(AbortSignal, ms);
  };
  try {
    await fetchMeta(ORIGIN, { fetch: stubFetch(() => json({ product: "ogre", api: 1 })).fetch });
    await fetchMeta(ORIGIN, { fetch: stubFetch(() => json({ product: "ogre", api: 1 })).fetch, timeoutMs: 1234 });
  } finally {
    AbortSignal.timeout = original;
  }
  assert.deepEqual(asked, [META_TIMEOUT_MS, 1234]);
  assert.equal(META_TIMEOUT_MS, 5000);
});

test("answers that are not an Ogre server's meta document are refused, each with its reason", async () => {
  /** @type {[string, () => Response, string, RegExp][]} */
  const cases = [
    ["another product", () => json({ product: "golinks", version: "1", api: 1 }), "not_ogre", /not an Ogre server/],
    ["no product", () => json({ version: "1", api: 1 }), "not_ogre", /not an Ogre server/],
    ["product in another case", () => json({ product: "Ogre", api: 1 }), "not_ogre", /not an Ogre server/],
    ["an array", () => json([{ product: "ogre", api: 1 }]), "not_ogre", /not an Ogre server/],
    ["null", () => json(null), "not_ogre", /not an Ogre server/],
    ["HTML", () => new Response("<!doctype html><title>Router</title>", { status: 200 }), "not_ogre", /not an Ogre server/],
    ["an empty body", () => new Response("", { status: 200 }), "not_ogre", /not an Ogre server/],
    ["API version 2", () => json({ product: "ogre", version: "2.0.0", api: 2 }), "api_version", /API version 2; this extension needs version 1/],
    ["API version as a string", () => json({ product: "ogre", api: "1" }), "api_version", /API version 1; this extension needs version 1/],
    ["no API version", () => json({ product: "ogre", version: "0.0.1" }), "api_version", /API version undefined/],
    ["403 from the cross-site guard", () => json({ error: { code: "request.cross_site" } }, { status: 403 }), "refused", /403 Forbidden/],
    ["404", () => new Response("not found", { status: 404, statusText: "Not Found" }), "status", /answered 404 Not Found/],
    ["500", () => new Response("", { status: 500 }), "status", /answered 500/],
    ["a redirect", () => new Response(null, { status: 302, headers: { Location: "http://elsewhere/" } }), "status", /redirect/],
  ];
  for (const [name, answer, kind, message] of cases) {
    const result = await fetchMeta(ORIGIN, { fetch: stubFetch(answer).fetch });
    assert.equal(result.ok, false, name);
    if (result.ok) continue;
    assert.equal(result.kind, kind, name);
    assert.match(result.message, message, name);
    assert.ok(result.message.includes(ORIGIN), `${name}: the message names the address`);
  }
});

test("a body larger than any meta answer is cut, not read whole", async () => {
  // 1 MiB of valid JSON: if it were read whole it would parse and pass.
  const padding = "x".repeat(1024 * 1024);
  const stub = stubFetch(() => json({ product: "ogre", api: 1, padding }));
  const result = await fetchMeta(ORIGIN, { fetch: stub.fetch });
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.kind, "not_ogre");
});

test("a server that cannot be reached is reported in words", async () => {
  const result = await fetchMeta(ORIGIN, {
    fetch: async () => {
      throw new TypeError("Failed to fetch");
    },
  });
  assert.deepEqual(result, { ok: false, kind: "unreachable", message: "Could not connect to http://localhost:4655. Is the Ogre server running there?" });
});

test("a server that does not answer in time is given up on", async () => {
  /** @type {typeof fetch} a fetch that never answers, until its signal aborts */
  const hang = (_input, init) =>
    new Promise((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(init.signal?.reason));
    });
  const started = Date.now();
  const result = await fetchMeta(ORIGIN, { fetch: hang, timeoutMs: 50 });
  assert.ok(Date.now() - started < 2000, "it did not wait for the default timeout");
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.kind, "timeout");
  assert.equal(result.ok === false && result.message, "http://localhost:4655 did not answer within 50 ms.");
});

test("a body that stops halfway is reported like a connection failure", async () => {
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode('{"product":'));
      controller.error(new TypeError("network error"));
    },
  });
  const result = await fetchMeta(ORIGIN, { fetch: async () => new Response(body, { status: 200 }) });
  assert.equal(result.ok === false && result.kind, "unreachable");
});
