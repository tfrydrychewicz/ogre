// @ts-check
/**
 * The create and edit form (plan L3.6; DIR-1 to DIR-4, DIR-7, VAR-2 to
 * VAR-5).
 *
 * The page (templates/form.html) carries the draft to start from in
 * data-initial, as base64-encoded JSON. This module builds a row per route, and on every change asks
 * POST /-/api/v1/links/validate about the whole draft (debounced by 200 ms,
 * the previous call aborted, out-of-order answers ignored). It then shows
 * only what the answer says: the tokens of each pattern and the place of
 * each error, every problem beside the field it concerns, whether the name
 * is free, the variables of each route, and whether sample values pass. The
 * launcher (launcher.js) previews the unsaved draft through the same
 * endpoint's "preview".
 *
 * The one thing written here rather than read from the server is the text of
 * a variable chosen in the variables table, "{name:type}", which replaces
 * that variable's span (as the server reported it) in the pattern; the
 * server then checks the result like anything typed (VAR-3).
 *
 * Renaming (DIR-3): an edit of the first route's name, the first token the
 * server reported for its pattern, is made to every other route whose
 * pattern starts with the same name, so the link is renamed as a whole and
 * not route by route.
 *
 * Saving is POST /-/api/v1/links, or PATCH /-/api/v1/links/{id} with the
 * version the form was opened at.
 *
 * @module
 */

import { apiFetch, debounced, errorMessage, isAbort } from "./lib/api.js";
import { announce, append, byId, clear, el, replace, setAttr } from "./lib/dom.js";
import { setFlash } from "./lib/flash.js";
import { mountLauncher } from "./launcher.js";
import { patternEditor } from "./form/pattern.js";
import { diff, fits, moveSpans } from "./form/spans.js";
import { messageItem, sampleValues, variablesTable } from "./form/variables.js";

/**
 * The sessionStorage key under which "Add this route to go/x" (DIR-4) hands
 * the routes it carries, with their notes on variables, to the edit form of
 * go/x: the address carries only patterns and destinations.
 */
const APPEND_KEY = "ogre.append";

/** @typedef {import("./form/pattern.js").Token} Token */
/** @typedef {import("./form/pattern.js").Problem} Problem */
/** @typedef {import("./form/variables.js").TableVariable} TableVariable */
/** @typedef {import("./launcher.js").LauncherRoute} LauncherRoute */
/** @typedef {import("./launcher.js").LauncherOutcome} LauncherOutcome */

/**
 * A variable of the validate answer (with describeVariables).
 *
 * @typedef {{ name: string, type: string, accepts: string, expression: string, start: number, end: number }} VariableCheck
 */

/**
 * One route of the validate answer.
 *
 * @typedef {{ tokens: Token[], problems: Problem[], samples: Record<string, Record<string, boolean>>, sampleReasons?: Record<string, Record<string, string>>, variables?: VariableCheck[], expressions?: Record<string, { valid: boolean, message?: string }> }} RouteCheck
 */

/**
 * The validate answer.
 *
 * @typedef {object} ValidateAnswer
 * @property {boolean} valid
 * @property {{ value: string, available: boolean, existing: { id: number, name: string } | null, canEdit: boolean }} name
 * @property {RouteCheck[]} routes
 * @property {Problem[]} problems
 * @property {any} [preview]
 */

/**
 * A route of the draft the page starts from (httpd.formRouteJSON).
 *
 * @typedef {{ id: number, pattern: string, destination: string, variables: Record<string, { description: string, example: string }> }} InitialRoute
 */

/**
 * The draft the page starts from (httpd.formDraftJSON).
 *
 * @typedef {{ linkId: number, version: number, name: string, description: string, routes: InitialRoute[], appended: number }} InitialDraft
 */

/**
 * A variable the table changed in the pattern, kept until the server reads
 * it: while an expression is being typed the pattern may not be readable,
 * and the row must not vanish under the user's hands.
 *
 * @typedef {{ name: string, type: string, expression: string, start: number, end: number, text: string }} LocalVariable
 */

/**
 * What the form knows about one route's pattern, kept in step with the text
 * between answers (spans.js).
 *
 * @typedef {object} Analysis
 * @property {Token[]} tokens
 * @property {Problem[]} errors The pattern's errors, for the marks.
 * @property {VariableCheck[]} variables
 */

/**
 * @typedef {object} Row
 * @property {number} key Stable for the row's life; element ids use it.
 * @property {number} id The saved route's ID; 0 for a new route.
 * @property {HTMLLIElement} li
 * @property {HTMLHeadingElement} title
 * @property {HTMLButtonElement} up
 * @property {HTMLButtonElement} down
 * @property {HTMLButtonElement} remove
 * @property {import("./form/pattern.js").PatternEditor} pattern
 * @property {HTMLInputElement} destination
 * @property {HTMLElement} patternMessages
 * @property {HTMLElement} destinationMessages
 * @property {HTMLElement} routeMessages
 * @property {{ root: HTMLElement, update: (variables: TableVariable[]) => void }} table
 * @property {{ pattern: boolean, destination: boolean }} touched
 * @property {string} text The pattern text the analysis belongs to.
 * @property {Analysis} analysis
 * @property {RouteCheck | null} check The newest answer about this route.
 * @property {Problem[] | null} saveProblems Problems a refused save reported.
 * @property {LocalVariable[]} local
 * @property {Map<string, { description: string, example: string }>} meta
 * @property {Map<string, string>} samples Sample text by variable.
 * @property {Map<string, string>} expressions The last expression of each
 *   variable, so that switching away from regex and back restores it.
 * @property {number | null} nameEnd Where the link's name ends in the
 *   pattern: the end of the first token the server reported, when it is a
 *   literal at the start, kept in step with edits of the name; null when
 *   unknown.
 */

