// @ts-check
// The first-run page (plan L4.3, EXT-3).
//
// Step 1 asks the Directory's meta endpoint whether it answers. Step 2 asks
// the user to type go/ and waits until Chrome navigates there. It is offered
// only once the Directory answers: a go/ visit that fails does not teach
// Chrome that go is a site (L0 spike, check 3). Step 3 has the user type a
// go link without the trailing slash and explains what to do if Chrome
// searches instead.
//
// How the page sees step 2: a webRequest listener for main-frame requests to
// go, in http and https (with "Always use secure connections" at its strict
// level Chrome asks for https://go/ before the extension sees it; L0 spike,
// check 6). It observes only: it cannot block or change a request, and it
// exists only while this page is open, so the extension watches nothing at
// any other time. Chrome reports to it only requests to hosts the extension
// has access to, which for go is go and nothing else.
//
// The listener cannot tell a typed go/ from one a web page opened (a link,
// a script) in another tab: Chrome reports the initiating page's origin only
// to an extension with access to that page (seen in Chromium of Playwright
// 1.64), and the transition type ("typed") only through the webNavigation
// permission, which would add a "read your browsing history" warning for
// this one page. So a page open in another tab could mark steps 2 and 3
// done; the cost is a wrong hint on this page, nothing more. The link it
// opened is shown without its query, which may hold anything.
//
// The page follows the Directory address in storage: saved in Settings in
// another tab, which is where the README and this page send a user whose
// server runs elsewhere, it is checked at once, with no reload.
//
// The page remembers in storage that step 2 is done, and that the first run
// is over, so the service worker never opens it again by itself and a page
// reopened later shows the step as done.

import { fetchMeta } from "../lib/api.js";
import { STORAGE_KEYS, goLinkPath } from "../lib/config.js";
import { byId, clearNotice, newTabKeys, showNotice } from "../lib/page.js";
import { errorText, loadDirectory } from "../lib/settings.js";

/** @typedef {import("../lib/api.js").MetaError["kind"]} MetaErrorKind */

/** How long the server check waits. Shorter than the settings page's: this one repeats by itself. */
const CHECK_TIMEOUT_MS = 3000;

/** The longest go link path shown back to the user. */
const MAX_SHOWN_PATH = 120;

const directoryText = byId("directory", HTMLElement);
const serverStatus = byId("server-status", HTMLElement);
const serverHelp = byId("server-help", HTMLElement);
const serverHelpOther = byId("server-help-other", HTMLElement);
const serverActions = byId("server-actions", HTMLElement);
const goStatus = byId("go-status", HTMLElement);
const linkStatus = byId("link-status", HTMLElement);
const finishStatus = byId("finish-status", HTMLElement);
const finishButton = byId("finish", HTMLButtonElement);

/**
 * @typedef {object} State
 * @property {string} origin the Directory
 * @property {boolean | null} serverOk null while the first check runs
 * @property {MetaErrorKind | null} failure why the last check failed; null when it did not
 * @property {boolean} checking a check is in flight
 * @property {boolean} checkAgain the address changed while a check was in flight: check the new one after it
 * @property {boolean} sawGo step 2 is done: Chrome navigated to go while the server answered
 * @property {string | null} sawLink step 3 is done: the path of a go link opened after step 2
 * @property {boolean} finished the user clicked Finish or "Don't show this page again"
 */

/** @type {State} */
const state = { origin: "", serverOk: null, failure: null, checking: false, checkAgain: false, sawGo: false, sawLink: null, finished: false };

/**
 * Whether a failed check means that nothing answers, so the help is how to
 * start Ogre; otherwise something else answers there (another program on
 * the port, or an Ogre server of another API version), and starting Ogre
 * on that address would fail.
 *
 * @param {MetaErrorKind | null} kind
 * @returns {boolean}
 */
function nothingAnswers(kind) {
  return kind === "unreachable" || kind === "timeout";
}

/**
 * Sets one step's state attribute and the words of its badge. The badge
 * carries the state in text; the card's edge color only repeats it.
 *
 * @param {string} id the step's element id
 * @param {"waiting" | "active" | "done"} stepState
 * @param {string} badgeText
 * @param {boolean} [failed] the step needs the user to fix something
 */
