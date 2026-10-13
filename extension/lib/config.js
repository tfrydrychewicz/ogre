// Where the Directory lives and the one redirect rule that sends go links to it.
//
// Everything here is pure: no `chrome.*` call and no I/O, so `node --test` can
// run it without a browser. The service worker (background.js) does the calls.

/** @typedef {chrome.declarativeNetRequest.Rule} Rule */
/** @typedef {chrome.declarativeNetRequest.UpdateRuleOptions} UpdateRuleOptions */

/**
 * The Directory a fresh install talks to: a godir started with its defaults.
 * The manifest's host permission names the same origin; keep the two in step.
 */
export const DEFAULT_DIRECTORY_URL = "http://localhost:4655";

/**
 * Id of the redirect rule. The extension owns exactly one dynamic rule, so the
 * id never changes and a rewrite always replaces the rule in place.
 */
export const RULE_ID = 1;

/**
 * Matches a go link typed with or without a scheme, and one Chrome upgraded to
 * HTTPS. Group 1 is everything after `go/`: path, query, and all.
 */
const GO_LINK_FILTER = "^https?://go/(.*)$";

/**
 * Builds the redirect rule of plan section 3.7 for one Directory.
 *
 * The substitution is the Directory's origin, a slash, and the two characters
 * backslash and 1, which Chrome replaces with group 1 of the filter. Only the
 * origin of the given URL is used: an origin cannot contain a backslash, so
 * nothing in it can be mistaken for another group reference.
 *
 * @param {string} directoryOrigin absolute http or https URL of the Directory, for example "http://localhost:4655"
 * @returns {Rule}
 * @throws {TypeError} when the value is not an absolute http or https URL
 */
export function buildRedirectRule(directoryOrigin) {
  const url = new URL(directoryOrigin);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new TypeError(`The Directory URL must start with http:// or https://, got ${JSON.stringify(directoryOrigin)}`);
  }
  return {
    id: RULE_ID,
    priority: 1,
    condition: { regexFilter: GO_LINK_FILTER, resourceTypes: ["main_frame"] },
    action: { type: "redirect", redirect: { regexSubstitution: url.origin + "/\\1" } },
  };
}

/**
 * Reports whether two rules redirect the same requests to the same place.
 *
 * It compares the fields buildRedirectRule sets and nothing else, because
 * Chrome may hand a rule back with fields the extension never wrote.
 *
 * @param {Rule} a
 * @param {Rule} b
 * @returns {boolean}
 */
export function sameRedirectRule(a, b) {
  return (
    a.id === b.id &&
    (a.priority ?? 1) === (b.priority ?? 1) &&
    a.action.type === b.action.type &&
    a.action.redirect?.regexSubstitution === b.action.redirect?.regexSubstitution &&
    a.condition.regexFilter === b.condition.regexFilter &&
    sameMembers(a.condition.resourceTypes, b.condition.resourceTypes)
  );
}

/**
 * Works out the change that leaves `wanted` as the only dynamic rule.
 *
 * Returns null when the installed rules are already right, so a browser start
 * writes nothing: dynamic rules persist across restarts (L0 spike, check 14).
 * Otherwise the change removes every installed rule and adds the wanted one.
 * The wanted id is always among the removals, so applying the same change
 * twice (install and startup can both fire) cannot fail on a duplicate id.
 *
 * @param {readonly Rule[]} installed what getDynamicRules returned
 * @param {Rule} wanted
 * @returns {UpdateRuleOptions | null}
 */
export function planRuleUpdate(installed, wanted) {
  if (installed.length === 1 && sameRedirectRule(installed[0], wanted)) return null;
  const removeRuleIds = [...new Set([wanted.id, ...installed.map((rule) => rule.id)])];
  return { removeRuleIds, addRules: [wanted] };
}

/**
 * True when both lists hold the same values, in any order.
 *
 * @param {readonly string[] | undefined} a
 * @param {readonly string[] | undefined} b
 * @returns {boolean}
 */
function sameMembers(a, b) {
  const left = [...(a ?? [])].sort();
  const right = [...(b ?? [])].sort();
  return left.length === right.length && left.every((value, index) => value === right[index]);
}
