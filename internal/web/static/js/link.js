// @ts-check
/**
 * A link's page (templates/link.html): the launcher, the copy buttons, and
 * the notice a save left for this page. The rest of the page is the
 * server's and needs no script.
 *
 * @module
 */

import { wireCopyButtons } from "./lib/copy.js";
import { byId } from "./lib/dom.js";
import { showFlash } from "./lib/flash.js";
import { mountLauncher, resolveSaved } from "./launcher.js";

wireCopyButtons(document.body);
showFlash(byId("flash", HTMLElement));

const root = byId("launcher", HTMLElement);
/** @type {import("./launcher.js").LauncherRoute[]} */
let routes = [];
try {
  routes = JSON.parse(root.dataset.routes || "[]");
} catch {
  routes = [];
}
const launcher = mountLauncher(root, { name: root.dataset.name || "", routes, resolve: resolveSaved });

// Search sends a link with variables here as /-/links/<name>#launch: the
// user came to fill in the values, so the first field takes focus. Not at
// once: navigating to a fragment runs the browser's own focusing steps for
// it around the load event, which would take focus straight back. The
// fragment stays in the address, so the page also scrolls to the launcher.
if (location.hash === "#launch") {
  const focus = () => setTimeout(() => launcher.focus(), 0);
  if (document.readyState === "complete") focus();
  else window.addEventListener("load", focus, { once: true });
}
