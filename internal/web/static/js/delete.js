// @ts-check
/**
 * The delete confirmation (templates/delete.html, plan L3.7).
 *
 * The button sends DELETE /-/api/v1/links/{id}?version=N with the version
 * the page was rendered with, so a link someone changed in the meantime is
 * not deleted unseen: the server answers 409 and the page offers to reload.
 * On success it leaves a notice for the home page and goes there.
 *
 * @module
 */

import { apiFetch, errorMessage } from "./lib/api.js";
import { byId, el, replace } from "./lib/dom.js";
import { setFlash } from "./lib/flash.js";

const button = byId("delete-confirm", HTMLButtonElement);
const message = byId("delete-message", HTMLElement);
const { linkId = "", version = "", name = "" } = button.dataset;
const label = button.textContent || "Delete";

button.hidden = false;
button.addEventListener("click", () => void remove());

async function remove() {
  button.disabled = true;
  button.textContent = "Deleting…";
  replace(message);
  let response;
  try {
    response = await apiFetch(`/-/api/v1/links/${encodeURIComponent(linkId)}?version=${encodeURIComponent(version)}`, {
      method: "DELETE",
    });
  } catch {
    fail("Not deleted", "The Directory could not be reached. Check that it is running, then try again.");
    return;
  }
  switch (response.status) {
    case 204:
      setFlash("/-/", `Deleted go/${name}.`);
      window.location.assign("/-/");
      return;
    case 409:
      fail(
        "Changed since you opened this page",
        `Someone changed go/${name} after this page was loaded. Reload to see the link as it is now, then decide again.`,
        el("button", { class: "button", type: "button", on: { click: () => window.location.reload() } }, "Reload"),
      );
      button.hidden = true;
      return;
    case 404:
      fail("Already gone", `go/${name} no longer exists; it may have been deleted already.`, el("a", { class: "button", href: "/-/" }, "Directory home"));
      button.hidden = true;
      return;
    default:
      fail("Not deleted", errorMessage(response));
  }
}

/**
 * Shows why the link was not deleted, and gives the button back.
 *
 * @param {string} title
 * @param {string} text
 * @param {Node} [action]
 */
function fail(title, text, action) {
  button.disabled = false;
  button.textContent = label;
  replace(
    message,
    el(
      "div",
      { class: "notice notice-error", role: "alert" },
      el("span", { class: "notice-title" }, title),
      el("p", null, text),
      action ? el("p", null, action) : null,
    ),
  );
}
