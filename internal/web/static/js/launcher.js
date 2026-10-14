// @ts-check
/**
 * The launcher (VAR-5, plan L3.5): one field per variable of a route, the go
 * link being built, and the final URL or the reason it does not resolve,
 * with an Open button enabled only on a hit.
 *
 * It appears on the link page (link.js), where it resolves saved links
 * through GET /resolve (resolveSaved), and as the live preview of the create
 * and edit form, which supplies its own resolve for an unsaved draft.
 *
 * Nothing here reads a pattern (NFR-22). Where the variables stand comes from
 * the server as tokens (start and end offsets in UTF-16 code units, as in the
 * validate endpoint's answer): the launcher puts the typed values in place of
 * the variable tokens and leaves every other character of the pattern as it
 * is. Whether a value fits, and where the go link leads, is the server's
 * answer, shown as it comes.
 *
 * A value is judged as soon as it is typed (VAR-5), before the other fields
 * are filled in: the launcher asks the validate endpoint whether each filled
 * field's value would be accepted (its "samples"), and shows the server's
 * sentence for a refused one beside the field. Once every field has a value,
 * the go link itself is resolved.
 *
 * Usage:
 *
 *   const launcher = mountLauncher(root, { name: "reports", routes, resolve: resolveSaved });
 *   launcher.setRoutes(newRoutes); // after the form changed them
 *   launcher.focus();              // the first field, or the route chooser
 *
 * @module
 */

import { apiFetch, debounced, errorMessage, isAbort } from "./lib/api.js";
import { announce, el, replace, safeURL } from "./lib/dom.js";
import { patternNodes } from "./lib/pattern.js";

/**
 * A variable of a route, as the link JSON lists it. Only name is required;
 * the rest is shown when present.
 *
 * @typedef {object} LauncherVariable
 * @property {string} name
 * @property {string} [type] "string", "int", "number", "regex"
 * @property {string} [accepts] What the type accepts, in words.
 * @property {string} [description]
 * @property {string} [example] Shown as the field's placeholder ("e.g. 10").
 */

/**
 * One well-formed segment of a pattern, as the server reports it (the
 * validate endpoint's routes[i].tokens). Offsets count UTF-16 code units.
 *
 * @typedef {import("./lib/pattern.js").PatternToken} LauncherToken
 */

/**
 * A route as the launcher takes it: a route of the link JSON with the tokens
 * of its pattern, which the link page and the form both have from the
 * server.
 *
 * @typedef {object} LauncherRoute
 * @property {number} [id] The saved route's ID; absent for a draft route.
 * @property {string} pattern
 * @property {string} [destination]
 * @property {LauncherVariable[]} [variables] In pattern order. Without it the
 *   fields come from the variable tokens.
 * @property {LauncherToken[]} tokens
 */

/**
 * Why a route did not take the path; message is the server's sentence.
 *
 * @typedef {object} LauncherReason
 * @property {string} message
 * @property {string} [variable] The variable whose value was refused, if any.
 * @property {string} [value]
 * @property {string} [accepts]
 * @property {number} [routeId] The route the reason is about, when known.
 */

/**
 * What resolving a path gave.
 *
 *   - "hit": destination is the final URL; routeId and pattern say which
 *     route answered, when known.
 *   - "miss": reasons says why each route refused, or message says why
 *     there is nothing to match.
 *   - "invalid": the path cannot be read as a go link; message says so.
 *   - "error": no answer (network, server error); message says what failed.
 *
 * @typedef {object} LauncherOutcome
 * @property {"hit" | "miss" | "invalid" | "error"} kind
 * @property {string} [destination]
 * @property {LauncherReason[]} [reasons]
 * @property {string} [message]
 * @property {number} [routeId]
 * @property {string} [pattern]
 */

/**
 * Resolves a path, which is the go link after "go/" with each value encoded
 * by encodeURIComponent ("reports/10/13"). info says which route of the
 * launcher's list the user filled in; resolveSaved ignores it, a form's
 * draft preview may need it.
 *
 * @typedef {(path: string, signal: AbortSignal, info: { route: LauncherRoute, index: number }) => Promise<LauncherOutcome>} LauncherResolve
 */

/**
 * Judges the values typed so far, each on its own: by variable name, the
 * server's sentence for a refused value, or null for an accepted one. A
 * variable left out was not judged. null as a whole means no answer.
 *
 * @typedef {(route: LauncherRoute, values: Record<string, string>, signal: AbortSignal) => Promise<Record<string, string | null> | null>} LauncherCheck
 */

