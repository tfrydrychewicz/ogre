// godir spike: service worker. THROWAWAY (milestone L0), deleted when L1 starts.
//
// Every listener is registered synchronously at the top level, so Chrome can
// wake a stopped worker and still deliver the event that woke it.

const DIRECTORY_ORIGIN = "http://localhost:4655";
const KEEP_LAST = 50;

/**
 * The one redirect rule of plan section 3.7.
 * The substitution Chrome receives is `http://localhost:4655/` followed by a
 * backslash and the digit 1 (24 characters): capture group 1 of regexFilter.
 * @type {chrome.declarativeNetRequest.Rule}
 */
const REDIRECT_RULE = {
  id: 1,
  priority: 1,
  condition: {
    regexFilter: "^https?://go/(.*)$",
    resourceTypes: ["main_frame"],
  },
  action: {
    type: "redirect",
    redirect: { regexSubstitution: "http://localhost:4655/\\1" },
  },
};

/**
 * Installs rule 1 unless it is already there, and logs what it found.
 *
 * The plan (section 3.7) relies on a dynamic rule surviving a browser restart
 * by itself. So onStartup only looks, and onInstalled installs only when the
 * rule is missing: the "lifecycle" list on the status page then shows directly
 * whether the rule was still present after a restart (check 14). Installing
 * again on every start would hide exactly that.
 *
 * @param {string} event which event asked ("onInstalled:install", "onStartup", ...)
 * @param {boolean} installIfMissing
 */
async function checkRedirectRule(event, installIfMissing) {
  const entry = { event, rulePresent: null, installedNow: false, time: new Date().toISOString() };
  try {
    const rules = await chrome.declarativeNetRequest.getDynamicRules();
    entry.rulePresent = rules.some((rule) => rule.id === REDIRECT_RULE.id);
    if (!entry.rulePresent && installIfMissing) {
      // One atomic call; removing id 1 first makes it safe to repeat.
      await chrome.declarativeNetRequest.updateDynamicRules({
        removeRuleIds: [REDIRECT_RULE.id],
        addRules: [REDIRECT_RULE],
      });
      entry.installedNow = true;
    }
    console.log("[godir spike] redirect rule checked", entry);
  } catch (err) {
    entry.error = String(err);
    console.error("[godir spike] redirect rule check failed", entry);
  }
  await appendCapped("lifecycle", entry);
}

/** Tail of the storage write chain: appends run one at a time, in event order. */
let storageQueue = Promise.resolve();

/**
 * Runs one storage change after all earlier ones. Events can arrive faster
 * than a get/set round trip, so every write goes through this queue instead of
 * racing the others and losing entries.
 * @param {string} what for the error message
 * @param {() => Promise<void>} change
 * @returns {Promise<void>}
 */
function queued(what, change) {
  storageQueue = storageQueue.then(change).catch((err) => {
    console.error(`[godir spike] could not ${what}`, err);
  });
  return storageQueue;
}

/**
 * Appends an entry to a list in chrome.storage.local, keeping the last 50.
 * @param {"goSeen" | "directoryErrors" | "lifecycle"} key
 * @param {object} entry
 * @returns {Promise<void>}
 */
function appendCapped(key, entry) {
  return queued(`append to ${key}`, async () => {
    const stored = await chrome.storage.local.get({ [key]: [] });
    const list = Array.isArray(stored[key]) ? stored[key] : [];
    list.push(entry);
    await chrome.storage.local.set({ [key]: list.slice(-KEEP_LAST) });
  });
}

/**
 * Asks the Directory for its meta endpoint from this service worker. The plan
 * needs this for "Test connection" (L4.2) and as the fallback of check 12;
 * Local Network Access could in principle stand in the way of it.
 * @returns {Promise<object>} what happened, never a rejection
 */
async function probeDirectory() {
  const url = DIRECTORY_ORIGIN + "/-/api/v1/meta";
  const result = { from: "service worker", url, time: new Date().toISOString() };
  try {
    const response = await fetch(url, { cache: "no-store", signal: AbortSignal.timeout(3000) });
    result.ok = response.ok;
    result.status = response.status;
    result.body = await response.text();
  } catch (err) {
    result.ok = false;
    result.error = String(err);
  }
  await queued("store the meta probe", () => chrome.storage.local.set({ workerMeta: result }));
  return result;
}

