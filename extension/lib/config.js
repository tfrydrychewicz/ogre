// @ts-check
// Where the Directory lives and the one redirect rule that sends go links to it.
//
// Everything here is pure: no `chrome.*` call and no I/O, so `node --test` can
// run it without a browser. The service worker (background.js) and the pages
// make the calls; lib/settings.js holds the ones they share.

/** @typedef {chrome.declarativeNetRequest.Rule} Rule */
/** @typedef {chrome.declarativeNetRequest.UpdateRuleOptions} UpdateRuleOptions */

/**
 * The Directory a fresh install talks to: an Ogre server started with its
 * defaults. The manifest's host permission names the same origin;
 * REQUIRED_HOST_PATTERNS below repeats it, and manifest.test.js checks that
 * the three agree.
 */
export const DEFAULT_DIRECTORY_URL = "http://localhost:4655";

/**
 * The manifest's host_permissions, exactly. Access to these is granted at
 * install and can never be released, so a Directory at one of them needs no
 * optional access, and releasing access never names them.
 */
export const REQUIRED_HOST_PATTERNS = Object.freeze(["*://go/*", `${DEFAULT_DIRECTORY_URL}/*`]);

/**
 * The keys this extension keeps in chrome.storage.local. Nothing else is
 * stored, and nothing is stored anywhere else (no sync storage: the Directory
 * URL is a property of this machine, typically localhost).
 */
export const STORAGE_KEYS = Object.freeze({
  /** The Directory's origin, as normalizeDirectoryUrl returns it. Absent until the user saves one. */
  directoryUrl: "directoryUrl",
  /** true once the first-run page is finished or dismissed; it then never opens by itself again. */
  firstRunDone: "firstRunDone",
  /** true once the first-run page saw a go/ navigation, so a reopened page shows that step as done. */
  firstRunSawGo: "firstRunSawGo",
});

/**
 * The keys this extension keeps in chrome.storage.session, which lives in
 * memory and is gone when the browser exits.
 */
export const SESSION_KEYS = Object.freeze({
  /**
   * When the worker finished handling the last onInstalled event, in
   * milliseconds since the epoch: the redirect rule checked and, on a fresh
   * install, the first-run page opened or found not needed. The end-to-end
   * tests wait for it rather than guess how long that takes.
   */
  installHandled: "installHandled",
});

/** The first-run page, relative to the extension's root. The service worker opens it once, on install. */
export const FIRST_RUN_PAGE = "onboarding/onboarding.html";

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
 * The part of a navigation's URL after `go/`, when the navigation is a go
 * link: the same test the redirect rule applies, so the first-run page counts
 * exactly the navigations the rule redirects.
 *
 * @param {string} url a URL as Chrome reports it, such as "http://go/roadmap"
 * @returns {string | null} "" for go/ itself, "roadmap" for go/roadmap, null for anything else
 */
export function goLinkPath(url) {
  const match = new RegExp(GO_LINK_FILTER).exec(url);
  return match ? match[1] : null;
}

/** The longest Directory URL accepted, as typed. Far beyond any real origin. */
const MAX_INPUT_LENGTH = 2048;

/**
 * @typedef {object} DirectoryUrlOk
 * @property {true} ok
 * @property {string} origin the normalized origin: scheme, lowercase host (punycode for an
 *   international name), and the port unless it is the scheme's default. No trailing slash.
 */

/**
 * @typedef {object} DirectoryUrlError
 * @property {false} ok
 * @property {DirectoryUrlErrorCode} code stable, for tests and for code that must tell the cases apart
 * @property {string} message one or two sentences to show next to the field
 * @property {string} [suggestion] a valid Directory URL the user probably meant; only for a missing scheme
 */

/**
 * @typedef {"empty" | "too_long" | "whitespace" | "missing_scheme" | "scheme" | "credentials"
 *   | "path" | "invalid" | "port" | "host" | "go_host"} DirectoryUrlErrorCode
 */

/** @typedef {DirectoryUrlOk | DirectoryUrlError} DirectoryUrlResult */