/**
 * @typedef {object} LauncherOptions
 * @property {string} [name] The link's name, used in messages. Optional:
 *   the go link shown is built from the pattern.
 * @property {LauncherRoute[]} routes
 * @property {LauncherResolve} [resolve] resolveSaved when omitted.
 * @property {LauncherCheck} [check] checkValues when omitted.
 * @property {boolean} [newTab] Open the destination in a new tab: the form's
 *   preview does, so trying a draft does not leave the unsaved form.
 */

/**
 * @typedef {object} Launcher
 * @property {(routes: LauncherRoute[]) => void} setRoutes Replaces the
 *   routes; values typed so far are kept by variable name, and the chosen
 *   route by position.
 * @property {() => void} focus Focuses the first field (or, without fields,
 *   the route chooser or the Open button).
 */

let mounted = 0;

/**
 * Builds the launcher inside root, replacing what root held (such as a
 * <noscript> fallback). root should be a block element; the launcher adds no
 * <form>, so it may sit inside the create form's own.
 *
 * @param {HTMLElement} root
 * @param {LauncherOptions} options
 * @returns {Launcher}
 */
export function mountLauncher(root, { name = "", routes, resolve = resolveSaved, check = checkValues, newTab = false }) {
  const prefix = `launcher-${++mounted}`;
  /** @type {LauncherRoute[]} */
  let current = [];
  let chosen = 0;
  /** @type {Map<string, string>} the typed values, by variable name */
  const values = new Map();
  /** @type {{ path: string, outcome: LauncherOutcome } | null} the newest answer */
  let answer = null;
  /** @type {HTMLInputElement[]} */
  let inputs = [];

  root.classList.add("launcher");
  const chooser = el("div", { class: "launcher-chooser" });
  const fields = el("div", { class: "launcher-fields" });
  const built = el("p", { class: "launcher-built" });
  const result = el("div", { class: "launcher-result", "aria-live": "polite" });
  const open = el("button", { class: "button button-primary launcher-open", type: "button", "aria-disabled": "true" }, "Open");
  const note = el("p", { class: "launcher-note field-help", id: `${prefix}-note` });
  open.setAttribute("aria-describedby", note.id);
  replace(root, chooser, fields, el("div", { class: "launcher-output" }, built, result), el("div", { class: "launcher-actions" }, open, note));

  const lookup = debounced((signal, /** @type {string} */ path, /** @type {number} */ index) =>
    resolve(path, signal, { route: current[index], index }),
  );
  const verify = debounced((signal, /** @type {Record<string, string>} */ filled, /** @type {number} */ index) =>
    check(current[index], filled, signal),
  );

  open.addEventListener("click", () => {
    if (open.getAttribute("aria-disabled") === "true") {
      announce(note.textContent || "Fill in the fields first.");
      return;
    }
    void go();
  });

  /** The route being filled in. */
  const route = () => current[chosen];

  /**
   * The variables of a route: its own list, or else its variable tokens.
   *
   * @param {LauncherRoute} r
   * @returns {LauncherVariable[]}
   */
  const variablesOf = (r) => {
    if (r.variables && r.variables.length > 0) return r.variables;
    return tokensOf(r)
      .filter((t) => t.kind === "variable" && t.name)
      .map((t) => ({ name: t.name || "", type: t.type }));
  };

  const render = () => {
    const r = route();
    renderChooser();
    if (!r) {
      replace(fields, el("p", { class: "muted" }, name ? `go/${name} has no routes to open.` : "There are no routes to open."));
      replace(built);
      replace(result);
      inputs = [];
      setOpen(false, "");
      return;
    }
    renderFields(variablesOf(r));
    void update();
    // Tests and scripts wait for this instead of for the network to settle.
    root.dataset.ready = "true";
  };

  const focusFirst = () => {
    const target = inputs[0] || chooser.querySelector("input:checked") || chooser.querySelector("input") || open;
    if (target instanceof HTMLElement) target.focus();
  };

  const renderChooser = () => {
    if (current.length < 2) {
      replace(chooser);
      return;
    }
    const group = `${prefix}-route`;
    const fieldset = el(
      "fieldset",
      { class: "launcher-routes" },
      el("legend", null, "Route"),
      current.map((r, i) =>
        el(
          "label",
          { class: "check launcher-route" },
          el("input", {
            type: "radio",
            name: group,
            value: String(i),
            checked: i === chosen,
            on: {
              change: () => {
                chosen = i;
                answer = null;
                render();
              },
            },
          }),
          el("bdi", { class: "pattern" }, patternNodes(r.pattern, tokensOf(r))),
        ),
      ),
    );
    replace(chooser, fieldset);
  };

  /** @param {LauncherVariable[]} variables */
  const renderFields = (variables) => {
    inputs = [];
    if (variables.length === 0) {
      replace(fields, el("p", { class: "field-help" }, "This route takes no values."));
      return;
    }
    replace(
      fields,
      variables.map((v, i) => {
        const id = `${prefix}-v${i}`;
        const help = [v.description, v.accepts ? `Accepts ${v.accepts}` : ""].filter(Boolean).join(" · ");
        const input = el("input", {
          class: "input",
          id,
          type: "text",
          name: v.name,
          value: values.get(v.name) || "",
          // "e.g." keeps the example from reading as a value already typed.
          placeholder: v.example ? `e.g. ${v.example}` : null,
          autocomplete: "off",
          autocapitalize: "none",
          spellcheck: "false",
          enterkeyhint: "go",
          "aria-describedby": `${id}-help ${id}-error`,
          on: {
            input: () => {
              values.set(v.name, input.value);
              update();
            },
            keydown: (event) => {
              if (/** @type {KeyboardEvent} */ (event).key === "Enter") {
                event.preventDefault();
                void go();
              }
            },
          },
        });
        input.dataset.variable = v.name;
        inputs.push(input);
        return el(
          "div",
          { class: "field launcher-field" },
          // The field is named by its variable alone; what it accepts is in
          // its help, in words.
          el("label", { for: id }, v.name),
          input,
          el("p", { class: "field-help", id: `${id}-help` }, help),
          el("p", { class: "field-error", id: `${id}-error`, hidden: true }),
        );
      }),
    );
  };

  /**
   * The path of the chosen route with the typed values, encoded for
   * resolving, or null while a value is missing. display is the same go link
   * as text for people, with each missing value shown by its name.
   *
   * @returns {{ path: string | null, display: (string | Node)[], missing: string[] }}
   */
  const compose = () => {
    const r = route();
    const tokens = r ? tokensOf(r) : [];
    /** @type {string[]} */
    const path = [];
    /** @type {(string | Node)[]} */
    const display = ["go/"];
    /** @type {string[]} */
    const missing = [];
    let at = 0;
    for (const t of tokens) {
      const between = r.pattern.slice(at, t.start);
      path.push(between);
      display.push(between);
      if (t.kind === "variable") {
        const value = values.get(t.name || "") || "";
        if (value === "") {
          missing.push(t.name || "");
          display.push(el("span", { class: "pattern-var" }, t.name || ""));
        } else {
          path.push(encodeURIComponent(value));
          display.push(el("span", { class: "launcher-value" }, displayValue(value)));
        }
      } else {
        const literal = r.pattern.slice(t.start, t.end);
        path.push(literal);
        display.push(literal);
      }
      at = t.end;
    }
    const tail = r ? r.pattern.slice(at) : "";
    path.push(tail);
    display.push(tail);
    return { path: missing.length > 0 ? null : path.join(""), display, missing };
  };

  const update = async () => {
    const { path, display, missing } = compose();
    replace(built, el("span", { class: "launcher-label" }, "Go link"), " ", el("span", { class: "golink" }, display));
    if (path === null) {
      lookup.cancel();
      answer = null;
      replace(result);
      setOpen(false, `Fill in ${listOf(missing)} to open it.`);
      await judgeFilled();
      return;
    }
    verify.cancel();
    if (answer && answer.path === path) {
      show(answer.outcome);
      return;
    }
    root.setAttribute("aria-busy", "true");
    let outcome;
    try {
      const reply = await lookup(path, chosen);
      if (!reply.current) return;
      outcome = reply.value;
    } catch (err) {
      if (isAbort(err)) return;
      outcome = /** @type {LauncherOutcome} */ ({ kind: "error", message: "Could not reach the Directory. Check that it is running." });
    } finally {
      root.removeAttribute("aria-busy");
    }
    answer = { path, outcome };
    show(outcome);
  };

  /**
   * Judges each value typed so far on its own, while others are still
   * missing, and marks a refused one with the server's sentence (VAR-5).
   * A field whose value changed since it was sent keeps its marks until its
   * own answer arrives.
   */
  const judgeFilled = async () => {
    /** @type {Record<string, string>} */
    const filled = {};
    for (const input of inputs) {
      if (input.value !== "") filled[input.dataset.variable || ""] = input.value;
    }
    if (Object.keys(filled).length === 0) {
      verify.cancel();
      clearFieldErrors();
      return;
    }
    let verdicts;
    try {
      const reply = await verify(filled, chosen);
      if (!reply.current) return;
      verdicts = reply.value;
    } catch {
      return; // no answer: nothing is marked, and resolving says the rest
    }
    if (!verdicts) return;
    for (const input of inputs) {
      const variable = input.dataset.variable || "";
      if (input.value === "") {
        markField(input, null);
      } else if (variable in verdicts && filled[variable] === input.value) {
        markField(input, verdicts[variable]);
      }
    }
  };

  /**
   * Open pressed or Enter typed: open on a hit. When the answer for what is
   * typed now has not arrived yet, it is asked for at once.
   */
  const go = async () => {
    const { path, missing } = compose();
    if (path === null) {
      const first = inputs.find((input) => input.value === "");
      first?.focus();
      announce(`Fill in ${listOf(missing)} first.`);
      return;
    }
    let outcome = answer && answer.path === path ? answer.outcome : null;
    if (!outcome) {
      lookup.cancel();
      const controller = new AbortController();
      try {
        outcome = await resolve(path, controller.signal, { route: route(), index: chosen });
      } catch (err) {
        if (isAbort(err)) return;
        outcome = { kind: "error", message: "Could not reach the Directory. Check that it is running." };
      }
      answer = { path, outcome };
      show(outcome);
    }
    if (outcome.kind === "hit" && outcome.destination && isWebURL(outcome.destination)) {
      if (newTab) window.open(outcome.destination, "_blank", "noopener");
      else window.location.assign(outcome.destination);
    } else {
      announce(note.textContent || "This go link does not open anything.");
    }
  };

  /** @param {LauncherOutcome} outcome */
  const show = (outcome) => {
    clearFieldErrors();
    const r = route();
    if (outcome.kind === "hit" && outcome.destination && isWebURL(outcome.destination)) {
      const other =
        r && r.id !== undefined && outcome.routeId !== undefined && outcome.routeId !== r.id && outcome.pattern
          ? el("p", { class: "field-help" }, "This go link is answered by the route ", el("bdi", { class: "pattern" }, "go/", outcome.pattern), ".")
          : null;
      replace(result, el("p", { class: "launcher-destination" }, el("span", { class: "launcher-label" }, "Opens"), " ", el("bdi", { class: "url" }, outcome.destination)), other);
      setOpen(true, newTab ? "Opens in a new tab." : "Opens in this tab.");
      return;
    }
    if (outcome.kind === "hit") {
      // A hit without a usable URL never comes from the server; refuse it
      // all the same rather than navigate to it.
      replace(result, el("p", { class: "field-error" }, "This destination can't be opened from here."));
      setOpen(false, "");
      return;
    }
    const reasons = outcome.reasons || [];
    const reason =
      (r && r.id !== undefined && reasons.find((x) => x.routeId === r.id)) || reasons[0] || null;
    let message = reason ? reason.message : outcome.message || "This go link does not open anything.";
    if (reason && reason.variable) {
      const input = inputs.find((i) => i.dataset.variable === reason.variable);
      if (input) {
        // The server's sentence goes beside the field it is about; the
        // summary below only points there, so it is not read twice.
        message = `The value of ${reason.variable} is not accepted; see the field above.`;
        markField(input, reason.message);
      }
    }
    const title = outcome.kind === "error" ? "Not checked" : "Doesn't open";
    replace(
      result,
      el(
        "div",
        { class: outcome.kind === "error" ? "notice notice-warning launcher-reason" : "notice notice-error launcher-reason" },
        el("span", { class: "notice-title" }, title),
        el("p", null, message),
      ),
    );
    setOpen(false, outcome.kind === "error" ? "" : "Change the values to open it.");
  };

  const clearFieldErrors = () => {
    for (const input of inputs) markField(input, null);
  };

  /**
   * Marks a field invalid with the server's sentence, or clears it (null).
   *
   * @param {HTMLInputElement} input
   * @param {string | null} message
   */
  const markField = (input, message) => {
    if (message) input.setAttribute("aria-invalid", "true");
    else input.removeAttribute("aria-invalid");
    const error = document.getElementById(`${input.id}-error`);
    if (error) {
      replace(error, message || "");
      error.hidden = !message;
    }
  };

  /**
   * @param {boolean} enabled
   * @param {string} why
   */
  const setOpen = (enabled, why) => {
    open.setAttribute("aria-disabled", enabled ? "false" : "true");
    replace(note, why);
  };

  const setRoutes = (/** @type {LauncherRoute[]} */ routes) => {
    current = Array.isArray(routes) ? routes.slice() : [];
    if (chosen >= current.length) chosen = 0;
    answer = null;
    lookup.cancel();
    verify.cancel();
    render();
  };

  setRoutes(routes);

  return { setRoutes, focus: focusFirst };
}