const form = byId("link-form", HTMLFormElement);
const mode = form.dataset.mode === "edit" ? "edit" : "new";
/** @type {InitialDraft} */
const initial = readInitial(form.dataset.initial || "");
const routesList = byId("routes", HTMLOListElement);
const addButton = byId("add-route", HTMLButtonElement);
const description = byId("description", HTMLTextAreaElement);
const nameStatus = byId("name-status", HTMLDivElement);
const linkProblems = byId("link-problems", HTMLUListElement);
const routeCount = byId("route-count", HTMLParagraphElement);
const alertBox = byId("form-alert", HTMLDivElement);
const saveButton = byId("save", HTMLButtonElement);
const saveStatus = byId("save-status", HTMLSpanElement);
const previewRoot = byId("preview", HTMLDivElement);
const typeTemplate = byId("type-options", HTMLTemplateElement);
const typeOptions = [...typeTemplate.content.querySelectorAll("option")];
/** The most routes a link can have (PRD 5.2), as the server says. */
const maxRoutes = Number(form.dataset.maxRoutes) > 0 ? Number(form.dataset.maxRoutes) : 20;

/** @type {Row[]} */
const rows = [];
let nextKey = 0;
/** @type {ValidateAnswer | null} */
let answer = null;
/** Set by a save attempt: from then on every problem shows. */
let submitted = false;
let saving = false;
/** Set when the page is left on purpose, so the unsaved warning stays away. */
let leaving = false;
let version = initial.version;
/** @type {ReturnType<typeof mountLauncher> | null} */
let launcher = null;
let launcherKey = "";
/** @type {Map<number, string>} owners of links the user can't edit, by ID */
const owners = new Map();

/** The draft as the page opened it, for telling whether anything changed. */
let baseline = "";

// ------------------------------------------------------------------ start

/**
 * Builds the form from the initial draft and wires it. It runs last, at the
 * end of this module, once every binding below exists.
 */
function start() {
  description.value = initial.description || "";
  const startRoutes = initial.routes.length > 0 ? initial.routes : [{ id: 0, pattern: "", destination: "", variables: {} }];
  for (const r of startRoutes) addRow(r);
  // Routes appended from the address are not part of the saved link: the
  // form is changed from the start, and leaving it warns.
  baseline = snapshot(rows.slice(0, rows.length - (initial.appended || 0)));
  renumber();
  form.hidden = false;

  if (initial.appended > 0) {
    const added = rows.slice(rows.length - initial.appended);
    takeHandedOver(added);
    const one = added.length === 1;
    showAlert("info", one ? "Route added" : "Routes added", [
      `Check ${one ? "it" : "them"}, then save to add ${one ? "it" : "them"} to go/${initial.name}. Where two routes fit the same go link, the more specific one opens it, wherever it is in the list, so a new route can take go links that one of the link's own routes opens now.`,
      el(
        "ul",
        { class: "added-routes" },
        added.map((row) =>
          el(
            "li",
            null,
            el("bdi", { class: "golink" }, `go/${row.pattern.input.value}`),
            " opens ",
            row.destination.value ? el("bdi", { class: "url" }, row.destination.value) : "nothing yet",
          ),
        ),
      ),
    ]);
    added[0].pattern.input.focus();
  } else if (mode === "new") {
    rows[0].pattern.input.focus();
    const end = rows[0].pattern.input.value.length;
    rows[0].pattern.input.setSelectionRange(end, end);
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    void save();
  });
  description.addEventListener("input", changed);
  addButton.addEventListener("click", () => {
    const name = answer && answer.name.value ? answer.name.value : "";
    const row = addRow({ id: 0, pattern: name ? `${name}/` : "", destination: "", variables: {} });
    renumber();
    row.pattern.input.focus();
    const end = row.pattern.input.value.length;
    row.pattern.input.setSelectionRange(end, end);
    announce(`Route ${rows.length} added.`);
    changed();
  });
  window.addEventListener("beforeunload", (event) => {
    if (leaving || !isDirty()) return;
    event.preventDefault();
    // Older browsers show the warning only when returnValue is set.
    event.returnValue = "";
  });
  void check();
}

// ------------------------------------------------------------------- rows

/**
 * Adds a route row at the end.
 *
 * @param {InitialRoute} r
 * @returns {Row}
 */
