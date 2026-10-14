// @ts-check
/**
 * The pattern field (VAR-2): a monospace input whose own text is transparent,
 * over a mirror element that draws the same text in the colors of the tokens
 * the server reported, with the place of each error underlined.
 *
 * Plan section 3.6 allows two ways to show tokens: this overlay, or the
 * tokens drawn beneath the field. The overlay was chosen after trying it in
 * Chromium: both elements use the same font, padding and border, the mirror
 * never wraps (white-space: pre) and follows the input's horizontal scroll,
 * so every character of the mirror sits under the same character of the
 * input. What could break that (a different border width when the field is
 * invalid, bold text in a token) is kept equal or avoided in form.css. The
 * input stays an ordinary input: typing, selecting, pasting, undo and screen
 * readers work on it unchanged, and the mirror is hidden from assistive
 * technology.
 *
 * @module
 */

import { el, append, clear } from "../lib/dom.js";
import { fits } from "./spans.js";

/**
 * A token of the validate answer: a well-formed segment of the pattern.
 *
 * @typedef {{ kind: string, start: number, end: number, name?: string, type?: string }} Token
 */

/**
 * A problem of the validate answer.
 *
 * @typedef {{ severity: string, field: string, code: string, message: string, start: number, end: number }} Problem
 */

/**
 * @typedef {object} PatternEditor
 * @property {HTMLDivElement} root The element to place in the page.
 * @property {HTMLInputElement} input
 * @property {(tokens: Token[], errors: Problem[]) => void} paint Draws the
 *   input's current text with these tokens and error marks.
 * @property {(invalid: boolean) => void} setInvalid Marks the field invalid
 *   (aria-invalid), keeping the mirror's border equal to the input's.
 */

/**
 * Builds the pattern field.
 *
 * @param {{ id: string, value: string, describedBy: string }} options
 * @returns {PatternEditor}
 */
export function patternEditor({ id, value, describedBy }) {
  const mirror = el("div", { class: "input input-mono pattern-mirror", "aria-hidden": "true" });
  const input = el("input", {
    class: "input input-mono pattern-input",
    id,
    type: "text",
    value,
    autocomplete: "off",
    autocapitalize: "none",
    spellcheck: "false",
    placeholder: "reports/{month:int}/{day:int}",
    "aria-describedby": describedBy,
  });
  const editor = el("div", { class: "pattern-editor" }, mirror, input);
  const root = el("div", { class: "pattern-box" }, el("span", { class: "pattern-prefix", "aria-hidden": "true" }, "go/"), editor);

  // The mirror follows the input's horizontal scroll. An input scrolls on
  // its own as the caret moves; there is no single event for that, so every
  // event that can move the caret or the view schedules a sync.
  let frame = 0;
  const sync = () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      mirror.scrollLeft = input.scrollLeft;
    });
  };
  for (const event of ["input", "scroll", "keydown", "keyup", "pointerdown", "pointerup", "pointermove", "select", "focus", "blur"]) {
    input.addEventListener(event, sync);
  }
  document.addEventListener("selectionchange", () => {
    if (document.activeElement === input) sync();
  });
  window.addEventListener("resize", sync);

  /** @type {PatternEditor} */
  const editorAPI = {
    root,
    input,
    paint(tokens, errors) {
      paintMirror(mirror, input.value, tokens, errors);
      mirror.scrollLeft = input.scrollLeft;
    },
    setInvalid(invalid) {
      if (invalid) input.setAttribute("aria-invalid", "true");
      else input.removeAttribute("aria-invalid");
      editor.classList.toggle("is-invalid", invalid);
    },
  };
  editorAPI.paint([], []);
  return editorAPI;
}

/**
 * Draws text into the mirror: the text between tokens plain, literal and
 * variable tokens in their classes, and error ranges underlined. An error
 * with an empty range (a missing "}" at the end, say) is drawn as a mark
 * between two characters.
 *
 * Only ranges that fit the text are used. The text is always the input's
 * own, so a stale range can at worst color the wrong characters, never hide
 * or add one.
 *
 * @param {HTMLElement} mirror
 * @param {string} text
 * @param {Token[]} tokens
 * @param {Problem[]} errors
 */
export function paintMirror(mirror, text, tokens, errors) {
  const length = text.length;
  const okTokens = tokens.filter((t) => fits(t, length) && t.start < t.end);
  const okErrors = errors.filter((p) => fits(p, length));
  // Cut the text at every boundary of a token or an error, then give each
  // piece the classes of what covers it.
  const cuts = new Set([0, length]);
  for (const t of okTokens) {
    cuts.add(t.start);
    cuts.add(t.end);
  }
  for (const p of okErrors) {
    cuts.add(p.start);
    cuts.add(p.end);
  }
  const points = [...cuts].sort((a, b) => a - b);
  clear(mirror);
  for (let i = 0; i < points.length; i++) {
    const at = points[i];
    if (okErrors.some((p) => p.start === at && p.end === at)) {
      append(mirror, el("span", { class: "pattern-mark" }));
    }
    if (i + 1 >= points.length) break;
    const end = points[i + 1];
    if (end <= at) continue;
    const piece = text.slice(at, end);
    const token = okTokens.find((t) => t.start <= at && end <= t.end);
    const error = okErrors.some((p) => p.start <= at && end <= p.end && p.start < p.end);
    /** @type {string[]} */
    const classes = [];
    if (token) classes.push(token.kind === "variable" ? "tok-var" : "tok-literal");
    else classes.push("tok-other");
    if (error) classes.push("tok-error");
    append(mirror, el("span", { class: classes.join(" ") }, piece));
  }
  // A trailing space gives the mirror the room an input keeps for the
  // caret after the last character, so both scroll the same distance.
  append(mirror, " ");
}