/**
 * The tokens of a route; none when the route came without them, which
 * leaves its pattern plain and its fields taken from its variables.
 *
 * @param {LauncherRoute} route
 * @returns {LauncherToken[]}
 */
function tokensOf(route) {
  return Array.isArray(route.tokens) ? route.tokens : [];
}

/**
 * Judges values on their own with the validate endpoint's "samples": the
 * route's pattern and destination, as the server reads them, decide whether
 * each value would be accepted (the type, and the rule against "." and ".."
 * in a destination's path), and "sampleReasons" gives the sentence a miss
 * page would show, in the words of the route's notes on its variables.
 *
 * @type {LauncherCheck}
 */
export async function checkValues(route, values, signal) {
  /** @type {Record<string, { description: string, example: string }>} */
  const variables = {};
  for (const v of route.variables || []) {
    if (v.description || v.example) variables[v.name] = { description: v.description || "", example: v.example || "" };
  }
  /** @type {Record<string, string[]>} */
  const samples = {};
  for (const [name, value] of Object.entries(values)) samples[name] = [value];
  const response = await apiFetch("links/validate", {
    method: "POST",
    body: { explainSamples: true, routes: [{ pattern: route.pattern, destination: route.destination || "", variables, samples }] },
    signal,
  });
  const answer = response.status === 200 && response.data && Array.isArray(response.data.routes) ? response.data.routes[0] : null;
  if (!answer || !answer.samples) return null;
  /** @type {Record<string, string | null>} */
  const out = {};
  for (const [name, value] of Object.entries(values)) {
    const verdict = answer.samples[name] ? answer.samples[name][value] : undefined;
    if (verdict === true) out[name] = null;
    if (verdict === false) {
      const reason = answer.sampleReasons && answer.sampleReasons[name] ? answer.sampleReasons[name][value] : undefined;
      out[name] = typeof reason === "string" && reason !== "" ? reason : `${name} doesn't accept this value.`;
    }
  }
  return out;
}

