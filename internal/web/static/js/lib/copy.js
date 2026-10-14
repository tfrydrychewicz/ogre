// @ts-check
/**
 * Copy buttons: <button type="button" data-copy="go/roadmap" hidden>Copy go link</button>.
 *
 * The text to copy is in data-copy, written by the server's template (so it
 * is escaped there) or set with setAttribute. Render such buttons with the
 * hidden attribute: they do nothing without JavaScript, so wireCopyButtons
 * reveals them.
 *
 * @module
 */

import { announce } from "./dom.js";

/** How long a button says "Copied", in milliseconds. */
const CONFIRM_MS = 1500;

/**
 * Wires every button[data-copy] inside root, and root itself if it is one.
 * Calling it again on the same buttons is harmless: each is wired once.
 *
 * @param {ParentNode & Node} root
 */
export function wireCopyButtons(root) {
  /** @type {HTMLButtonElement[]} */
  const buttons = [...root.querySelectorAll("button[data-copy]")].filter((b) => b instanceof HTMLButtonElement);
  if (root instanceof HTMLButtonElement && root.hasAttribute("data-copy")) buttons.push(root);
  for (const button of buttons) {
    if (button.dataset.copyWired) continue;
    button.dataset.copyWired = "1";
    button.hidden = false;
    button.addEventListener("click", () => copyFrom(button));
  }
}

/**
 * @param {HTMLButtonElement} button
 */
async function copyFrom(button) {
  const text = button.dataset.copy || "";
  const ok = await copyText(text);
  const label = button.dataset.copyLabel || button.textContent || "";
  button.dataset.copyLabel = label;
  button.textContent = ok ? "Copied" : "Copy failed";
  announce(ok ? `Copied ${text}` : `Could not copy ${text}`);
  clearTimeout(Number(button.dataset.copyTimer));
  button.dataset.copyTimer = String(
    setTimeout(() => {
      button.textContent = label;
      delete button.dataset.copyLabel;
    }, CONFIRM_MS),
  );
}

/**
 * Puts text on the clipboard. The asynchronous clipboard API exists only
 * in a secure context, which http://localhost is and http://go/ is not; the
 * fallback selects the text in an off-screen field and copies that.
 *
 * @param {string} text
 * @returns {Promise<boolean>}
 */
export async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Denied: fall through to the older way.
    }
  }
  const field = document.createElement("textarea");
  field.value = text;
  field.setAttribute("readonly", "");
  field.className = "visually-hidden";
  const focused = document.activeElement;
  document.body.appendChild(field);
  field.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  field.remove();
  if (focused instanceof HTMLElement) focused.focus();
  return ok;
}
