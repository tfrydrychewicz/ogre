// Ogre service worker.
//
// Its one job in this milestone is to make sure the redirect rule is installed.
// The rule itself does the redirecting, inside Chrome, whether or not this
// worker is running (plan section 3.7, NFR-4).
//
// Both listeners are registered synchronously at the top level, so Chrome can
// start a stopped worker and still deliver the event that started it.

import { DEFAULT_DIRECTORY_URL, buildRedirectRule, planRuleUpdate } from "./lib/config.js";

/**
 * The check in progress, if any. Checks run one after another: install,
 * browser start and worker start can all ask within the same moment, and two
 * checks at once would both find the rule missing and both write it.
 *
 * @type {Promise<void>}
 */
let queue = Promise.resolve();

/**
 * Installs the redirect rule for the default Directory unless it is already
 * the only dynamic rule. Never rejects: a failure is logged, and the next
 * time the worker starts it tries again.
 *
 * @param {string} reason which event asked, for the log
 * @returns {Promise<void>} settles when this check, and every one asked for before it, is done
 */
function ensureRedirectRule(reason) {
  queue = queue.then(async () => {
    try {
      const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
      const installed = await chrome.declarativeNetRequest.getDynamicRules();
      const change = planRuleUpdate(installed, wanted);
      if (change === null) return;
      await chrome.declarativeNetRequest.updateDynamicRules(change);
      console.info(`[ogre] redirect rule installed (${reason})`);
    } catch (error) {
      console.error(`[ogre] could not install the redirect rule (${reason})`, error);
    }
  });
  return queue;
}

chrome.runtime.onInstalled.addListener((details) => {
  void ensureRedirectRule(`onInstalled:${details.reason}`);
});

chrome.runtime.onStartup.addListener(() => {
  void ensureRedirectRule("onStartup");
});

// Every time the worker starts, whatever woke it: one read of the installed
// rules when nothing needs changing. This is what repairs a rule whose
// install failed, without waiting for the next browser start.
void ensureRedirectRule("worker start");
