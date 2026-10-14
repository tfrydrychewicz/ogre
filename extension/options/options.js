// @ts-check
// The settings page: test and save the Directory URL (plan L4.2, EXT-2).
//
// Saving asks the service worker, which owns the redirect rule, the stored
// URL and the granted access (lib/settings.js). This page only checks the
// address, asks Chrome for access when the address needs it, and tests the
// connection.
//
// Chrome lets an extension ask for a permission only while it handles a user
// gesture, so every click handler below calls requestAccess before its first
// await. Validation is synchronous for the same reason.

import { fetchMeta } from "../lib/api.js";
import { DEFAULT_DIRECTORY_URL, hostPattern, needsOptionalAccess, normalizeDirectoryUrl } from "../lib/config.js";
import { byId, clearNotice, showNotice } from "../lib/page.js";
import { errorText, loadDirectory, requestAccess, setDirectory } from "../lib/settings.js";

/** @typedef {import("../lib/api.js").MetaResult} MetaResult */

const form = byId("form", HTMLFormElement);
const field = byId("url", HTMLInputElement);
const fieldError = byId("url-error", HTMLElement);
const current = byId("current", HTMLElement);
const result = byId("result", HTMLElement);
const unreachable = byId("unreachable", HTMLElement);
const unreachableReason = byId("unreachable-reason", HTMLElement);
const buttons = [
  byId("save", HTMLButtonElement),
  byId("test", HTMLButtonElement),
  byId("default", HTMLButtonElement),
  byId("save-anyway", HTMLButtonElement),
  byId("cancel-anyway", HTMLButtonElement),
];

/**
 * The origin the "Save anyway" warning is about. Editing the field takes the
 * warning away, so the button always saves the address the warning named.
 *
 * @type {string | null}
 */
let warnedOrigin = null;

/**
 * Checks the field. On a problem, marks the field and says what to change;
 * otherwise clears any earlier mark.
 *
 * @returns {string | null} the normalized origin, or null when the address cannot be used
 */
function readField() {
  const parsed = normalizeDirectoryUrl(field.value);
  if (!parsed.ok) {
    field.setAttribute("aria-invalid", "true");
    fieldError.textContent = parsed.message;
    fieldError.hidden = false;
    field.focus();
    return null;
  }
  field.removeAttribute("aria-invalid");
  fieldError.hidden = true;
  fieldError.textContent = "";
  return parsed.origin;
}

/**
 * The element that had focus when the buttons were disabled: disabling the
 * focused button drops focus to the page, and a keyboard user would lose
 * their place.
 *
 * @type {Element | null}
 */
let focusBeforeBusy = null;

/**
 * Disables the buttons while a request runs, so a second click cannot start
 * a second save halfway through the first. When the buttons come back,
 * focus returns to where it was, unless something else took it meanwhile or
 * the element is gone from view.
 *
 * @param {boolean} busy
 */
function setBusy(busy) {
  if (busy) focusBeforeBusy = document.activeElement;
  for (const button of buttons) button.disabled = busy;
  form.setAttribute("aria-busy", String(busy));
  if (!busy) {
    const previous = focusBeforeBusy;
    focusBeforeBusy = null;
    if (!canHoldFocus(document.activeElement) && previous instanceof HTMLElement && canHoldFocus(previous)) previous.focus();
  }
}

/**
 * Whether an element can keep the keyboard's focus: it is in the page,
 * shown, and not disabled. Chrome may leave focus on an element that was
 * hidden or disabled while it had it.
 *
 * @param {Element | null} element
 * @returns {boolean}
 */
function canHoldFocus(element) {
  if (!(element instanceof HTMLElement) || element === document.body || !element.isConnected) return false;
  if (element instanceof HTMLButtonElement && element.disabled) return false;
  return element.offsetParent !== null;
}

/**
 * Reports a failure no handler expected, such as a chrome.* call that
 * rejected, instead of leaving "Checking" on the page.
 *
 * @param {unknown} error
 */
function showUnexpected(error) {
  console.error("[ogre] the settings page failed", error);
  showNotice(result, "error", "Error: something went wrong", `${errorText(error)}. Reload the page and try again.`);
}

/** @param {string} origin */
function hideWarning(origin = "") {
  if (warnedOrigin !== null && warnedOrigin === origin) return;
  warnedOrigin = null;
  unreachable.hidden = true;
}

/**
 * The sentence for a successful connection test.
 *
 * @param {string} origin
 * @param {Extract<MetaResult, { ok: true }>} meta
 * @returns {string}
 */
function connectedText(origin, meta) {
  const version = meta.version === "" ? "of an unknown version" : `version ${meta.version}`;
  return `An Ogre server, ${version}, answers at ${origin}.`;
}

/**
 * What to say when Chrome did not grant access to a Directory.
 *
 * @param {string} origin
 */
function showAccessRefused(origin) {
  showNotice(
    result,
    "error",
    "Error: access not given",
    `The extension may not contact ${origin} until you allow it. Try again, and choose "Allow" when Chrome asks.`,
  );
}