/**
 * Checks what a user typed as the Directory URL and reduces it to an origin.
 *
 * Why it is strict: the URL becomes the substitution of the redirect rule,
 * and Chrome silently does nothing with a rule whose substituted URL is
 * invalid (PRD Appendix B.2). Every go link would then fail with no message.
 * So anything that is not plainly "an http or https server, by its address"
 * is refused here, with a sentence that says what to change, instead of being
 * guessed at.
 *
 * Accepted: `http://` or `https://` (any case), a host, an optional port, and
 * an optional single trailing `/`. Surrounding spaces are ignored. The result
 * is the origin as the browser's URL parser writes it: lowercase, default
 * port dropped, an international name in punycode, a numeric IPv4 address in
 * dotted form.
 *
 * Refused, each with its own code:
 * - no scheme (`localhost:4655`): refused rather than given `http://`,
 *   because for a server elsewhere the right scheme may well be https. The
 *   message, and `suggestion`, name the http form when it would be valid;
 *   when even that would be refused (`go`), the reason it would be is given;
 * - any scheme but http and https (`javascript:`, `data:`, `file:`, `ftp:`);
 * - a user name or password, even an empty one (`http://@host`);
 * - a path, query or fragment beyond one `/`: the redirect appends the go
 *   link's own path to the origin, and a Directory below a path is not
 *   supported;
 * - whitespace or control characters inside, which the URL parser would
 *   silently drop or reject;
 * - port 0, and anything the URL parser refuses (port 65536 and up);
 * - a host with characters a DNS name or an IP address cannot have, an empty
 *   label (`a..b`) or a trailing dot (`localhost.`, which is a different
 *   origin from `localhost` to the browser and to the Directory's host check);
 * - the host `go`, on any port: the rule would send `go/x` to `http://go/x`,
 *   which the same rule matches again.
 *
 * @param {string} input what the user typed
 * @returns {DirectoryUrlResult}
 */