function setStep(id, stepState, badgeText, failed = false) {
  byId(id, HTMLElement).dataset.state = stepState;
  const badge = byId(`${id}-badge`, HTMLElement);
  badge.textContent = badgeText;
  const variant = failed ? " badge-danger" : stepState === "done" ? " badge-success" : stepState === "active" ? " badge-accent" : "";
  badge.className = `badge${variant}`;
}

/** Brings every step in line with `state`. */
function render() {
  const failed = state.serverOk === false;
  if (state.serverOk === true) setStep("step-server", "done", "Done");
  else if (failed && nothingAnswers(state.failure)) setStep("step-server", "active", "Not answering", true);
  else if (failed && state.failure === "api_version") setStep("step-server", "active", "Wrong version", true);
  else if (failed) setStep("step-server", "active", "Wrong server", true);
  else setStep("step-server", "active", "Checking");
  serverHelp.hidden = !failed || !nothingAnswers(state.failure);
  serverHelpOther.hidden = !failed || nothingAnswers(state.failure) || state.failure === "api_version";
  serverActions.hidden = !failed;

  const goReady = state.serverOk === true || state.sawGo;
  if (state.sawGo) setStep("step-go", "done", "Done");
  else if (goReady) setStep("step-go", "active", "Your turn");
  else setStep("step-go", "waiting", "Waiting for step 1");
  byId("step-go-body", HTMLElement).hidden = !goReady || state.sawGo;
  byId("step-go-waiting", HTMLElement).hidden = goReady;

  if (state.sawLink !== null) setStep("step-link", "done", "Done");
  else if (state.sawGo) setStep("step-link", "active", "Your turn");
  else setStep("step-link", "waiting", "Waiting for step 2");
  byId("step-link-body", HTMLElement).hidden = !state.sawGo;
  byId("step-link-waiting", HTMLElement).hidden = state.sawGo;
  finishButton.hidden = !state.sawGo || state.finished;
  byId("finish-actions", HTMLElement).hidden = state.finished;
}

/**
 * Asks the Directory whether it answers. Checks never overlap: one asked
 * for while another runs (the address changed, the user came back to the
 * page) runs once that one is over. An answer about an address the page no
 * longer shows is dropped.
 */
async function checkServer() {
  if (state.checking) {
    state.checkAgain = true;
    return;
  }
  state.checking = true;
  state.checkAgain = false;
  const origin = state.origin;
  if (state.serverOk !== true) showNotice(serverStatus, "info", "Checking", `Asking ${origin} whether your Ogre server is running…`);
  try {
    const meta = await fetchMeta(origin, { timeoutMs: CHECK_TIMEOUT_MS });
    if (origin !== state.origin) return;
    state.serverOk = meta.ok;
    state.failure = meta.ok ? null : meta.kind;
    if (meta.ok) {
      const version = meta.version === "" ? "" : ` (version ${meta.version})`;
      showNotice(serverStatus, "success", "Your server answers", `An Ogre server${version} answers at ${origin}.`);
    } else if (nothingAnswers(meta.kind)) {
      showNotice(serverStatus, "error", "Error: no Ogre server answers yet", meta.message);
    } else {
      showNotice(serverStatus, "error", "Error: something else answers there", meta.message);
    }
  } finally {
    state.checking = false;
    render();
    if (state.checkAgain) void checkServer();
  }
}

/**
 * Shows the Directory address from storage, and checks it when it is not
 * the one the page shows already. Run at start-up and whenever the stored
 * address changes, for example when it is saved in Settings in another tab.
 */
async function followDirectory() {
  const directory = await loadDirectory();
  if (directory.origin === state.origin) return;
  const first = state.origin === "";
  state.origin = directory.origin;
  directoryText.textContent = directory.origin;
  // A new address: what the page knew about the old one no longer holds.
  // Step 2, once seen, stays done: Chrome has learned go as a site.
  state.serverOk = null;
  state.failure = null;
  if (!first) clearNotice(serverStatus);
  render();
  await checkServer();
}

/**
 * Checks again when the user comes back to this page, typically from the
 * terminal where they started the server. Once the server has answered,
 * nothing is checked again: the page does not poll.
 */
function recheckOnReturn() {
  if (state.serverOk !== true && document.visibilityState === "visible") void checkServer();
}

/**
 * Remembers in storage that the first run is over (and, when `sawGo`, that
 * step 2 was seen). A failure is logged: the worst outcome is that the page
 * opens again on the next install event.
 *
 * @param {{ sawGo?: boolean }} [what]
 */