function addRow(r) {
  const key = ++nextKey;
  const id = `route-${key}`;
  const pattern = patternEditor({
    id: `${id}-pattern`,
    value: r.pattern,
    describedBy: `${id}-pattern-help ${id}-pattern-msgs`,
  });
  const destination = el("input", {
    class: "input input-mono",
    id: `${id}-destination`,
    type: "text",
    inputmode: "url",
    value: r.destination,
    autocomplete: "off",
    autocapitalize: "none",
    spellcheck: "false",
    placeholder: "https://reports.example.com/daily?month={month}",
    "aria-describedby": `${id}-destination-help ${id}-destination-msgs`,
  });
  const title = el("h3", { class: "route-title", id: `${id}-title`, tabindex: "-1" }, "Route");
  const up = el("button", { type: "button", class: "button button-quiet button-small" }, el("span", { "aria-hidden": "true" }, "↑ "), "Up");
  const down = el("button", { type: "button", class: "button button-quiet button-small" }, el("span", { "aria-hidden": "true" }, "↓ "), "Down");
  const remove = el("button", { type: "button", class: "button button-quiet button-small route-remove" }, "Remove");
  const patternMessages = el("ul", { class: "field-messages", id: `${id}-pattern-msgs` });
  const destinationMessages = el("ul", { class: "field-messages", id: `${id}-destination-msgs` });
  const routeMessages = el("ul", { class: "field-messages route-messages" });

  /** @type {Row} */
  const row = {
    key,
    id: r.id || 0,
    li: el("li", { class: "route", "aria-labelledby": title.id }),
    title,
    up,
    down,
    remove,
    pattern,
    destination,
    patternMessages,
    destinationMessages,
    routeMessages,
    table: variablesTable({
      typeOptions,
      handlers: {
        type: (name, type) => setVariable(row, name, type, null),
        expression: (name, expression) => setVariable(row, name, "regex", expression),
        samples: (name, text) => {
          row.samples.set(name, text);
          renderVariables(row);
          changed();
        },
        meta: (name, field, value) => {
          const meta = row.meta.get(name) || { description: "", example: "" };
          meta[field] = value;
          row.meta.set(name, meta);
          changed();
        },
      },
    }),
    // A prefilled field shows its problems at once; an empty one only after
    // the user has been in it, so a new form does not open full of errors.
    touched: { pattern: r.pattern !== "", destination: r.destination !== "" },
    text: r.pattern,
    analysis: { tokens: [], errors: [], variables: [] },
    check: null,
    saveProblems: null,
    local: [],
    meta: new Map(Object.entries(r.variables || {}).map(([name, v]) => [name, { description: v.description || "", example: v.example || "" }])),
    samples: new Map(),
    expressions: new Map(),
    nameEnd: null,
  };

  up.addEventListener("click", () => move(row, -1));
  down.addEventListener("click", () => move(row, 1));
  remove.addEventListener("click", () => removeRow(row));
  pattern.input.addEventListener("input", () => {
    row.touched.pattern = true;
    const before = row.text;
    const edit = diff(before, pattern.input.value);
    patternChanged(row, edit);
    if (row === rows[0]) renameOthers(row, before, edit);
  });
  pattern.input.addEventListener("blur", () => {
    if (!row.touched.pattern) {
      row.touched.pattern = true;
      renderRow(row);
    }
  });
  destination.addEventListener("input", () => {
    row.touched.destination = true;
    row.saveProblems = null;
    changed();
  });
  destination.addEventListener("blur", () => {
    if (!row.touched.destination) {
      row.touched.destination = true;
      renderRow(row);
    }
  });

  append(
    row.li,
    el(
      "div",
      { class: "route-head" },
      title,
      el("div", { class: "cluster route-tools" }, up, down, remove),
    ),
    el(
      "div",
      { class: "field" },
      el("label", { for: pattern.input.id }, "Pattern"),
      pattern.root,
      el("p", { class: "field-help", id: `${id}-pattern-help` }, "What people type after go/. Write variables in braces: {name}, or {name:int} for a whole number."),
      patternMessages,
    ),
    el(
      "div",
      { class: "field" },
      el("label", { for: destination.id }, "Destination"),
      destination,
      el("p", { class: "field-help", id: `${id}-destination-help` }, "The address to open. Put a variable's value in it with {name}."),
      destinationMessages,
    ),
    routeMessages,
    row.table.root,
  );
  routesList.appendChild(row.li);
  rows.push(row);
  renderRow(row);
  return row;
}

/**
 * The pattern's text changed by edit: keep what the form knows in step
 * with it until the server answers.
 *
 * @param {Row} row
 * @param {import("./form/spans.js").Edit} edit
 */
function patternChanged(row, edit) {
  row.text = row.pattern.input.value;
  row.analysis = {
    tokens: moveSpans(row.analysis.tokens, edit),
    errors: moveSpans(row.analysis.errors, edit),
    variables: moveSpans(row.analysis.variables, edit),
  };
  row.local = moveSpans(row.local, edit);
  row.saveProblems = null;
  paintPattern(row);
  changed();
}

/**
 * The first route's name was edited: make the same edit to every other route
 * whose pattern starts with the old name (DIR-3). Where a name ends is the
 * server's answer (nameEnd), kept in step with the edits since; an edit that
 * reaches past the name ends the renaming until the server reads the new
 * pattern. The other routes start with exactly the old name, so the same
 * edit leaves them starting with exactly the new one.
 *
 * @param {Row} first
 * @param {string} before The first route's pattern before the edit.
 * @param {import("./form/spans.js").Edit} edit
 */
function renameOthers(first, before, edit) {
  const end = first.nameEnd;
  if (end === null || edit.start > end || edit.oldEnd > end) {
    first.nameEnd = null;
    return;
  }
  const delta = edit.newEnd - edit.oldEnd;
  first.nameEnd = end + delta;
  const oldName = before.slice(0, end);
  for (const row of rows) {
    if (row === first || row.nameEnd !== end || row.pattern.input.value.slice(0, end) !== oldName) continue;
    const text = row.pattern.input.value;
    row.pattern.input.value = text.slice(0, edit.start) + first.pattern.input.value.slice(edit.start, edit.newEnd) + text.slice(edit.oldEnd);
    row.touched.pattern = true;
    patternChanged(row, edit);
    row.nameEnd = end + delta;
  }
}

/**
 * Rewrites one variable of a route's pattern with the type chosen in the
 * table (VAR-3). expression is the regex's new expression; null keeps the
 * one the variable had.
 *
 * @param {Row} row
 * @param {string} name
 * @param {string} type
 * @param {string | null} expression
 */