/**
 * Resolves a saved go link with GET /-/api/v1/resolve?path= and turns the
 * answer into a LauncherOutcome. The sentences shown come from the server
 * (reason messages, error messages) wherever it sends one.
 *
 * @param {string} path
 * @param {AbortSignal} signal
 * @returns {Promise<LauncherOutcome>}
 */
export async function resolveSaved(path, signal) {
  const response = await apiFetch(`resolve?path=${encodeURIComponent(path)}`, { signal });
  const data = response.data;
  if (response.status !== 200 || !data || typeof data.kind !== "string") {
    return { kind: "error", message: errorMessage(response, "The Directory could not check this go link.") };
  }
  switch (data.kind) {
    case "hit":
      return {
        kind: "hit",
        destination: String(data.destination || ""),
        routeId: data.route && data.route.id,
        pattern: data.route && data.route.pattern,
      };
    case "miss_known":
      return {
        kind: "miss",
        reasons: (Array.isArray(data.routes) ? data.routes : []).map(
          /** @param {any} r */ (r) => ({
            routeId: r.id,
            variable: (r.reason && r.reason.variable) || "",
            value: (r.reason && r.reason.value) || "",
            accepts: (r.reason && r.reason.accepts) || "",
            message: (r.reason && r.reason.message) || "This route does not take this go link.",
          }),
        ),
      };
    case "miss_unknown":
      return { kind: "miss", message: "There is no link with this name any more. It may have been deleted or renamed." };
    default:
      return { kind: "invalid", message: "This can't be read as a go link." };
  }
}

/**
 * A value as people type it into a go link: as it is, except for the
 * characters that would change the address's structure ("/", "?", "#", "%")
 * and white space, which are shown escaped, as they are sent.
 *
 * @param {string} value
 * @returns {string}
 */
function displayValue(value) {
  return value.replace(/[/?#%\s]/g, (c) => encodeURIComponent(c));
}

/**
 * Whether a destination may be navigated to: an absolute http or https URL.
 * The server allows nothing else; this is the second line.
 *
 * @param {string} url
 * @returns {boolean}
 */
function isWebURL(url) {
  return /^https?:\/\//i.test(url) && safeURL(url);
}

/**
 * "month", "month and day", "month, day and year".
 *
 * @param {string[]} names
 * @returns {string}
 */
function listOf(names) {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}
