// @ts-check
/**
 * The variables table of one route (VAR-3, VAR-4): per variable a type
 * selector, a description and an example, and for a regular expression the
 * expression in its own field with a sample tester.
 *
 * The table shows what the server says the pattern holds, and reports what
 * the user changes to its owner (form.js), which rewrites the variable in
 * the pattern and asks the server again. Whether a sample value passes is
 * the server's answer, from its own engine; nothing is matched here.
 *
 * Rows are kept by variable name across answers, so a field keeps focus and
 * what is typed in it while the answers come and go.
 *
 * @module
 */

import { el, replace, setAttr } from "../lib/dom.js";

/**
 * A variable as the table shows it.
 *
 * @typedef {object} TableVariable
 * @property {string} name
 * @property {string} type
 * @property {string} accepts What the type accepts, in words; "" when the
 *   server has not read the variable yet.
 * @property {string} expression The expression of a regex variable.
 * @property {boolean} pending The server cannot read the variable as it is
 *   written now (an expression being typed, say); the row stays so that the
 *   user can finish.
 * @property {{ message: string, severity: string }[]} problems The pattern's
 *   problems that lie within this variable.
 * @property {Record<string, boolean> | undefined} samples The server's verdict
 *   on each sample value.
 * @property {Record<string, string> | undefined} sampleReasons The server's
 *   sentence for each refused sample value.
 * @property {string} sampleText The sample values as typed, one per line.
 * @property {string} description
 * @property {string} example
 */

/**
 * @typedef {object} TableHandlers
 * @property {(name: string, type: string) => void} type
 * @property {(name: string, expression: string) => void} expression
 * @property {(name: string, text: string) => void} samples
 * @property {(name: string, field: "description" | "example", value: string) => void} meta
 */

/**
 * @typedef {object} VariableRow
 * @property {HTMLFieldSetElement} root
 * @property {HTMLSelectElement} type
 * @property {HTMLElement} regex
 * @property {HTMLInputElement} expression
 * @property {HTMLElement} expressionMessages
 * @property {HTMLTextAreaElement} samples
 * @property {HTMLElement} sampleResults
 * @property {HTMLInputElement} description
 * @property {HTMLInputElement} example
 * @property {HTMLElement} accepts
 */

/**
 * The sample values of a text: one per line, without empty lines and
 * repeats. A value is kept exactly as typed otherwise: values are never
 * trimmed (PRD 5.3).
 *
 * @param {string} text
 * @returns {string[]}
 */
export function sampleValues(text) {
  return [...new Set(text.split(/\r?\n/).filter((line) => line !== ""))];
}

let tables = 0;

/**
 * Builds an empty table; update() fills it.
 *
 * @param {{ typeOptions: HTMLOptionElement[], handlers: TableHandlers }} options
 * @returns {{ root: HTMLElement, update: (variables: TableVariable[]) => void }}
 */
