// @ts-check
/**
 * The home page (templates/home.html): search as you type, and the notice a
 * finished action left for this page (such as "Deleted go/x").
 *
 * @module
 */

import { showFlash } from "./lib/flash.js";
import { byId } from "./lib/dom.js";
import { mountSearch } from "./search.js";

mountSearch({
  form: byId("home-search", HTMLFormElement),
  input: byId("q", HTMLInputElement),
  section: byId("results-section", HTMLElement),
  status: byId("search-status", HTMLElement),
  results: byId("results", HTMLElement),
  create: document.getElementById("search-create") || undefined,
  createLink: document.getElementById("search-create-link") || undefined,
  lists: document.getElementById("home-lists") || undefined,
});

for (const hint of document.querySelectorAll(".js-only")) {
  if (hint instanceof HTMLElement) hint.hidden = false;
}

showFlash(byId("flash", HTMLElement));