/** @param {number} timeStamp milliseconds since the epoch, from a webRequest event */
function isoTime(timeStamp) {
  return new Date(timeStamp).toISOString();
}

chrome.runtime.onInstalled.addListener((details) => {
  void checkRedirectRule("onInstalled:" + details.reason, true);
});

chrome.runtime.onStartup.addListener(() => {
  void checkRedirectRule("onStartup", false);
});

// Requests from the status page. "clear" goes through the same queue as the
// appends, so an append already under way cannot bring cleared entries back.
chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type === "clear") {
    void queued("clear", () => chrome.storage.local.set({ goSeen: [], directoryErrors: [] })).then(() =>
      sendResponse({ cleared: true }),
    );
    return true; // the answer comes later
  }
  if (message?.type === "probeMeta") {
    void probeDirectory().then(sendResponse);
    return true;
  }
  return false;
});

// Check 13: does the extension see go navigations before they are redirected?
chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    void appendCapped("goSeen", {
      url: details.url,
      time: isoTime(details.timeStamp),
      tabId: details.tabId,
    });
  },
  { urls: ["*://go/*"], types: ["main_frame"] },
);

// Check 12: a Directory that does not answer. Record the failure, then replace
// the tab with the unreachable page. That page is a chrome-extension:// URL, so
// it can never match this filter, and nothing here retries on its own: a failed
// Retry click simply lands on the unreachable page again.
chrome.webRequest.onErrorOccurred.addListener(
  (details) => {
    void handleDirectoryError(details);
  },
  { urls: [DIRECTORY_ORIGIN + "/*"], types: ["main_frame"] },
);

/**
 * Records the failure and replaces the tab, unless there is no tab to replace,
 * the navigation was cancelled rather than failed (the user went elsewhere or
 * pressed Stop), or the failure belongs to a prerender the user never asked to
 * see.
 *
 * The entry also keeps what chrome.tabs.get says about the tab at this moment.
 * That is an observation for L5.3, which would rather decide by "does the tab
 * still show the URL that failed" than by comparing the error string: the
 * spike shows whether those fields are usable when the event fires.
 * @param {chrome.webRequest.WebResponseErrorDetails} details
 */
async function handleDirectoryError(details) {
  const entry = {
    url: details.url,
    error: details.error,
    time: isoTime(details.timeStamp),
    tabId: details.tabId,
    tabUrlAtError: null,
    tabPendingUrlAtError: null,
    replaced: false,
  };
  const lifecycle = details.documentLifecycle;
  const skip =
    details.tabId < 0 ||
    details.error === "net::ERR_ABORTED" ||
    (lifecycle !== undefined && lifecycle !== "active");
  try {
    if (details.tabId >= 0) {
      const tab = await chrome.tabs.get(details.tabId);
      entry.tabUrlAtError = tab.url ?? null;
      entry.tabPendingUrlAtError = tab.pendingUrl ?? null;
    }
    if (!skip) {
      const page =
        chrome.runtime.getURL("unreachable.html") +
        "?url=" +
        encodeURIComponent(details.url) +
        "&error=" +
        encodeURIComponent(details.error);
      await chrome.tabs.update(details.tabId, { url: page });
      entry.replaced = true;
    }
  } catch (err) {
    entry.replaceError = String(err);
    console.error("[godir spike] could not show the unreachable page", err);
  }
  await appendCapped("directoryErrors", entry);
}

// Check 15: only the extension-omnibox variant declares the keyword, so
// chrome.omnibox is undefined in the plain extension.
if (chrome.omnibox) {
  chrome.omnibox.onInputEntered.addListener((text) => {
    // No tab id: updates the selected tab of the current window.
    chrome.tabs.update({ url: DIRECTORY_ORIGIN + "/" + text }).catch((err) => {
      console.error("[godir spike] omnibox navigation failed", err);
    });
  });
}
