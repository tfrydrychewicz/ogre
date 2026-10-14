// @ts-check
/**
 * Search as you type (SRCH-1, SRCH-4, plan L3.3).
 *
 * The page renders the search form and, for /-/?q=, the ranked results
 * itself, so search works without JavaScript. This module takes the same
 * form over: each keystroke asks GET /-/api/v1/links?q= (debounced, the
 * previous call aborted, late answers dropped) and renders the results with
 * the same markup as templates/home.html's "link-item"; keep the two alike.
 *
 * Keyboard: from the search box, Down moves to the first result and Enter
 * opens the first one; in the results, Up and Down move, Enter opens (it is
 * a link), Escape returns to the box, and typing goes back to the box. A
 * result opens its go link at once when the link takes no values, and its
 * page with the launcher focused otherwise.
 *
 * The ranking is the server's; the order of the answer is kept as it is.
 *
 * @module
 */

import { apiFetch, debounced, errorMessage, isAbort } from "./lib/api.js";
import { announce, el, replace } from "./lib/dom.js";
import { patternNodes } from "./lib/pattern.js";

/**
 * A whole number the server wrote into a data attribute, or the fallback
 * when it is missing or broken. The server's numbers (how many results the
 * page shows, how many routes a result lists) are read, not copied here, so
 * results that arrive as the user types are cut exactly like the page's own.
 *
 * @param {string | undefined} value
 * @param {number} fallback
 */
function count(value, fallback) {
  const n = Number(value);
  return Number.isInteger(n) && n > 0 ? n : fallback;
}

/**
 * @typedef {object} SearchElements
 * @property {HTMLFormElement} form
 * @property {HTMLInputElement} input
 * @property {HTMLElement} section The results' section, hidden without a query.
 * @property {HTMLElement} status A role="status" line: how many results.
 * @property {HTMLElement} results The <ol> of results.
 * @property {HTMLElement} [create] Shown when nothing matches.
 * @property {HTMLElement} [createLink] The link inside create; it is pointed
 *   at the create form prefilled with the query.
 * @property {HTMLElement} [lists] What the page shows without a query; hidden during a search.
 */

/**
 * Takes over the search form of the home page.
 *
 * @param {SearchElements} elements
 */
export function mountSearch({ form, input, section, status, results, create, createLink, lists }) {
  const limit = count(form.dataset.limit, 20);
  const routesShown = count(form.dataset.routesShown, 3);
  /** The query the shown results are for; null while they are not current. */
  let shownFor = input.value.trim() === "" ? null : input.value.trim();

  // One more than is shown, to tell "exactly the limit matched" from "more
  // matched than are shown", as the server's page does.
  const search = debounced(async (signal, /** @type {string} */ q) =>
    apiFetch(`links?q=${encodeURIComponent(q)}&limit=${limit + 1}`, { signal }),
  );

  const showLists = (/** @type {boolean} */ searching) => {
    section.hidden = !searching;
    if (lists) lists.hidden = searching;
  };

  const run = async () => {
    const q = input.value.trim();
    rememberQuery(q);
    if (q === "") {
      search.cancel();
      shownFor = null;
      replace(results);
      replace(status);
      if (create) create.hidden = true;
      showLists(false);
      return;
    }
    shownFor = null;
    section.setAttribute("aria-busy", "true");
    try {
      const reply = await search(q);
      if (!reply.current) return;
      const response = reply.value;
      if (response.status !== 200 || !response.data || !Array.isArray(response.data.links)) {
        replace(results);
        replace(status, errorMessage(response, "Search failed. Try again."));
        if (create) create.hidden = true;
      } else {
        render(response.data.links, q);
        shownFor = q;
      }
      showLists(true);
    } catch (err) {
      if (isAbort(err)) return;
      replace(status, "Search failed: the Directory could not be reached.");
      showLists(true);
    } finally {
      section.removeAttribute("aria-busy");
    }
  };

  /**
   * @param {any[]} links
   * @param {string} q
   */
  const render = (links, q) => {
    const capped = links.length > limit;
    const shown = links.slice(0, limit);
    replace(results, shown.map((link) => resultItem(link, routesShown)));
    const n = shown.length;
    replace(
      status,
      n === 0
        ? "No link matches your search."
        : `${n} ${n === 1 ? "link matches" : "links match"}${capped ? `; showing the best ${n}` : ""}.`,
    );
    if (create) create.hidden = n !== 0;
    if (createLink) createLink.setAttribute("href", `/-/new?pattern=${encodeURIComponent(q)}`);
  };

  input.addEventListener("input", () => void run());

  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown") {
      const first = resultLinks()[0];
      if (first) {
        event.preventDefault();
        first.focus();
      }
    } else if (event.key === "Escape" && input.value !== "") {
      event.preventDefault();
      input.value = "";
      void run();
    }
  });

  // Enter in the box: open the first result when the results on screen are
  // for what is typed; otherwise let the form submit, and the server answer.
  form.addEventListener("submit", (event) => {
    const q = input.value.trim();
    const first = resultLinks()[0];
    if (q !== "" && shownFor === q && first) {
      event.preventDefault();
      announce(`Opening ${first.textContent || "the first result"}`);
      window.location.assign(first.href);
    }
  });

  results.addEventListener("keydown", (event) => {
    const links = resultLinks();
    const at = links.indexOf(/** @type {HTMLAnchorElement} */ (document.activeElement));
    if (at === -1) return;
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        links[Math.min(at + 1, links.length - 1)].focus();
        break;
      case "ArrowUp":
        event.preventDefault();
        if (at === 0) input.focus();
        else links[at - 1].focus();
        break;
      case "Home":
        event.preventDefault();
        links[0].focus();
        break;
      case "End":
        event.preventDefault();
        links[links.length - 1].focus();
        break;
      case "Escape":
        event.preventDefault();
        input.focus();
        break;
      default:
        // A character typed on a result belongs in the search box: moving
        // focus during keydown sends the character there.
        if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) input.focus();
    }
  });

  /** @returns {HTMLAnchorElement[]} */
  const resultLinks = () =>
    [...results.querySelectorAll("a.result-link")].filter((a) => a instanceof HTMLAnchorElement);
}

