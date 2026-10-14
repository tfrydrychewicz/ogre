// @ts-check
/**
 * A notice that a finished action leaves for the next page: "Deleted
 * go/x." on the home page after a delete, "Saved." on the link page after a
 * save. The page that acts sets it just before it navigates; the page it
 * lands on shows it once.
 *
 * It lives in sessionStorage, which may be unavailable (a private window,
 * blocked site data); there is then simply no notice. The text is the
 * acting page's own sentence and is put in as text.
 *
 * @module
 */

import { el, replace } from "./dom.js";

/** The sessionStorage key of the notice. */
const FLASH_KEY = "ogre.flash";

/**
 * Leaves a notice for the next page. path is the page it is for, so a notice
 * is never shown on some other page the user opens first.
 *
 * @param {string} path the pathname of the page that shows it, as in location.pathname
 * @param {string} text
 */
export function setFlash(path, text) {
  try {
    sessionStorage.setItem(FLASH_KEY, JSON.stringify({ path, text }));
  } catch {
    // No notice then; the action itself succeeded.
  }
}

/**
 * Shows, once, the notice left for this page, inside container.
 *
 * @param {HTMLElement} container
 */
export function showFlash(container) {
  let text = "";
  try {
    const raw = sessionStorage.getItem(FLASH_KEY);
    if (raw === null) return;
    const flash = JSON.parse(raw);
    if (!flash || flash.path !== location.pathname || typeof flash.text !== "string") return;
    sessionStorage.removeItem(FLASH_KEY);
    text = flash.text;
  } catch {
    return;
  }
  if (text === "") return;
  replace(container, el("div", { class: "notice notice-success", role: "status" }, el("span", { class: "notice-title" }, "Done"), el("p", null, text)));
}