function setVariable(row, name, type, expression) {
  const span = variableSpan(row, name);
  if (!span) return; // not where the form last saw it; the next answer redraws the table
  const remembered = row.expressions.get(name) || "";
  const regex = expression !== null ? expression : remembered;
  if (type === "regex") row.expressions.set(name, regex);
  const text = variableText(name, type, regex);
  const before = row.pattern.input.value;
  const after = before.slice(0, span.start) + text + before.slice(span.end);
  row.pattern.input.value = after;
  row.touched.pattern = true;
  const edit = { start: span.start, oldEnd: span.end, newEnd: span.start + text.length };
  patternChanged(row, edit);
  // The variable is where it was written, until the server reads it again.
  /** @type {LocalVariable} */
  const local = { name, type, expression: type === "regex" ? regex : "", start: span.start, end: edit.newEnd, text };
  row.local = [...row.local.filter((v) => v.name !== name), local];
  row.analysis.tokens = [...row.analysis.tokens, { kind: "variable", start: local.start, end: local.end, name, type }].sort((a, b) => a.start - b.start);
  paintPattern(row);
  renderVariables(row);
}

/**
 * The text of a variable of the given type. This is the one piece of the
 * pattern syntax the form writes: the table exists so that nobody has to
 * write it by hand (VAR-3). Whether the result is valid is the server's to
 * say, like everything typed.
 *
 * @param {string} name
 * @param {string} type
 * @param {string} expression
 * @returns {string}
 */
function variableText(name, type, expression) {
  if (type === "string") return `{${name}}`;
  if (type === "regex") return `{${name}:regex(${expression})}`;
  return `{${name}:${type}}`;
}

/**
 * Where a variable is in the pattern now: as the table last wrote it, or as
 * the server last read it (moved along with the edits since).
 *
 * @param {Row} row
 * @param {string} name
 * @returns {{ start: number, end: number } | null}
 */
function variableSpan(row, name) {
  const length = row.pattern.input.value.length;
  const local = row.local.find((v) => v.name === name);
  if (local && fits(local, length)) return local;
  const v = row.analysis.variables.find((x) => x.name === name);
  return v && fits(v, length) ? v : null;
}

/**
 * Moves a route up (-1) or down (1). The other row is moved around it, so
 * the button that was pressed keeps focus; when it can't be pressed again,
 * focus goes to its partner.
 *
 * @param {Row} row
 * @param {number} by
 */
function move(row, by) {
  const from = rows.indexOf(row);
  const to = from + by;
  if (from < 0 || to < 0 || to >= rows.length) return;
  const other = rows[to];
  rows[to] = row;
  rows[from] = other;
  if (by < 0) routesList.insertBefore(other.li, row.li.nextSibling);
  else routesList.insertBefore(other.li, row.li);
  renumber();
  const pressed = by < 0 ? row.up : row.down;
  if (pressed.disabled) (by < 0 ? row.down : row.up).focus();
  announce(`Route moved to position ${to + 1} of ${rows.length}.`);
  changed();
}

/**
 * Removes a route. Focus goes to the heading of the route that took its
 * place, or of the one before it, never to another Remove button.
 *
 * @param {Row} row
 */
function removeRow(row) {
  const at = rows.indexOf(row);
  if (at < 0 || rows.length === 1) return;
  rows.splice(at, 1);
  row.li.remove();
  renumber();
  const next = rows[Math.min(at, rows.length - 1)];
  next.title.focus();
  announce(`Route ${at + 1} removed. ${rows.length === 1 ? "One route is" : `${rows.length} routes are`} left.`);
  changed();
}

/** Numbers the routes and sets what their buttons can do and are called. */
function renumber() {
  rows.forEach((row, i) => {
    const n = i + 1;
    replace(row.title, `Route ${n}`);
    row.up.disabled = i === 0;
    row.down.disabled = i === rows.length - 1;
    row.remove.disabled = rows.length === 1;
    row.up.setAttribute("aria-label", `Move route ${n} up`);
    row.down.setAttribute("aria-label", `Move route ${n} down`);
    row.remove.setAttribute("aria-label", `Remove route ${n}`);
  });
  addButton.disabled = rows.length >= maxRoutes;
  replace(routeCount, rows.length === 1 ? "1 route" : `${rows.length} routes`, rows.length >= maxRoutes ? ` (the most a link can have)` : "");
}

// ------------------------------------------------------------- validating

/** Something in the draft changed: check it again, a moment later. */
function changed() {
  void check();
}

const checkLater = debounced((signal) => validate(signal));

/** Asks the server about the draft as it is now and shows the answer. */
async function check() {
  try {
    const outcome = await checkLater();
    if (outcome.current) show(outcome.value);
  } catch (err) {
    // validate reports failures in its value; anything else is a bug.
    console.error(err);
  }
}

/**
 * The draft as the API takes it.
 *
 * @param {{ samples?: boolean, rows?: Row[] }} [options]
 */
function draftBody({ samples = false, rows: which = rows } = {}) {
  return {
    description: description.value,
    routes: which.map((row) => {
      /** @type {Record<string, any>} */
      const route = {
        pattern: row.pattern.input.value,
        destination: row.destination.value,
        variables: variablesMeta(row),
      };
      if (row.id) route.id = row.id;
      if (samples) {
        /** @type {Record<string, string[]>} */
        const values = {};
        for (const [name, text] of row.samples) {
          const list = sampleValues(text);
          if (list.length > 0 && knownVariables(row).has(name)) values[name] = list;
        }
        route.samples = values;
        // The expressions the table is writing that the server can't read
        // in the pattern yet: asked about on their own, so a "[" or "(" left
        // open is explained in the expression's terms (VAR-3).
        /** @type {Record<string, string>} */
        const expressions = {};
        for (const v of row.local) {
          if (v.type === "regex") expressions[v.name] = v.expression;
        }
        if (Object.keys(expressions).length > 0) route.expressions = expressions;
      }
      return route;
    }),
  };
}

/**
 * The names the server last found in a route's pattern, with those the
 * table is writing.
 *
 * @param {Row} row
 * @returns {Set<string>}
 */
function knownVariables(row) {
  return new Set([...row.analysis.variables.map((v) => v.name), ...row.local.map((v) => v.name)]);
}

