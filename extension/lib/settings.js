// @ts-check
// The Directory URL as stored, and the calls the pages make to change it.
//
// These use chrome.* and run in the extension's pages and service worker. The
// rules themselves (what is a valid Directory URL, which access to release)
// live in config.js, which needs no browser.
//
// One writer: only the service worker changes the stored URL, the redirect
// rule and the granted access, one change at a time (background.js). A page
// asks it with SET_DIRECTORY. Two pages saving at once, or a save racing the
// worker's own start-up check, therefore cannot leave the rule pointing at
// one Directory and the storage naming another.

import { DEFAULT_DIRECTORY_URL, STORAGE_KEYS, hostPattern, needsOptionalAccess, normalizeDirectoryUrl } from "./config.js";

/** The message type a page sends to the worker to change the Directory. */
export const SET_DIRECTORY = "ogre:set-directory";

/**
 * @typedef {object} SetDirectoryRequest
 * @property {typeof SET_DIRECTORY} type
 * @property {string} url the new Directory URL; the worker validates it again
 */

/**
 * @typedef {{ ok: true, origin: string, released: string[] } | { ok: false, message: string }} SetDirectoryReply
 *   released: the host access given up, as patterns; empty when there was none to give up
 */

/**
 * @typedef {object} StoredDirectory
 * @property {string} origin the Directory to use: the stored one, or the default
 * @property {boolean} saved whether a Directory URL was saved by the user
 * @property {unknown} [invalid] a stored value that is not a valid Directory URL, which is
 *   ignored in favor of the default. Only a hand-edited or corrupted storage holds one.
 */

/**
 * Reads the Directory the extension uses.
 *
 * @returns {Promise<StoredDirectory>}
 */
export async function loadDirectory() {
  const stored = (await chrome.storage.local.get(STORAGE_KEYS.directoryUrl))[STORAGE_KEYS.directoryUrl];
  if (stored === undefined) return { origin: DEFAULT_DIRECTORY_URL, saved: false };
  const parsed = typeof stored === "string" ? normalizeDirectoryUrl(stored) : null;
  if (parsed?.ok) return { origin: parsed.origin, saved: true };
  return { origin: DEFAULT_DIRECTORY_URL, saved: false, invalid: stored };
}

/**
 * Asks Chrome for access to one Directory, if the manifest does not already
 * give it. Chrome shows its permission prompt, and grants only that origin.
 *
 * Call it directly in a click handler, before any `await`: Chrome lets an
 * extension ask for a permission only while handling a user gesture.
 *
 * @param {string} origin a normalized origin
 * @returns {Promise<boolean>} whether the extension now has access
 */
export function requestAccess(origin) {
  if (!needsOptionalAccess(origin)) return Promise.resolve(true);
  return chrome.permissions.request({ origins: [hostPattern(origin)] });
}

/**
 * Asks the service worker to make `url` the Directory.
 *
 * @param {string} url
 * @returns {Promise<SetDirectoryReply>}
 */
export async function setDirectory(url) {
  /** @type {SetDirectoryRequest} */
  const request = { type: SET_DIRECTORY, url };
  try {
    /** @type {unknown} */
    const reply = await chrome.runtime.sendMessage(request);
    if (isReply(reply)) return reply;
    return { ok: false, message: "The extension's background script gave no answer. Reload the extension and try again." };
  } catch (error) {
    return { ok: false, message: `The extension's background script could not be reached (${errorText(error)}). Reload the extension and try again.` };
  }
}

/**
 * @param {unknown} value
 * @returns {value is SetDirectoryReply}
 */
function isReply(value) {
  if (typeof value !== "object" || value === null) return false;
  const reply = /** @type {Record<string, unknown>} */ (value);
  if (reply.ok === true) return typeof reply.origin === "string" && Array.isArray(reply.released);
  return reply.ok === false && typeof reply.message === "string";
}

/**
 * @param {unknown} error
 * @returns {string}
 */
export function errorText(error) {
  return error instanceof Error ? error.message : String(error);
}
