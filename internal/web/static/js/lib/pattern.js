// @ts-check
/**
 * A pattern drawn from the tokens the server reported for it, its variables
 * highlighted. Nothing here reads a pattern (NFR-22): the server says where
 * each token starts and ends, in UTF-16 code units, and the text between the
 * tokens (the "/" between segments) is kept as it is.
 *
 * @module
 */

import { el } from "./dom.js";

/**
 * One well-formed segment of a pattern, as the server reports it (the
 * validate endpoint's routes[i].tokens, and the tokens of each route of a
 * search result or a link page).
 *
 * @typedef {object} PatternToken
 * @property {string} kind "literal" or "variable"
 * @property {number} start
 * @property {number} end
 * @property {string} [name] For a variable.
 * @property {string} [type] For a variable.
 */

/**
 * A pattern as nodes, after "go/", its variable tokens highlighted. Without
 * tokens it is plain text: the page never guesses where a variable is.
 *
 * @param {string} pattern
 * @param {PatternToken[] | null | undefined} tokens
 * @returns {(string | Node)[]}
 */
export function patternNodes(pattern, tokens) {
  /** @type {(string | Node)[]} */
  const out = ["go/"];
  if (!Array.isArray(tokens)) {
    out.push(pattern);
    return out;
  }
  let at = 0;
  for (const t of tokens) {
    if (t.start < at || t.end > pattern.length || t.start > t.end) continue;
    out.push(pattern.slice(at, t.start));
    const text = pattern.slice(t.start, t.end);
    out.push(t.kind === "variable" ? el("span", { class: "pattern-var" }, text) : text);
    at = t.end;
  }
  out.push(pattern.slice(at));
  return out.filter((part) => part !== "");
}
