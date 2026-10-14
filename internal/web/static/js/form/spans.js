// @ts-check
/**
 * Offsets into a pattern that is being edited.
 *
 * The server reports where each token, variable and problem of a pattern is,
 * as UTF-16 code units, which are JavaScript string indices. Its answer
 * arrives after the user has typed on, so until the next answer the form
 * keeps the old one in step with the text: what lies before an edit stays,
 * what lies after it moves by the edit's length, and what the edit touched is
 * dropped until the server reads it again. Nothing here knows what a pattern
 * means; it only moves ranges along with the characters they cover.
 *
 * @module
 */

/**
 * A change of a text: the characters [start, oldEnd) of the old text became
 * [start, newEnd) of the new one.
 *
 * @typedef {{ start: number, oldEnd: number, newEnd: number }} Edit
 */

/**
 * A range of the text: [start, end).
 *
 * @typedef {{ start: number, end: number }} Span
 */

/**
 * The edit that turns before into after: the common beginning and end are
 * kept, the middle is what changed.
 *
 * @param {string} before
 * @param {string} after
 * @returns {Edit}
 */
export function diff(before, after) {
  const limit = Math.min(before.length, after.length);
  let start = 0;
  while (start < limit && before.charCodeAt(start) === after.charCodeAt(start)) start++;
  let tail = 0;
  while (
    tail < limit - start &&
    before.charCodeAt(before.length - 1 - tail) === after.charCodeAt(after.length - 1 - tail)
  ) {
    tail++;
  }
  return { start, oldEnd: before.length - tail, newEnd: after.length - tail };
}

/**
 * Where a range of the old text is in the new one, or null when the edit
 * changed part of it.
 *
 * A range that ends where an insertion begins, or begins where it ends, is
 * untouched: typing right after a variable's "}" does not change the
 * variable.
 *
 * @template {Span} T
 * @param {T} span
 * @param {Edit} edit
 * @returns {T | null}
 */
export function moveSpan(span, edit) {
  const delta = edit.newEnd - edit.oldEnd;
  if (span.end <= edit.start) return span;
  if (span.start >= edit.oldEnd) {
    return { ...span, start: span.start + delta, end: span.end + delta };
  }
  return null;
}

/**
 * moveSpan for a list; the ranges an edit touched are left out.
 *
 * @template {Span} T
 * @param {T[]} spans
 * @param {Edit} edit
 * @returns {T[]}
 */
export function moveSpans(spans, edit) {
  /** @type {T[]} */
  const out = [];
  for (const span of spans) {
    const moved = moveSpan(span, edit);
    if (moved) out.push(moved);
  }
  return out;
}

/**
 * Whether a range is a valid range of a text of the given length.
 *
 * @param {Span} span
 * @param {number} length
 * @returns {boolean}
 */
export function fits(span, length) {
  return Number.isInteger(span.start) && Number.isInteger(span.end) && span.start >= 0 && span.start <= span.end && span.end <= length;
}