/**
 * The descriptions and examples of a route's variables, for the variables
 * the pattern has: a description of a variable it does not declare would be
 * refused.
 *
 * @param {Row} row
 */
function variablesMeta(row) {
  /** @type {Record<string, { description: string, example: string }>} */
  const out = {};
  const known = knownVariables(row);
  for (const [name, meta] of row.meta) {
    if (known.has(name) && (meta.description !== "" || meta.example !== "")) out[name] = { ...meta };
  }
  return out;
}

/**
 * One validate call. The answer is checked against the draft that was
 * sent, so an answer about older text is never drawn over newer text.
 *
 * @param {AbortSignal} [signal]
 * @returns {Promise<{ ok: true, answer: ValidateAnswer, sent: string[] } | { ok: false, message: string }>}
 */
async function validate(signal) {
  const body = { ...draftBody({ samples: true }), linkId: initial.linkId || null, describeVariables: true, explainSamples: true };
  const sent = rows.map((row) => row.pattern.input.value);
  try {
    const response = await apiFetch("links/validate", { method: "POST", body, signal });
    if (response.status !== 200 || !response.data || !Array.isArray(response.data.routes)) {
      return { ok: false, message: errorMessage(response, "The Directory could not check this link.") };
    }
    return { ok: true, answer: /** @type {ValidateAnswer} */ (response.data), sent };
  } catch (err) {
    if (isAbort(err)) throw err;
    return { ok: false, message: "Could not reach the Directory to check this link. Check that it is running." };
  }
}

/**
 * Shows a validate answer.
 *
 * @param {{ ok: true, answer: ValidateAnswer, sent: string[] } | { ok: false, message: string }} outcome
 */
function show(outcome) {
  if (!outcome.ok) {
    showAlert("warning", "Not checked", [outcome.message]);
    return;
  }
  const current = rows.map((row) => row.pattern.input.value);
  if (outcome.sent.length !== current.length || outcome.sent.some((text, i) => text !== current[i])) return;
  if (alertBox.dataset.kind === "warning") hideAlert();
  answer = outcome.answer;
  answer.routes.forEach((check, i) => {
    const row = rows[i];
    if (!row) return;
    row.check = check;
    row.text = row.pattern.input.value;
    row.analysis = {
      tokens: check.tokens || [],
      errors: (check.problems || []).filter((p) => p.field === "pattern" && p.severity === "error"),
      variables: check.variables || [],
    };
    const first = row.analysis.tokens[0];
    row.nameEnd = first && first.kind === "literal" && first.start === 0 ? first.end : null;
    // A variable the table wrote is the server's again once it is read
    // there; until then it stays, as long as its text is unchanged.
    row.local = row.local.filter((v) => {
      if (row.analysis.variables.some((x) => x.name === v.name)) return false;
      return row.text.slice(v.start, v.end) === v.text;
    });
    for (const v of row.analysis.variables) {
      if (v.type === "regex") row.expressions.set(v.name, v.expression);
    }
  });
  rows.forEach(renderRow);
  renderLinkLevel();
  renderName();
  updatePreview();
  // Tests and scripts wait for this instead of for the network to settle.
  form.dataset.ready = "true";
}

// -------------------------------------------------------------- rendering

/**
 * Whether a field's problems are shown yet.
 *
 * @param {Row} row
 * @param {"pattern" | "destination"} field
 */
function shown(row, field) {
  return submitted || row.touched[field];
}

/** @param {Row} row */
function problemsOf(row) {
  return row.saveProblems || (row.check ? row.check.problems || [] : []);
}

/** @param {Row} row */
function paintPattern(row) {
  row.pattern.paint(row.analysis.tokens, shown(row, "pattern") ? row.analysis.errors : []);
}

/**
 * Draws a route's messages, marks and variables.
 *
 * @param {Row} row
 */
function renderRow(row) {
  const problems = problemsOf(row);
  /** @param {"pattern" | "destination"} field */
  const of = (field) => (shown(row, field) ? problems.filter((p) => p.field === field) : []);
  const patternProblems = of("pattern");
  const destinationProblems = of("destination");
  replace(row.patternMessages, patternProblems.map((p) => messageItem(p.severity, p.message)));
  replace(row.destinationMessages, destinationProblems.map((p) => messageItem(p.severity, p.message)));
  replace(row.routeMessages, problems.filter((p) => p.field === "route").map((p) => messageItem(p.severity, p.message)));
  row.pattern.setInvalid(patternProblems.some((p) => p.severity === "error"));
  setAttr(row.destination, "aria-invalid", destinationProblems.some((p) => p.severity === "error") ? "true" : null);
  paintPattern(row);
  renderVariables(row);
}

/**
 * Fills the route's variables table: the variables the server read, and
 * those the table is writing that it can't read yet.
 *
 * @param {Row} row
 */
function renderVariables(row) {
  const problems = row.check && row.text === row.pattern.input.value ? row.check.problems || [] : [];
  /** @type {TableVariable[]} */
  const list = row.analysis.variables.map((v) => tableVariable(row, v.name, v.type, v.accepts, v.expression, false, v, problems));
  for (const v of row.local) {
    if (list.some((x) => x.name === v.name)) continue;
    list.push(tableVariable(row, v.name, v.type, "", v.expression, true, v, problems));
  }
  list.sort((a, b) => spanStart(row, a.name) - spanStart(row, b.name));
  row.table.update(list);
}

/**
 * @param {Row} row
 * @param {string} name
 */
function spanStart(row, name) {
  const span = variableSpan(row, name);
  return span ? span.start : Number.MAX_SAFE_INTEGER;
}

/**
 * @param {Row} row
 * @param {string} name
 * @param {string} type
 * @param {string} accepts
 * @param {string} expression
 * @param {boolean} pending
 * @param {{ start: number, end: number }} span
 * @param {Problem[]} problems
 * @returns {TableVariable}
 */