export function normalizeDirectoryUrl(input) {
  const text = String(input).trim();
  if (text === "") return refuse("empty", "Enter the address of your Ogre server, for example http://localhost:4655.");
  if (text.length > MAX_INPUT_LENGTH) return refuse("too_long", "This address is too long to be a server's address.");
  // Unicode-aware: \s covers the spaces String.trim removes, \p{Cc} the
  // control characters (tab, newline) and \p{Cf} the invisible format
  // characters (a zero-width space), which the URL parser would drop silently.
  if (/[\s\p{Cc}\p{Cf}]/u.test(text)) return refuse("whitespace", "Remove the spaces, line breaks or other invisible characters inside the address.");

  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(text)?.[1].toLowerCase();
  if (!/^https?:\/\//i.test(text)) {
    if (scheme !== undefined && /^[a-z][a-z0-9+.-]*:\/\//i.test(text)) {
      return refuse("scheme", `The address must start with http:// or https://, not ${scheme}://.`);
    }
    // A port alone, "4771", as someone told "it runs on port 4771" types
    // it. The URL parser would read it as the IPv4 address 0.0.18.163.
    const port = /^:?([0-9]{1,5})\/?$/.exec(text)?.[1];
    if (port !== undefined) {
      if (Number(port) < 1 || Number(port) > 65535) return refuse("port", "The port must be a number from 1 to 65535.");
      // The default Directory's host, with this port.
      const origin = DEFAULT_DIRECTORY_URL.replace(/:[0-9]+$/, `:${Number(port)}`);
      return refuse("missing_scheme", `Give the whole address, not only the port: for example ${origin}.`, origin);
    }
    // "localhost:4655", "example.com", "[::1]:4655", each perhaps with a
    // trailing slash: a host and port with the scheme left out.
    if (/^(\[[0-9a-f:.]*\]|[^/?#@:[\]\\]+)(:[0-9]*)?\/?$/i.test(text)) {
      const guess = normalizeDirectoryUrl(`http://${text}`);
      if (guess.ok) {
        return refuse(
          "missing_scheme",
          `Start the address with http:// or https://, for example ${guess.origin}.`,
          guess.origin,
        );
      }
      // With a scheme it would still be wrong ("go", "localhost:0"): say
      // that instead, so fixing one problem does not reveal another.
      if (guess.code !== "invalid") return guess;
      return refuse("missing_scheme", "Start the address with http:// or https://.");
    }
    if (scheme !== undefined && scheme !== "http" && scheme !== "https") {
      return refuse("scheme", `The address must start with http:// or https://, not ${scheme}:.`);
    }
    // "http:localhost", "https:/example.com", "http:\\host": the parser
    // would accept these, but nobody types them on purpose.
    return refuse("scheme", "The address must start with http:// or https://, with both slashes.");
  }

  // The parts as typed, before the parser normalizes anything away: an
  // empty user name, a lone "?" or "#", and "/." all vanish in parsing.
  const afterScheme = text.slice(text.indexOf("://") + 3);
  const authorityEnd = afterScheme.search(/[/?#\\]/);
  const authority = authorityEnd === -1 ? afterScheme : afterScheme.slice(0, authorityEnd);
  const rest = authorityEnd === -1 ? "" : afterScheme.slice(authorityEnd);
  if (authority.includes("@")) {
    return refuse("credentials", "Leave out the user name and password: the address is only the server's, such as http://localhost:4655.");
  }
  if (rest !== "" && rest !== "/") {
    return refuse("path", "Leave out everything after the server's name and port: the address is only the server's, such as http://localhost:4655, with no path.");
  }

  /** @type {URL} */
  let url;
  try {
    url = new URL(text);
  } catch {
    // A host followed by a port that is too large is the one failure worth
    // naming precisely; everything else is just not an address.
    const port = /^(?:\[[^\]]*\]|[^:]+):([0-9]+)$/.exec(authority)?.[1];
    if (port !== undefined && Number(port) > 65535) return refuse("port", "The port must be a number from 1 to 65535.");
    return refuse("invalid", "This is not a valid web address.");
  }
  if (url.port === "0") return refuse("port", "The port must be a number from 1 to 65535.");

  const host = url.hostname;
  if (!isPlainHost(host)) {
    if (host.endsWith(".")) return refuse("host", "Remove the dot at the end of the server's name.");
    if (host.split(".").some((label) => label.length > 63)) {
      return refuse("host", "Each part of the server's name, between dots, can be at most 63 characters long.");
    }
    return refuse("host", "The server's name may contain only letters, digits, hyphens and dots, or be an IP address.");
  }
  if (host === "go") {
    return refuse("go_host", "The Directory cannot be at the host go: every go link would be sent back to itself. Use the Ogre server's own address, such as http://localhost:4655.");
  }
  return { ok: true, origin: url.origin };
}

/**
 * True for a host as the URL parser writes it that is safe to put into the
 * redirect rule and a host permission: DNS labels of letters, digits and
 * hyphens (punycode included), a dotted IPv4 address, or an IPv6 literal in
 * brackets. The parser itself allows more, such as `$` or `_`, which Chrome's
 * own URL code and match patterns may treat differently.
 *
 * @param {string} host
 * @returns {boolean}
 */
function isPlainHost(host) {
  if (/^\[[0-9a-f:.]+\]$/.test(host)) return true;
  return host.split(".").every((label) => /^[a-z0-9-]{1,63}$/.test(label));
}

/**
 * @param {DirectoryUrlErrorCode} code
 * @param {string} message
 * @param {string} [suggestion]
 * @returns {DirectoryUrlError}
 */
function refuse(code, message, suggestion) {
  return suggestion === undefined ? { ok: false, code, message } : { ok: false, code, message, suggestion };
}

/**
 * The host permission pattern that covers one Directory: its origin and every
 * path. A port is part of the pattern, so access to one port is not access to
 * another.
 *
 * @param {string} origin a normalized origin, as normalizeDirectoryUrl returns it
 * @returns {string} for example "http://localhost:4655/*"
 */
export function hostPattern(origin) {
  return `${origin}/*`;
}

/**
 * Reports whether talking to this Directory needs host access that the
 * manifest does not already grant.
 *
 * @param {string} origin a normalized origin
 * @returns {boolean}
 */
export function needsOptionalAccess(origin) {
  return !REQUIRED_HOST_PATTERNS.includes(hostPattern(origin));
}

/**
 * Works out which optional host access to release once `origin` is the
 * Directory: every origin the user granted that is not the new Directory's.
 * That is the previous Directory, and any origin granted for a connection
 * test that was never saved. The manifest's own hosts are never listed:
 * Chrome refuses to remove a required permission.
 *
 * @param {readonly string[]} granted the origins chrome.permissions.getAll reports
 * @param {string} origin the Directory now in use
 * @param {readonly string[]} [required] the manifest's host_permissions as the running
 *   extension reports them; one of them in the list would make Chrome refuse the whole removal
 * @returns {string[]} patterns to pass to chrome.permissions.remove; empty when there is nothing to release
 */
export function originsToRelease(granted, origin, required = REQUIRED_HOST_PATTERNS) {
  const keep = hostPattern(origin);
  return granted.filter((pattern) => pattern !== keep && !required.includes(pattern) && !REQUIRED_HOST_PATTERNS.includes(pattern));
}

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
 * Otherwise the change removes every installed rule and adds the wanted one,
 * in one call, so there is never a moment with no rule or with two. The
 * wanted id is always among the removals, so applying the same change twice
 * (install and startup can both fire) cannot fail on a duplicate id.
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