/**
 * One result, as templates/home.html's "link-item" renders it.
 *
 * @param {any} link A link of the API, with "rank".
 * @param {number} routesShown How many routes to list; the rest are counted.
 * @returns {HTMLLIElement}
 */
function resultItem(link, routesShown) {
  const name = String(link.name || "");
  const routes = Array.isArray(link.routes) ? link.routes : [];
  const direct = routes.every(/** @param {any} r */ (r) => !Array.isArray(r.variables) || r.variables.length === 0);
  const href = direct ? `/${encodeURIComponent(name)}` : `/-/links/${encodeURIComponent(name)}#launch`;
  const shown = routes.slice(0, routesShown);
  const more = routes.length - shown.length;
  const owner = link.owner && link.owner.displayName ? String(link.owner.displayName) : "";

  return el(
    "li",
    { class: "result" },
    el(
      "div",
      { class: "result-head" },
      el("a", { class: "result-link golink", href, "data-direct": direct ? "true" : null }, "go/", name),
      link.rank === "misspelled" ? el("span", { class: "badge" }, "Similar name") : null,
      direct ? null : el("span", { class: "badge badge-accent" }, "Takes values"),
    ),
    link.description ? el("p", { class: "result-desc" }, el("bdi", null, String(link.description))) : null,
    el(
      "ul",
      { class: "result-routes list-plain" },
      shown.map(/** @param {any} r */ (r) => {
        const host = hostOf(String(r.destination || ""));
        return el(
          "li",
          null,
          el("bdi", { class: "pattern" }, patternNodes(String(r.pattern || ""), r.tokens)),
          host
            ? [" ", el("span", { class: "result-host" }, el("span", { "aria-hidden": "true" }, "→"), el("span", { class: "visually-hidden" }, "opens"), " ", el("bdi", null, host))]
            : null,
        );
      }),
      more > 0 ? el("li", { class: "muted small" }, `and ${more} more ${more === 1 ? "route" : "routes"}`) : null,
    ),
    el(
      "p",
      { class: "result-meta" },
      owner ? el("span", null, "Owner: ", el("bdi", null, owner)) : null,
      direct
        ? [" ", el("a", { class: "result-details", href: `/-/links/${encodeURIComponent(name)}` }, "Details", el("span", { class: "visually-hidden" }, ` of go/${name}`))]
        : null,
    ),
  );
}

/**
 * The host of a destination template, for a short summary; "" when it cannot
 * be read. A placeholder never stands in the host (the server's rule), so
 * the template parses as a URL.
 *
 * @param {string} destination
 * @returns {string}
 */
function hostOf(destination) {
  try {
    return new URL(destination).host;
  } catch {
    return "";
  }
}

/**
 * Keeps the address in step with the query, so reloading or sharing the page
 * shows the same search. Replaces the entry: typing is not navigation.
 *
 * @param {string} q
 */
function rememberQuery(q) {
  try {
    const url = q === "" ? "/-/" : `/-/?q=${encodeURIComponent(q)}`;
    history.replaceState(history.state, "", url);
  } catch {
    // Not worth failing a search over.
  }
}