function tableVariable(row, name, type, accepts, expression, pending, span, problems) {
  const meta = row.meta.get(name) || { description: "", example: "" };
  let inside = problems.filter((p) => p.field === "pattern" && p.start >= span.start && p.end <= span.end && p.start < span.end);
  // The expression asked about on its own: its problem in its own terms
  // beats the pattern's, which can only say that regex( is not closed.
  const own = row.check && row.check.expressions && row.text === row.pattern.input.value ? row.check.expressions[name] : undefined;
  if (pending && type === "regex" && own && !own.valid && own.message) {
    inside = [{ severity: "error", field: "pattern", code: "", message: own.message, start: span.start, end: span.end }];
  }
  return {
    name,
    type,
    accepts,
    expression,
    pending,
    problems: inside,
    samples: row.check && row.check.samples ? row.check.samples[name] : undefined,
    sampleReasons: row.check && row.check.sampleReasons ? row.check.sampleReasons[name] : undefined,
    sampleText: row.samples.get(name) || "",
    description: meta.description,
    example: meta.example,
  };
}

/** Problems of the link as a whole. */
function renderLinkLevel() {
  const problems = answer ? answer.problems || [] : [];
  replace(linkProblems, problems.map((p) => messageItem(p.severity, p.message)));
}

/**
 * Says whether the name is free (DIR-2); when it is taken, links to the link
 * that has it and, when the user may edit that one, offers to add the
 * routes there instead (DIR-4).
 */
function renderName() {
  const name = answer ? answer.name : null;
  if (!name || !name.value) {
    delete nameStatus.dataset.key;
    clear(nameStatus);
    nameStatus.className = "name-status";
    return;
  }
  const golink = el("span", { class: "golink" }, `go/${name.value}`);
  if (name.available) {
    delete nameStatus.dataset.key;
    if (mode === "new") {
      nameStatus.className = "name-status name-free";
      replace(nameStatus, el("span", { class: "message-kind" }, "Available:"), " ", golink, " is free.");
    } else if (name.value !== initial.name) {
      nameStatus.className = "name-status name-rename";
      replace(nameStatus, el("span", { class: "message-kind" }, "Rename:"), " saving renames ", el("span", { class: "golink" }, `go/${initial.name}`), " to ", golink, ".");
    } else {
      clear(nameStatus);
      nameStatus.className = "name-status";
    }
    return;
  }
  const existing = name.existing;
  if (!existing) {
    clear(nameStatus);
    nameStatus.className = "name-status";
    return;
  }
  // #name-status is a live region: what changes in it is read out, once.
  // Drawing the same notice again for every answer would read it again.
  const key = JSON.stringify([existing.id, existing.name, name.canEdit, mode, rows.length, owners.get(existing.id)]);
  if (nameStatus.dataset.key === key) return;
  nameStatus.dataset.key = key;
  nameStatus.className = "name-status name-taken notice notice-warning";
  const href = `/-/links/${encodeURIComponent(existing.name)}`;
  /** @type {(string | Node)[]} */
  const parts = [
    el("strong", { class: "notice-title" }, "Name taken: ", el("span", { class: "golink" }, `go/${existing.name}`), " already exists."),
  ];
  if (name.canEdit && mode === "new") {
    const count = rows.length;
    const add = el(
      "button",
      { type: "button", class: "button button-small" },
      count === 1 ? "Add this route to " : "Add these routes to ",
      el("span", { class: "golink" }, `go/${existing.name}`),
    );
    add.addEventListener("click", () => addToExisting(existing.name));
    parts.push(
      el("p", null, "You can edit it, so you can add ", count === 1 ? "this route" : "these routes", " to it instead of creating a new link."),
      el("p", { class: "cluster" }, add, el("a", { href }, "Open ", el("span", { class: "golink" }, `go/${existing.name}`))),
    );
  } else if (name.canEdit) {
    parts.push(el("p", null, "Choose another name, or ", el("a", { href }, "open ", el("span", { class: "golink" }, `go/${existing.name}`)), "."));
  } else {
    const owner = owners.get(existing.id);
    parts.push(
      el(
        "p",
        null,
        owner ? `It belongs to ${owner}, so you can't add routes to it. ` : "You can't edit it. ",
        "Choose another name, or ",
        el("a", { href }, "open ", el("span", { class: "golink" }, `go/${existing.name}`)),
        owner ? ` and ask ${owner} to add your route.` : " to see who owns it.",
      ),
    );
    if (owner === undefined) void loadOwner(existing.id);
  }
  replace(nameStatus, ...parts);
}

/**
 * Finds out who owns a link the user can't edit, so the form can say whom
 * to ask (DIR-4). The answer comes from the link's own API entry.
 *
 * @param {number} id
 */
async function loadOwner(id) {
  owners.set(id, "");
  try {
    const response = await apiFetch(`links/${encodeURIComponent(String(id))}`);
    const owner = response.status === 200 && response.data && response.data.owner && response.data.owner.displayName;
    if (typeof owner === "string" && owner !== "") {
      owners.set(id, owner);
      renderName();
    }
  } catch {
    // The sentence without a name is still true.
  }
}

/**
 * Opens the edit form of the link that has the name, with this form's
 * routes appended there (DIR-4). The address carries each route's pattern
 * and destination; what the user wrote about the variables travels beside
 * it in sessionStorage, and the edit form takes it back (takeHandedOver), so
 * nothing typed here is lost on the way.
 *
 * @param {string} name
 */
