// @ts-check
// Ogre service worker.
//
// It keeps one thing true: the redirect rule sends go links to the Directory
// the user chose (or the default one). The rule itself does the redirecting,
// inside Chrome, whether or not this worker is running (plan section 3.7,
// NFR-4). The worker also opens the first-run page once, and is the only code
// that changes the Directory: the settings page asks it to (SET_DIRECTORY).
//
// Every listener is registered synchronously at the top level, so Chrome can
// start a stopped worker and still deliver the event that started it.
//
// It never contacts any server, and observes no navigation: the first-run
// page does its own watching, only while it is open.

import { FIRST_RUN_PAGE, SESSION_KEYS, STORAGE_KEYS, buildRedirectRule, hostPattern, needsOptionalAccess, normalizeDirectoryUrl, originsToRelease, planRuleUpdate } from "./lib/config.js";
import { SET_DIRECTORY, errorText, loadDirectory } from "./lib/settings.js";

/** @typedef {import("./lib/settings.js").SetDirectoryReply} SetDirectoryReply */
/** @typedef {chrome.declarativeNetRequest.Rule} Rule */

/**
 * The end of the last task queued. Tasks run one after another: install,
 * browser start, worker start and a save from the settings page can all ask
 * within the same moment, and two at once would each read the rules, decide
 * on their own, and the last write would win whatever the user asked for.
 *
 * @type {Promise<void>}
 */
let queue = Promise.resolve();

/**
 * Runs `task` after every task queued before it.
 *
 * @template T
 * @param {() => Promise<T>} task
 * @returns {Promise<T>} settles with the task's own result
 */
function enqueue(task) {
  const run = queue.then(task);
  queue = run.then(
    () => undefined,
    () => undefined,
  );
  return run;
}

/**
 * Installs the redirect rule for the stored Directory unless it is already
 * the only dynamic rule. Never rejects: a failure is logged, and the next
 * time the worker starts it tries again.
 *
 * @param {string} reason which event asked, for the log
 * @returns {Promise<void>} settles when this check, and every task queued before it, is done
 */
function ensureRedirectRule(reason) {
  return enqueue(async () => {
    try {
      const directory = await loadDirectory();
      if (directory.invalid !== undefined) {
        console.warn(`[ogre] the stored Directory URL ${JSON.stringify(directory.invalid)} is not valid; using ${directory.origin}`);
      }
      const wanted = buildRedirectRule(directory.origin);
      const installed = await chrome.declarativeNetRequest.getDynamicRules();
      const change = planRuleUpdate(installed, wanted);
      if (change === null) return;
      await chrome.declarativeNetRequest.updateDynamicRules(change);
      console.info(`[ogre] redirect rule installed for ${directory.origin} (${reason})`);
    } catch (error) {
      console.error(`[ogre] could not install the redirect rule (${reason})`, error);
    }
  });
}

/**
 * Makes `url` the Directory: the redirect rule first, in one call that
 * removes the old rule and adds the new, then the stored URL, then the
 * release of access no longer needed. When the rule or the storage cannot be
 * written, nothing is left changed. A failure to release access is only
 * logged: the Directory has changed, and extra access is the lesser problem.
 *
 * @param {string} url what the settings page sent; validated again here
 * @returns {Promise<SetDirectoryReply>}
 */
async function changeDirectory(url) {
  const parsed = normalizeDirectoryUrl(url);
  if (!parsed.ok) return { ok: false, message: parsed.message };
  const { origin } = parsed;

  // The settings page asks for access before it sends the request. Holding
  // it is what keeps the connection test working afterwards; a Directory the
  // extension may not contact is never saved.
  if (needsOptionalAccess(origin) && !(await chrome.permissions.contains({ origins: [hostPattern(origin)] }))) {
    return { ok: false, message: `Chrome has not given the extension access to ${origin}, so nothing was changed. Save again and allow access when Chrome asks.` };
  }

  /** @type {Rule[]} */
  let before;
  try {
    before = await chrome.declarativeNetRequest.getDynamicRules();
    const change = planRuleUpdate(before, buildRedirectRule(origin));
    if (change !== null) await chrome.declarativeNetRequest.updateDynamicRules(change);
  } catch (error) {
    console.error(`[ogre] could not install the redirect rule for ${origin}`, error);
    return { ok: false, message: `Chrome refused the new redirect rule (${errorText(error)}). Nothing was changed.` };
  }

  try {
    await chrome.storage.local.set({ [STORAGE_KEYS.directoryUrl]: origin });
  } catch (error) {
    console.error(`[ogre] could not store the Directory URL ${origin}`, error);
    try {
      const now = await chrome.declarativeNetRequest.getDynamicRules();
      const ids = [...new Set([...now, ...before].map((rule) => rule.id))];
      await chrome.declarativeNetRequest.updateDynamicRules({ removeRuleIds: ids, addRules: before });
    } catch (restoreError) {
      // The next worker start puts the stored Directory's rule back.
      console.error("[ogre] could not restore the previous redirect rule", restoreError);
    }
    return { ok: false, message: `The new address could not be stored (${errorText(error)}). Nothing was changed.` };
  }

  /** @type {string[]} */
  let released = [];
  try {
    const granted = (await chrome.permissions.getAll()).origins ?? [];
    const release = originsToRelease(granted, origin, chrome.runtime.getManifest().host_permissions ?? []);
    if (release.length > 0 && (await chrome.permissions.remove({ origins: release }))) released = release;
  } catch (error) {
    console.warn("[ogre] could not release access to the previous Directory", error);
  }

  console.info(`[ogre] Directory set to ${origin}${released.length > 0 ? `; released ${released.join(", ")}` : ""}`);
  return { ok: true, origin, released };
}