export function variablesTable({ typeOptions, handlers }) {
  const prefix = `vars-${++tables}`;
  const list = el("div", { class: "variable-list" });
  const root = el(
    "div",
    { class: "variables", hidden: true },
    el("h4", { class: "variables-title" }, "Variables"),
    el("p", { class: "field-help" }, "A type you choose here is written into the pattern for you."),
    list,
  );
  /** @type {Map<string, VariableRow>} */
  const rows = new Map();
  let made = 0;

  /**
   * @param {string} name
   * @returns {VariableRow}
   */
  const makeRow = (name) => {
    const id = `${prefix}-${++made}`;
    const type = el(
      "select",
      { class: "select", id: `${id}-type`, on: { change: () => handlers.type(name, type.value) } },
      typeOptions.map((o) => /** @type {HTMLOptionElement} */ (o.cloneNode(true))),
    );
    const expressionMessages = el("ul", { class: "field-messages", id: `${id}-expression-msgs` });
    const expression = el("input", {
      class: "input input-mono",
      id: `${id}-expression`,
      type: "text",
      autocomplete: "off",
      autocapitalize: "none",
      spellcheck: "false",
      placeholder: "[0-9]+",
      "aria-describedby": `${id}-expression-help ${id}-expression-msgs`,
      on: { input: () => handlers.expression(name, expression.value) },
    });
    const sampleResults = el("ul", { class: "sample-results", id: `${id}-sample-results`, "aria-live": "polite" });
    const samples = el("textarea", {
      class: "textarea input-mono sample-input",
      id: `${id}-samples`,
      rows: 3,
      autocomplete: "off",
      autocapitalize: "none",
      spellcheck: "false",
      "aria-describedby": `${id}-samples-help ${id}-sample-results`,
      on: { input: () => handlers.samples(name, samples.value) },
    });
    const description = el("input", {
      class: "input",
      id: `${id}-description`,
      type: "text",
      autocomplete: "off",
      "aria-describedby": `${id}-description-help`,
      on: { input: () => handlers.meta(name, "description", description.value) },
    });
    const example = el("input", {
      class: "input",
      id: `${id}-example`,
      type: "text",
      autocomplete: "off",
      autocapitalize: "none",
      spellcheck: "false",
      "aria-describedby": `${id}-example-help`,
      on: { input: () => handlers.meta(name, "example", example.value) },
    });
    const accepts = el("p", { class: "field-help variable-accepts", id: `${id}-accepts` });
    type.setAttribute("aria-describedby", accepts.id);
    const regex = el(
      "div",
      { class: "variable-regex", hidden: true },
      el(
        "div",
        { class: "field" },
        el("label", { for: expression.id }, "Regular expression"),
        expression,
        el("p", { class: "field-help", id: `${id}-expression-help` }, "It must match the whole value. Add (?i) at the start to ignore case."),
        expressionMessages,
      ),
      el(
        "div",
        { class: "field" },
        el("label", { for: samples.id }, "Try values"),
        samples,
        el("p", { class: "field-help", id: `${id}-samples-help` }, "One value per line; the Directory checks each one with its own engine."),
        sampleResults,
      ),
    );
    const root = el(
      "fieldset",
      { class: "variable" },
      el("legend", { class: "variable-name" }, el("span", { class: "visually-hidden" }, "Variable "), el("span", { class: "pattern-var" }, `{${name}}`)),
      el(
        "div",
        { class: "variable-grid" },
        // "Type of {day}": each variable's selector has a name of its own.
        el("div", { class: "field" }, el("label", { for: type.id }, "Type", el("span", { class: "visually-hidden" }, ` of {${name}}`)), type, accepts),
        el(
          "div",
          { class: "field" },
          el("label", { for: description.id }, "Description ", el("span", { class: "muted" }, "(optional)")),
          description,
          el("p", { class: "field-help", id: `${id}-description-help` }, "What the value means, for people who fill it in."),
        ),
        el(
          "div",
          { class: "field" },
          el("label", { for: example.id }, "Example ", el("span", { class: "muted" }, "(optional)")),
          example,
          el("p", { class: "field-help", id: `${id}-example-help` }, "A value it accepts, shown as a hint."),
        ),
      ),
      regex,
    );
    return { root, type, regex, expression, expressionMessages, samples, sampleResults, description, example, accepts };
  };

  /**
   * @param {VariableRow} row
   * @param {TableVariable} v
   */
  const fill = (row, v) => {
    row.type.value = v.type;
    const isRegex = v.type === "regex";
    row.regex.hidden = !isRegex;
    if (isRegex && document.activeElement !== row.expression && row.expression.value !== v.expression) {
      row.expression.value = v.expression;
    }
    setValue(row.samples, v.sampleText);
    setValue(row.description, v.description);
    setValue(row.example, v.example);
    replace(row.accepts, v.accepts ? `Accepts ${v.accepts}.` : v.pending ? "Not checked yet: the pattern has a problem here." : "");

    const errors = v.problems.filter((p) => p.severity === "error");
    setAttr(row.expression, "aria-invalid", isRegex && errors.length > 0 ? "true" : null);
    replace(row.expressionMessages, isRegex ? v.problems.map((p) => messageItem(p.severity, p.message)) : []);

    const values = sampleValues(v.sampleText);
    replace(
      row.sampleResults,
      values.map((value) => {
        const verdict = v.samples ? v.samples[value] : undefined;
        const label = verdict === true ? "Passes" : verdict === false ? "Fails" : "Not checked";
        const kind = verdict === true ? "sample-pass" : verdict === false ? "sample-fail" : "sample-unknown";
        const why = verdict === false && v.sampleReasons ? v.sampleReasons[value] : undefined;
        return el(
          "li",
          { class: `sample ${kind}` },
          el("span", { class: "sample-verdict" }, label),
          " ",
          el("bdi", { class: "mono" }, value),
          why ? el("span", { class: "sample-reason" }, " · ", why) : null,
        );
      }),
    );
  };

  return {
    root,
    update(variables) {
      root.hidden = variables.length === 0;
      const wanted = new Set(variables.map((v) => v.name));
      for (const [name, row] of rows) {
        if (!wanted.has(name)) {
          row.root.remove();
          rows.delete(name);
        }
      }
      variables.forEach((v, i) => {
        let row = rows.get(v.name);
        if (!row) {
          row = makeRow(v.name);
          rows.set(v.name, row);
        }
        fill(row, v);
        // Moved only when out of place: moving a node takes focus from it.
        if (list.children[i] !== row.root) list.insertBefore(row.root, list.children[i] || null);
      });
    },
  };
}

/**
 * Sets a field's value unless it already holds it: assigning the same value
 * would move the caret to the end while the user types.
 *
 * @param {HTMLInputElement | HTMLTextAreaElement} field
 * @param {string} value
 */
function setValue(field, value) {
  if (field.value !== value) field.value = value;
}

/**
 * One message beside a field. Its kind is in its words, not only in its
 * color.
 *
 * @param {string} severity
 * @param {string} message
 * @returns {HTMLLIElement}
 */
export function messageItem(severity, message) {
  const error = severity === "error";
  return el(
    "li",
    { class: error ? "field-error" : "field-warning" },
    el("span", { class: "message-kind" }, error ? "Error:" : "Warning:"),
    " ",
    message,
  );
}