function addToExisting(name) {
  const query = new URLSearchParams();
  for (const row of rows) {
    query.append("pattern", row.pattern.input.value);
    query.append("destination", row.destination.value);
  }
  const routes = rows.map((row) => ({
    pattern: row.pattern.input.value,
    destination: row.destination.value,
    variables: Object.fromEntries(row.meta),
  }));
  try {
    sessionStorage.setItem(APPEND_KEY, JSON.stringify({ name, routes }));
  } catch {
    // Without storage the routes still arrive, without their notes.
  }
  leaving = true;
  window.location.assign(`/-/links/${encodeURIComponent(name)}/edit?${query.toString()}`);
}

/**
 * Takes back the notes on variables that the create form handed over with
 * the routes appended to this edit form (addToExisting). They are used only
 * when the routes in the address are exactly the ones handed over, and are
 * read once.
 *
 * @param {Row[]} added The appended rows, in order.
 */
function takeHandedOver(added) {
  /** @type {any} */
  let handed = null;
  try {
    const raw = sessionStorage.getItem(APPEND_KEY);
    sessionStorage.removeItem(APPEND_KEY);
    handed = raw === null ? null : JSON.parse(raw);
  } catch {
    return;
  }
  if (!handed || typeof handed.name !== "string" || handed.name.toLowerCase() !== initial.name.toLowerCase()) return;
  const routes = Array.isArray(handed.routes) ? handed.routes : [];
  const same =
    routes.length === added.length &&
    added.every((row, i) => routes[i] && routes[i].pattern === row.pattern.input.value && routes[i].destination === row.destination.value);
  if (!same) return;
  added.forEach((row, i) => {
    const variables = routes[i].variables && typeof routes[i].variables === "object" ? routes[i].variables : {};
    for (const [name, meta] of Object.entries(variables)) {
      if (!meta || typeof meta !== "object") continue;
      const description = typeof meta.description === "string" ? meta.description : "";
      const example = typeof meta.example === "string" ? meta.example : "";
      if (description !== "" || example !== "") row.meta.set(name, { description, example });
    }
  });
}

// ---------------------------------------------------------------- preview

/**
 * Gives the launcher the routes that could be saved as they are, with the
 * tokens and variables the server read in them. Routes with errors are left
 * out until they are fixed.
 */
function updatePreview() {
  /** @type {LauncherRoute[]} */
  const routes = [];
  rows.forEach((row, i) => {
    const check = row.check;
    if (!check || row.text !== row.pattern.input.value) return;
    if ((check.problems || []).some((p) => p.severity === "error")) return;
    routes.push({
      id: i,
      pattern: row.pattern.input.value,
      destination: row.destination.value,
      tokens: check.tokens || [],
      variables: (check.variables || []).map((v) => {
        const meta = row.meta.get(v.name) || { description: "", example: "" };
        return { name: v.name, type: v.type, accepts: v.accepts, description: meta.description, example: meta.example };
      }),
    });
  });
  const key = JSON.stringify(routes);
  if (key === launcherKey) return;
  launcherKey = key;
  if (routes.length === 0) {
    launcher = null;
    replace(previewRoot, el("p", { class: "empty form-preview-empty" }, "The preview appears once a route has a pattern and a destination without errors."));
    return;
  }
  if (launcher) {
    launcher.setRoutes(routes);
    return;
  }
  // Opening a draft's destination leaves the form, with what is not saved;
  // the preview opens it in a new tab instead.
  launcher = mountLauncher(previewRoot, { name: answer ? answer.name.value : "", routes, resolve: resolveDraft, newTab: true });
}

/**
 * The launcher's resolve for the unsaved draft: the validate endpoint's
 * preview, decided by the link model exactly as for a saved link.
 *
 * @param {string} path
 * @param {AbortSignal} signal
 * @param {{ route: LauncherRoute, index: number }} info
 * @returns {Promise<LauncherOutcome>}
 */
async function resolveDraft(path, signal, info) {
  const route = info && info.route && typeof info.route.id === "number" ? info.route.id : null;
  const body = { ...draftBody(), linkId: initial.linkId || null, preview: { route, path } };
  const response = await apiFetch("links/validate", { method: "POST", body, signal });
  const preview = response.status === 200 && response.data ? response.data.preview : null;
  if (!preview || typeof preview.kind !== "string") {
    return { kind: "error", message: errorMessage(response, "The Directory could not preview this link.") };
  }
  switch (preview.kind) {
    case "hit": {
      const answered = typeof preview.route === "number" ? rows[preview.route] : null;
      return {
        kind: "hit",
        destination: String(preview.destination || ""),
        routeId: typeof preview.route === "number" ? preview.route : undefined,
        pattern: answered ? answered.pattern.input.value : undefined,
      };
    }
    case "miss_known":
      return {
        kind: "miss",
        reasons: (Array.isArray(preview.reasons) ? preview.reasons : []).map(
          /** @param {any} r */ (r) => ({
            routeId: r.route,
            variable: r.variable || "",
            value: r.value || "",
            accepts: r.accepts || "",
            message: r.message || "This route does not take this go link.",
          }),
        ),
      };
    case "bad_request":
      return { kind: "invalid", message: String(preview.message || "This can't be read as a go link.") };
    default:
      return { kind: "miss", message: String(preview.message || "This link can't be previewed yet.") };
  }
}

// ------------------------------------------------------------------ saving

/**
 * Checks the draft once more, then saves it. Problems are placed beside
 * their fields; a conflict is explained above the form, keeping everything
 * the user typed.
 */