async function rememberDone({ sawGo = false } = {}) {
  /** @type {Record<string, boolean>} */
  const items = { [STORAGE_KEYS.firstRunDone]: true };
  if (sawGo) items[STORAGE_KEYS.firstRunSawGo] = true;
  try {
    await chrome.storage.local.set(items);
  } catch (error) {
    console.error("[ogre] could not remember that the first run is done", error);
  }
}

/**
 * A go link as the user would type it, without its query or fragment (which
 * can hold anything, a session token included), shortened for display.
 *
 * @param {string} path what followed go/
 * @returns {string}
 */
function shownLink(path) {
  const link = `go/${path.replace(/[?#].*$/s, "")}`;
  return link.length > MAX_SHOWN_PATH ? `${link.slice(0, MAX_SHOWN_PATH - 1)}…` : link;
}

/**
 * Called by Chrome for every main-frame request to go while this page is
 * open, whichever tab it is in.
 *
 * @param {chrome.webRequest.OnBeforeRequestDetails} details
 * @returns {undefined} nothing: an observer never answers a request
 */
function onGoNavigation(details) {
  const path = goLinkPath(details.url);
  if (path === null) return undefined;

  if (!state.sawGo) {
    if (state.serverOk !== true) {
      showNotice(
        goStatus,
        "warning",
        "Warning: your server wasn't answering",
        `Chrome went to ${shownLink(path)}, but nothing answered at ${state.origin}, so Chrome may not remember go as a site. Once step 1 is done, type go/ again.`,
      );
      return undefined;
    }
    state.sawGo = true;
    // Written at once: when the user typed go/ in this very tab, the page is
    // about to be replaced by the Directory's.
    void rememberDone({ sawGo: true });
    showNotice(goStatus, "success", "Done", `Chrome opened ${shownLink(path)}, and the extension sent it to your server.`);
    render();
    return undefined;
  }

  if (state.sawLink === null && path !== "") {
    state.sawLink = path;
    showNotice(linkStatus, "success", "Done", `Chrome opened ${shownLink(path)} as a go link. Go links work in this browser.`);
    render();
  }
  return undefined;
}

/**
 * Ends the first run. The buttons go away, so focus moves to the message
 * that replaces them rather than to the top of the page.
 *
 * @param {import("../lib/page.js").NoticeKind} kind
 * @param {string} title
 * @param {string} text
 */
function finish(kind, title, text) {
  void rememberDone();
  state.finished = true;
  showNotice(finishStatus, kind, title, text);
  render();
  finishStatus.focus();
}

finishButton.addEventListener("click", () => {
  finish("success", "All set", "You can close this tab. To see this page again, open the extension's settings.");
});

byId("skip", HTMLButtonElement).addEventListener("click", () => {
  finish("info", "Noted", "This page won't open by itself again. You'll find it in the extension's settings.");
});

byId("check-again", HTMLButtonElement).addEventListener("click", () => void checkServer());
document.addEventListener("visibilitychange", recheckOnReturn);
window.addEventListener("focus", recheckOnReturn);

// Registered before anything is awaited, so no navigation made while the
// page loads is missed. No extraInfoSpec: this listener observes only.
chrome.webRequest.onBeforeRequest.addListener(onGoNavigation, { urls: ["*://go/*"], types: ["main_frame"] });

chrome.storage.onChanged.addListener((changes, areaName) => {
  if (areaName === "local" && STORAGE_KEYS.directoryUrl in changes) {
    followDirectory().catch((error) => showStartError(error));
  }
});

byId("new-tab-keys", HTMLElement).textContent = newTabKeys();
finishStatus.tabIndex = -1;

/**
 * Says that the page could not start, instead of leaving every step at its
 * first words.
 *
 * @param {unknown} error
 */
function showStartError(error) {
  console.error("[ogre] the first-run page could not read its settings", error);
  showNotice(serverStatus, "error", "Error: something went wrong", `The page could not read the extension's settings (${errorText(error)}). Reload the page; if that does not help, reload the extension on chrome://extensions.`);
}

void (async () => {
  try {
    const stored = await chrome.storage.local.get(STORAGE_KEYS.firstRunSawGo);
    if (stored[STORAGE_KEYS.firstRunSawGo] === true && !state.sawGo) {
      state.sawGo = true;
      showNotice(goStatus, "success", "Done", "Chrome has opened go/ before, with your server answering.");
    }
    await followDirectory();
  } catch (error) {
    showStartError(error);
  }
})();