/**
 * Hands the address to the service worker and reports what it did.
 *
 * @param {string} origin
 * @param {MetaResult | null} meta the connection test's result, or null when the user saved without one
 */
async function save(origin, meta) {
  const reply = await setDirectory(origin);
  if (!reply.ok) {
    showNotice(result, "error", "Error: not saved", reply.message);
    return;
  }
  current.textContent = reply.origin;
  field.value = reply.origin;
  const paragraphs = [`Go links now go to ${reply.origin}.`];
  if (meta?.ok) paragraphs.push(connectedText(reply.origin, meta));
  if (meta === null) paragraphs.push("No Ogre server answered there when you saved. Start it, then use Test connection.");
  if (reply.released.length > 0) {
    paragraphs.push(`Access the extension no longer needs was given up: ${reply.released.map((pattern) => pattern.replace(/\/\*$/, "")).join(", ")}.`);
  }
  showNotice(result, "success", "Saved", ...paragraphs);
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  const origin = readField();
  if (origin === null) {
    clearNotice(result);
    hideWarning();
    return;
  }
  const access = requestAccess(origin); // before any await: see the top of this file
  setBusy(true);
  hideWarning(origin);
  showNotice(result, "info", "Checking", `Asking ${origin} whether it is an Ogre server…`);
  void (async () => {
    try {
      if (!(await access)) {
        showAccessRefused(origin);
        return;
      }
      const meta = await fetchMeta(origin);
      if (!meta.ok) {
        // A well-formed address that does not answer can still be saved,
        // after a warning that says so (plan L4.2).
        clearNotice(result);
        warnedOrigin = origin;
        unreachableReason.textContent = meta.message;
        unreachable.hidden = false;
        focusBeforeBusy = byId("save-anyway", HTMLButtonElement);
        return;
      }
      await save(origin, meta);
    } catch (error) {
      showUnexpected(error);
    } finally {
      setBusy(false);
    }
  })();
});

byId("save-anyway", HTMLButtonElement).addEventListener("click", () => {
  const origin = readField();
  if (origin === null || origin !== warnedOrigin) {
    hideWarning();
    return;
  }
  const access = requestAccess(origin);
  setBusy(true);
  hideWarning();
  void (async () => {
    try {
      if (!(await access)) {
        showAccessRefused(origin);
        return;
      }
      await save(origin, null);
    } catch (error) {
      showUnexpected(error);
    } finally {
      // The warning and its buttons are gone: focus the field it was about.
      focusBeforeBusy = field;
      setBusy(false);
    }
  })();
});

byId("cancel-anyway", HTMLButtonElement).addEventListener("click", () => {
  hideWarning();
  field.focus();
});

byId("test", HTMLButtonElement).addEventListener("click", () => {
  const origin = readField();
  if (origin === null) {
    clearNotice(result);
    return;
  }
  // Without access, the browser would block the request and the test could
  // not tell a stopped server from a refused permission. The access granted
  // here is given up again when another address is saved, or, if none is,
  // when the browser next starts (background.js, releaseUnusedAccess).
  const access = requestAccess(origin);
  setBusy(true);
  hideWarning();
  showNotice(result, "info", "Checking", `Asking ${origin} whether it is an Ogre server…`);
  void (async () => {
    try {
      if (!(await access)) {
        showAccessRefused(origin);
        return;
      }
      const meta = await fetchMeta(origin);
      if (meta.ok) {
        const saved = current.textContent === origin;
        showNotice(result, "success", "Connected", connectedText(origin, meta), saved ? "This is the saved address." : "Save to send go links there.");
      } else {
        showNotice(result, "error", "Error: no Ogre server answers there", meta.message);
      }
    } catch (error) {
      showUnexpected(error);
    } finally {
      setBusy(false);
    }
  })();
});

byId("default", HTMLButtonElement).addEventListener("click", () => {
  field.value = DEFAULT_DIRECTORY_URL;
  readField();
  hideWarning();
  clearNotice(result);
  field.focus();
});

field.addEventListener("input", () => {
  // The marks and messages were about the text before this edit.
  if (field.getAttribute("aria-invalid") === "true") {
    field.removeAttribute("aria-invalid");
    fieldError.hidden = true;
  }
  hideWarning();
  clearNotice(result);
});

void (async () => {
  try {
    const directory = await loadDirectory();
    current.textContent = directory.origin;
    if (field.value === "") field.value = directory.origin;
    if (directory.invalid !== undefined) {
      showNotice(result, "warning", "Warning: stored address ignored", `The stored address is not valid, so go links go to ${directory.origin}. Save an address to replace it.`);
    } else if (needsOptionalAccess(directory.origin) && !(await chrome.permissions.contains({ origins: [hostPattern(directory.origin)] }))) {
      showNotice(result, "warning", "Warning: access withdrawn", `The extension may no longer contact ${directory.origin}. Go links still go there; save the address again to allow access, so Test connection works.`);
    }
  } catch (error) {
    showUnexpected(error);
  }
})();
