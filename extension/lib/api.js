// @ts-check
// Calls from the extension to the Directory. There is one: the meta endpoint,
// which says whether an address answers as an Ogre server and which API it
// speaks (EXT-2). It is the only server the extension ever contacts (NFR-18),
// and only when a page of the extension asks: the settings page's "Test
// connection" and "Save", and the first-run page's check.
//
// No chrome.* call here, so node --test can run it with a stand-in fetch.

/** The product identifier the meta endpoint must report (plan section 3.5). */
export const PRODUCT = "ogre";

/** The API version this extension speaks. A Directory reporting another one is refused. */
export const API_VERSION = 1;

/** How long a connection test waits before it gives up, in milliseconds. */
export const META_TIMEOUT_MS = 5000;

/** The most of the meta answer that is read: it is a few dozen bytes. */
const MAX_META_BYTES = 16 * 1024;

/**
 * @typedef {object} MetaOk
 * @property {true} ok
 * @property {string} version the server's version, such as "0.1.0"; empty when it reported none
 * @property {number} api always API_VERSION
 */

/**
 * @typedef {object} MetaError
 * @property {false} ok
 * @property {"timeout" | "unreachable" | "refused" | "status" | "not_ogre" | "api_version"} kind
 *   timeout: no answer in time. unreachable: no connection, or the browser blocked the request.
 *   refused: the server answered 403, which an Ogre server does for a request it takes for
 *   another site's. status: some other non-200 answer, or a redirect. not_ogre: an answer that
 *   is not an Ogre meta document. api_version: an Ogre server speaking another API version.
 * @property {string} message a sentence to show the user
 */

/** @typedef {MetaOk | MetaError} MetaResult */

/**
 * Asks a Directory who it is. Never rejects: every failure is a MetaError
 * whose message says what happened in words a user can act on.
 *
 * The request carries no cookies and is never cached, and a redirect is
 * reported instead of followed: the meta endpoint never redirects, so an
 * address that does is not an Ogre server's root.
 *
 * @param {string} origin a normalized Directory origin, such as "http://localhost:4655"
 * @param {object} [options]
 * @param {number} [options.timeoutMs] defaults to META_TIMEOUT_MS
 * @param {typeof fetch} [options.fetch] for tests; defaults to the global fetch
 * @returns {Promise<MetaResult>}
 */
export async function fetchMeta(origin, { timeoutMs = META_TIMEOUT_MS, fetch: fetchImpl = globalThis.fetch } = {}) {
  const url = `${origin}/-/api/v1/meta`;
  /** @type {Response} */
  let response;
  /** @type {string} */
  let body;
  try {
    response = await fetchImpl(url, {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "omit",
      cache: "no-store",
      redirect: "manual",
      signal: AbortSignal.timeout(timeoutMs),
    });
    body = await readLimited(response, MAX_META_BYTES);
  } catch (error) {
    if (isTimeout(error)) {
      return fail("timeout", `${origin} did not answer within ${duration(timeoutMs)}.`);
    }
    return fail("unreachable", `Could not connect to ${origin}. Is the Ogre server running there?`);
  }

  if (response.type === "opaqueredirect" || (response.status >= 300 && response.status < 400)) {
    return fail("status", `${origin} answered with a redirect, so it is not an Ogre server's address. Use the address the server prints when it starts.`);
  }
  if (response.status === 403) {
    return fail("refused", `${origin} refused the request (403 Forbidden). An Ogre server answers this way when it takes the request for another site's.`);
  }
  if (response.status !== 200) {
    return fail("status", `${origin} answered ${response.status}${response.statusText ? ` ${response.statusText}` : ""}, so it is not an Ogre server, or not this version of one.`);
  }

  /** @type {unknown} */
  let meta;
  try {
    meta = JSON.parse(body);
  } catch {
    meta = null;
  }
  if (!isRecord(meta) || meta.product !== PRODUCT) {
    return fail("not_ogre", `Something answers at ${origin}, but it is not an Ogre server.`);
  }
  if (meta.api !== API_VERSION) {
    return fail("api_version", `The Ogre server at ${origin} speaks API version ${String(meta.api)}; this extension needs version ${API_VERSION}. Update the extension or the server so they match.`);
  }
  const version = typeof meta.version === "string" ? meta.version : "";
  return { ok: true, version, api: API_VERSION };
}

/**
 * Reads at most `limit` bytes of a body as text. A larger body is cut, which
 * makes it invalid JSON: nothing the meta endpoint sends comes close.
 *
 * @param {Response} response
 * @param {number} limit
 * @returns {Promise<string>}
 */
async function readLimited(response, limit) {
  if (!response.body) return "";
  const reader = response.body.getReader();
  /** @type {Uint8Array[]} */
  const chunks = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    size += value.byteLength;
    if (size > limit) {
      await reader.cancel();
      break;
    }
  }
  const all = new Uint8Array(Math.min(size, limit + 1));
  let offset = 0;
  for (const chunk of chunks) {
    const part = chunk.subarray(0, all.length - offset);
    all.set(part, offset);
    offset += part.length;
    if (offset >= all.length) break;
  }
  return new TextDecoder().decode(all);
}

/**
 * @param {number} ms
 * @returns {string} for example "5 seconds", "1 second", "50 ms"
 */
function duration(ms) {
  if (ms < 1000) return `${ms} ms`;
  const seconds = Math.round(ms / 1000);
  return `${seconds} second${seconds === 1 ? "" : "s"}`;
}

/**
 * @param {unknown} error
 * @returns {boolean}
 */
function isTimeout(error) {
  return error instanceof Error && (error.name === "TimeoutError" || error.name === "AbortError");
}

/**
 * @param {unknown} value
 * @returns {value is Record<string, unknown>}
 */
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * @param {MetaError["kind"]} kind
 * @param {string} message
 * @returns {MetaError}
 */
function fail(kind, message) {
  return { ok: false, kind, message };
}