async function save() {
  if (saving) return;
  saving = true;
  checkLater.cancel();
  saveButton.setAttribute("aria-disabled", "true");
  replace(saveStatus, "Checking…");
  try {
    const outcome = await validate();
    submitted = true;
    show(outcome);
    if (!outcome.ok) return;
    if (!outcome.answer.valid) {
      refused("This link can't be saved yet", ["Fix the problems marked below, then save again."]);
      return;
    }
    replace(saveStatus, "Saving…");
    const body = draftBody();
    const response =
      mode === "new"
        ? await apiFetch("links", { method: "POST", body })
        : await apiFetch(`links/${encodeURIComponent(String(initial.linkId))}`, { method: "PATCH", body: { ...body, version } });
    if (response.status === 200 || response.status === 201) {
      const name = response.data && typeof response.data.name === "string" ? response.data.name : "";
      const target = name ? `/-/links/${encodeURIComponent(name)}` : "/-/";
      setFlash(target, mode === "new" ? `Created go/${name}. It works now.` : `Saved go/${name}. The change works now.`);
      leaving = true;
      replace(saveStatus, "Saved.");
      window.location.assign(target);
      return;
    }
    saveFailed(response);
  } catch (err) {
    if (!isAbort(err)) {
      showAlert("error", "Not saved", ["Could not reach the Directory. Check that it is running, then save again. Your changes are still here."]);
      alertBox.focus();
    }
  } finally {
    saving = false;
    saveButton.removeAttribute("aria-disabled");
    if (!leaving) replace(saveStatus);
  }
}

/**
 * A save the server refused.
 *
 * @param {import("./lib/api.js").ApiResponse} response
 */
function saveFailed(response) {
  const error = response.data && response.data.error ? response.data.error : {};
  const code = typeof error.code === "string" ? error.code : "";
  const details = error.details || {};
  const message = errorMessage(response, "The link was not saved.");
  if (response.status === 422) {
    const routes = Array.isArray(details.routes) ? details.routes : [];
    rows.forEach((row, i) => {
      row.saveProblems = routes[i] && Array.isArray(routes[i].problems) ? routes[i].problems : null;
      renderRow(row);
    });
    replace(linkProblems, (Array.isArray(details.problems) ? details.problems : []).map(/** @param {Problem} p */ (p) => messageItem(p.severity, p.message)));
    refused("Not saved", [message]);
    return;
  }
  if (response.status === 409 && code === "link.version_conflict") {
    const reload = el("button", { type: "button", class: "button button-small" }, "Reload and discard my changes");
    reload.addEventListener("click", () => {
      leaving = true;
      window.location.reload();
    });
    showAlert("error", "Changed elsewhere", [
      el("p", null, message, " Your changes have not been saved and are still in the form."),
      el(
        "p",
        { class: "cluster" },
        el("a", { href: `/-/links/${encodeURIComponent(initial.name)}`, target: "_blank", rel: "noopener" }, "See the current version in a new tab"),
        reload,
      ),
    ]);
    alertBox.focus();
    return;
  }
  if (response.status === 409 && details.existing) {
    answer = answer || { valid: false, name: { value: "", available: false, existing: null, canEdit: false }, routes: [], problems: [] };
    answer.name = { value: details.existing.name, available: false, existing: details.existing, canEdit: Boolean(details.canEdit) };
    renderName();
    refused("Not saved", [message]);
    return;
  }
  showAlert("error", "Not saved", [message, " Your changes are still here."]);
  alertBox.focus();
}

/**
 * Shows why saving stopped and puts focus on the first field that needs
 * attention, or on the explanation.
 *
 * @param {string} title
 * @param {(string | Node)[]} body
 */
function refused(title, body) {
  showAlert("error", title, body);
  const target = firstInvalid();
  if (target) target.focus();
  else alertBox.focus();
}

/** The first field marked invalid, or the name conflict. */
function firstInvalid() {
  /** @type {HTMLElement | null} */
  const field = form.querySelector('[aria-invalid="true"]');
  if (field) return field;
  if (answer && answer.name.existing) return rows[0].pattern.input;
  return null;
}

// ---------------------------------------------------------------- helpers

/**
 * @param {"error" | "warning" | "info"} kind
 * @param {string} title
 * @param {(string | Node)[]} body
 */
function showAlert(kind, title, body) {
  alertBox.className = `notice notice-${kind}`;
  alertBox.dataset.kind = kind;
  alertBox.setAttribute("role", kind === "info" ? "status" : "alert");
  replace(alertBox, el("strong", { class: "notice-title" }, title), ...body.map((part) => (typeof part === "string" ? el("p", null, part) : part)));
  alertBox.hidden = false;
}

function hideAlert() {
  alertBox.hidden = true;
  delete alertBox.dataset.kind;
  clear(alertBox);
}

/**
 * What the user can change, as text, for telling whether anything changed.
 *
 * @param {Row[]} which
 */
function snapshot(which) {
  return JSON.stringify(draftBody({ rows: which }));
}

function isDirty() {
  return snapshot(rows) !== baseline;
}

/**
 * Reads the draft the page was rendered with: JSON, base64-encoded so that
 * none of its text is markup (httpd.formPage.Initial). A broken attribute is
 * a bug in the page; the form then starts empty rather than not at all.
 *
 * @param {string} encoded
 * @returns {InitialDraft}
 */
function readInitial(encoded) {
  /** @type {any} */
  let data = {};
  try {
    const bytes = Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0));
    data = JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    data = {};
  }
  const routes = Array.isArray(data.routes) ? data.routes : [];
  return {
    linkId: Number(data.linkId) || 0,
    version: Number(data.version) || 0,
    name: typeof data.name === "string" ? data.name : "",
    description: typeof data.description === "string" ? data.description : "",
    appended: Number(data.appended) || 0,
    routes: routes.map(/** @param {any} r */ (r) => ({
      id: Number(r.id) || 0,
      pattern: typeof r.pattern === "string" ? r.pattern : "",
      destination: typeof r.destination === "string" ? r.destination : "",
      variables: r.variables && typeof r.variables === "object" ? r.variables : {},
    })),
  };
}

start();