/**
 * Gives up host access that no Directory uses: an origin the user allowed
 * for a "Test connection" on the settings page and then never saved. Saving
 * gives such access up at once (changeDirectory); this catches the test
 * that was not followed by a save. It runs only when the browser starts and
 * when the extension is installed or updated, never while the settings page
 * may be between a test and a save, which would make Chrome ask again.
 * Never rejects: a failure is logged, and the access waits for the next start.
 *
 * @param {string} reason which event asked, for the log
 * @returns {Promise<void>}
 */
function releaseUnusedAccess(reason) {
  return enqueue(async () => {
    try {
      const directory = await loadDirectory();
      const granted = (await chrome.permissions.getAll()).origins ?? [];
      const release = originsToRelease(granted, directory.origin, chrome.runtime.getManifest().host_permissions ?? []);
      if (release.length > 0 && (await chrome.permissions.remove({ origins: release }))) {
        console.info(`[ogre] released access no Directory uses: ${release.join(", ")} (${reason})`);
      }
    } catch (error) {
      console.warn(`[ogre] could not release unused access (${reason})`, error);
    }
  });
}

/**
 * Opens the first-run page, unless the user has finished it or it is already
 * open in a tab.
 *
 * Both checks matter in practice: Chromium started with --load-extension
 * reports a fresh install on every launch (L0 spike, check 14), so without
 * them the page would open in every session, and again beside a copy that
 * the browser restored from the last one.
 *
 * @returns {Promise<void>}
 */
async function openFirstRun() {
  try {
    const stored = await chrome.storage.local.get(STORAGE_KEYS.firstRunDone);
    if (stored[STORAGE_KEYS.firstRunDone] === true) return;
    const url = chrome.runtime.getURL(FIRST_RUN_PAGE);
    const open = await chrome.runtime.getContexts({
      contextTypes: [/** @type {chrome.runtime.ContextType} */ ("TAB")],
      documentUrls: [url],
    });
    if (open.length > 0) return;
    await chrome.tabs.create({ url });
  } catch (error) {
    console.error("[ogre] could not open the first-run page", error);
  }
}

/**
 * @param {unknown} message
 * @returns {message is import("./lib/settings.js").SetDirectoryRequest}
 */
function isSetDirectory(message) {
  if (typeof message !== "object" || message === null) return false;
  const request = /** @type {Record<string, unknown>} */ (message);
  return request.type === SET_DIRECTORY && typeof request.url === "string";
}

/**
 * Records that the install event has been handled (SESSION_KEYS). Never
 * rejects: only the tests read the mark.
 *
 * @returns {Promise<void>}
 */
async function markInstallHandled() {
  try {
    await chrome.storage.session.set({ [SESSION_KEYS.installHandled]: Date.now() });
  } catch (error) {
    console.warn("[ogre] could not record that the install was handled", error);
  }
}

chrome.runtime.onInstalled.addListener((details) => {
  const rule = ensureRedirectRule(`onInstalled:${details.reason}`);
  const access = releaseUnusedAccess(`onInstalled:${details.reason}`);
  const page = details.reason === "install" ? openFirstRun() : undefined;
  // None of them rejects.
  void Promise.all([rule, access, page]).then(markInstallHandled);
});

chrome.runtime.onStartup.addListener(() => {
  void ensureRedirectRule("onStartup");
  void releaseUnusedAccess("onStartup");
});

// The rule follows the stored URL, whoever changed it: changeDirectory below
// (a no-op check then, as the rule is already right), or a value written to
// storage by other means, such as an end-to-end test, which cannot answer
// Chrome's permission prompt.
chrome.storage.onChanged.addListener((changes, areaName) => {
  if (areaName === "local" && STORAGE_KEYS.directoryUrl in changes) void ensureRedirectRule("storage changed");
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  // Only this extension's own pages may change the Directory. Nothing else
  // can send here today (no content scripts, nothing externally
  // connectable); the check keeps it so if that ever changes.
  if (sender.id !== chrome.runtime.id || !sender.url?.startsWith(chrome.runtime.getURL("")) || !isSetDirectory(message)) {
    return false;
  }
  enqueue(() => changeDirectory(message.url)).then(sendResponse, (error) => {
    console.error("[ogre] could not change the Directory", error);
    sendResponse({ ok: false, message: `The change failed (${errorText(error)}).` });
  });
  return true; // the answer comes later
});

// Every time the worker starts, whatever woke it: one read of the stored URL
// and the installed rules when nothing needs changing. This is what repairs a
// rule whose install failed, without waiting for the next browser start.
void ensureRedirectRule("worker start");
