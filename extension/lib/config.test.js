// @ts-check
// Unit tests for the pure extension logic. Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  DEFAULT_DIRECTORY_URL,
  FIRST_RUN_PAGE,
  REQUIRED_HOST_PATTERNS,
  RULE_ID,
  SESSION_KEYS,
  STORAGE_KEYS,
  buildRedirectRule,
  goLinkPath,
  hostPattern,
  needsOptionalAccess,
  normalizeDirectoryUrl,
  originsToRelease,
  planRuleUpdate,
  sameRedirectRule,
} from "./config.js";

test("the default Directory is an Ogre server started with its defaults", () => {
  assert.equal(DEFAULT_DIRECTORY_URL, "http://localhost:4655");
  assert.equal(RULE_ID, 1);
});

test("buildRedirectRule returns the rule of plan section 3.7", () => {
  assert.deepEqual(buildRedirectRule(DEFAULT_DIRECTORY_URL), {
    id: 1,
    priority: 1,
    condition: { regexFilter: "^https?://go/(.*)$", resourceTypes: ["main_frame"] },
    action: { type: "redirect", redirect: { regexSubstitution: "http://localhost:4655/\\1" } },
  });
});

test("the substitution is the origin, a slash, a backslash, and the digit 1", () => {
  const substitution = buildRedirectRule(DEFAULT_DIRECTORY_URL).action.redirect?.regexSubstitution ?? "";
  const origin = "http://localhost:4655";

  assert.equal(substitution.length, origin.length + 3);
  assert.equal(substitution.length, 24);
  assert.equal(substitution.slice(0, origin.length), origin);
  // Character codes, so that no escaping rule of this file can hide a mistake.
  assert.deepEqual(
    [...substitution.slice(origin.length)].map((character) => character.charCodeAt(0)),
    [0x2f, 0x5c, 0x31],
  );
});

test("the filter captures everything after go/ for http and https", () => {
  const filter = buildRedirectRule(DEFAULT_DIRECTORY_URL).condition.regexFilter ?? "";
  const pattern = new RegExp(filter);
  /** @type {[string, string | null][]} */
  const cases = [
    ["http://go/roadmap", "roadmap"],
    ["https://go/roadmap", "roadmap"],
    ["http://go/", ""],
    ["http://go/gh/api-server/pr/12?tab=files#top", "gh/api-server/pr/12?tab=files#top"],
    ["http://go/a%20b%2Fc", "a%20b%2Fc"],
    ["http://go", null],
    ["http://golang.org/doc", null],
    ["http://example.com/http://go/x", null],
    ["ftp://go/x", null],
  ];
  for (const [url, captured] of cases) {
    const match = pattern.exec(url);
    assert.equal(match ? match[1] : null, captured, url);
  }
});

test("buildRedirectRule uses only the origin of the Directory URL", () => {
  /** @type {[string, string][]} */
  const cases = [
    ["http://localhost:4655", "http://localhost:4655/\\1"],
    ["http://localhost:4655/", "http://localhost:4655/\\1"],
    ["http://localhost:4655/-/", "http://localhost:4655/\\1"],
    ["https://go.example.com", "https://go.example.com/\\1"],
    ["HTTPS://Go.Example.com:443", "https://go.example.com/\\1"],
    ["http://[::1]:4655", "http://[::1]:4655/\\1"],
  ];
  for (const [input, substitution] of cases) {
    assert.equal(buildRedirectRule(input).action.redirect?.regexSubstitution, substitution, input);
  }
});

test("buildRedirectRule refuses anything that is not an absolute http or https URL", () => {
  for (const input of ["", "localhost:4655", "go", "ftp://example.com", "javascript:alert(1)", "chrome://settings"]) {
    assert.throws(() => buildRedirectRule(input), TypeError, JSON.stringify(input));
  }
});

test("sameRedirectRule detects a different origin", () => {
  const local = buildRedirectRule("http://localhost:4655");

  assert.equal(sameRedirectRule(local, buildRedirectRule("http://localhost:4655")), true);
  assert.equal(sameRedirectRule(local, buildRedirectRule("http://localhost:4656")), false);
  assert.equal(sameRedirectRule(local, buildRedirectRule("https://localhost:4655")), false);
  assert.equal(sameRedirectRule(local, buildRedirectRule("https://go.example.com")), false);
});

test("sameRedirectRule compares every field the rule sets, and only those", () => {
  const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
  /** @type {[string, chrome.declarativeNetRequest.Rule, boolean][]} */
  const cases = [
    ["identical", structuredClone(wanted), true],
    // Chrome may hand a rule back with fields the extension never wrote.
    ["extra condition field", { ...wanted, condition: { ...wanted.condition, isUrlFilterCaseSensitive: false } }, true],
    ["priority left out, which means 1", { id: wanted.id, condition: wanted.condition, action: wanted.action }, true],
    ["other id", { ...wanted, id: 2 }, false],
    ["other priority", { ...wanted, priority: 2 }, false],
    ["other filter", { ...wanted, condition: { ...wanted.condition, regexFilter: "^http://go/(.*)$" } }, false],
    ["more resource types", { ...wanted, condition: { ...wanted.condition, resourceTypes: ["main_frame", "sub_frame"] } }, false],
    ["no resource types", { ...wanted, condition: { regexFilter: wanted.condition.regexFilter } }, false],
    ["other action", { ...wanted, action: { type: "block" } }, false],
    ["redirect to a fixed URL", { ...wanted, action: { type: "redirect", redirect: { url: "http://localhost:4655/" } } }, false],
  ];
  for (const [name, installed, same] of cases) {
    assert.equal(sameRedirectRule(installed, wanted), same, name);
    assert.equal(sameRedirectRule(wanted, installed), same, `${name}, reversed`);
  }
});

test("planRuleUpdate writes nothing when the rule is already installed", () => {
  const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
  assert.equal(planRuleUpdate([structuredClone(wanted)], wanted), null);
});

test("planRuleUpdate leaves the wanted rule as the only one", () => {
  const wanted = buildRedirectRule(DEFAULT_DIRECTORY_URL);
  const elsewhere = buildRedirectRule("http://localhost:4656");
  /** @type {chrome.declarativeNetRequest.Rule} */
  const stray = { id: 7, condition: { urlFilter: "||example.com" }, action: { type: "block" } };
  /** @type {[string, chrome.declarativeNetRequest.Rule[], number[]][]} */
  const cases = [
    ["nothing installed", [], [1]],
    ["rule points at another Directory", [elsewhere], [1]],
    ["a stray rule beside the right one", [wanted, stray], [1, 7]],
    ["only a stray rule", [stray], [1, 7]],
  ];
  for (const [name, installed, removeRuleIds] of cases) {
    assert.deepEqual(planRuleUpdate(installed, wanted), { removeRuleIds, addRules: [wanted] }, name);
  }
});

// ---------------------------------------------------------------------------
// The Directory URL a user types (plan L4.1)

/** @typedef {import("./config.js").DirectoryUrlErrorCode} Code */

test("normalizeDirectoryUrl reduces an accepted address to its origin", () => {
  /** @type {[string, string][]} what the user typed, and the origin it becomes */
  const cases = [
    ["http://localhost:4655", "http://localhost:4655"],
    ["http://localhost:4655/", "http://localhost:4655"],
    ["  http://localhost:4655/  ", "http://localhost:4655"],
    ["\u00a0http://localhost:4655\u3000", "http://localhost:4655"],
    ["HTTP://LOCALHOST:4655", "http://localhost:4655"],
    ["https://Go.Example.com:443/", "https://go.example.com"],
    ["http://localhost:80", "http://localhost"],
    ["https://localhost:443", "https://localhost"],
    ["http://localhost:", "http://localhost"],
    ["http://example.com:08080", "http://example.com:8080"],
    ["http://localhost:65535", "http://localhost:65535"],
    ["http://127.0.0.1:4655", "http://127.0.0.1:4655"],
    ["http://127.1:4655", "http://127.0.0.1:4655"],
    ["http://0x7f.0.0.1:4655", "http://127.0.0.1:4655"],
    ["http://[::1]:4655", "http://[::1]:4655"],
    ["http://[::FFFF:127.0.0.1]:4655", "http://[::ffff:7f00:1]:4655"],
    ["http://bücher.example", "http://xn--bcher-kva.example"],
    ["http://xn--bcher-kva.example", "http://xn--bcher-kva.example"],
    ["http://ｌｏｃａｌｈｏｓｔ:4655", "http://localhost:4655"],
    ["http://%6cocalhost:4655", "http://localhost:4655"],
    ["https://go.example.com", "https://go.example.com"],
    ["http://golinks:8080", "http://golinks:8080"],
  ];
  for (const [input, origin] of cases) {
    assert.deepEqual(normalizeDirectoryUrl(input), { ok: true, origin }, JSON.stringify(input));
  }
});

test("normalizeDirectoryUrl refuses hostile and mistaken input, saying why", () => {
  /** @type {[string, Code][]} */
  const cases = [
    ["", "empty"],
    ["   ", "empty"],
    [`http://${"a".repeat(2050)}.com`, "too_long"],
    ["http://exa mple.com", "whitespace"],
    ["http://local\thost:4655", "whitespace"],
    ["http://localhost\n:4655", "whitespace"],
    ["http://localhost:4655/\u0000", "whitespace"],
    ["http://local\u200bhost", "whitespace"], // the parser would silently drop the zero-width space
    ["http://localhost\u00ad:4655", "whitespace"], // and the soft hyphen
    ["javascript:alert(1)", "scheme"],
    ["JavaScript:alert(1)", "scheme"],
    ["javascript://%0aalert(1)", "scheme"],
    ["data:text/html,<h1>hi</h1>", "scheme"],
    ["file:///etc/passwd", "scheme"],
    ["ftp://example.com", "scheme"],
    ["ws://localhost:4655", "scheme"],
    ["chrome://settings", "scheme"],
    ["chrome-extension://abcdefghijklmnop/", "scheme"],
    ["http:localhost:4655", "scheme"],
    ["http:/localhost:4655", "scheme"],
    ["http:\\\\localhost:4655", "scheme"],
    ["http://user:pw@localhost:4655", "credentials"],
    ["http://user@localhost:4655", "credentials"],
    ["http://@localhost:4655", "credentials"],
    ["http://:pw@localhost:4655", "credentials"],
    ["http://localhost:4655@evil.example", "credentials"],
    ["http://localhost:4655/path", "path"],
    ["http://localhost:4655/-/", "path"],
    ["http://localhost:4655//", "path"],
    ["http://localhost:4655/.", "path"],
    ["http://localhost:4655?x=1", "path"],
    ["http://localhost:4655/?", "path"],
    ["http://localhost:4655#top", "path"],
    ["http://localhost:4655/#", "path"],
    ["http://localhost:4655\\", "path"],
    ["http://localhost:0", "port"],
    ["http://localhost:00", "port"],
    ["http://localhost:65536", "port"],
    ["http://localhost:99999999999", "port"],
    ["http://localhost:-1", "invalid"],
    ["http://localhost:abc", "invalid"],
    ["http://", "invalid"],
    ["https://", "invalid"],
    ["http://:4655", "invalid"],
    ["http://[::1", "invalid"],
    ["http://[1:2:3]", "invalid"],
    ["http://localhost.", "host"],
    ["http://localhost.:4655", "host"],
    ["http://a..b", "host"],
    ["http://.example.com", "host"],
    ["http://a_b.example", "host"],
    ["http://a$b.example", "host"],
    ["http://a'b.example", "host"],
    [`http://${"a".repeat(64)}.com`, "host"],
    ["http://go", "go_host"],
    ["http://GO", "go_host"],
    ["http://go/", "go_host"],
    ["https://go", "go_host"],
    ["http://go:4655", "go_host"],
    ["http://%67o", "go_host"],
    ["http://ｇｏ", "go_host"],
    ["http://go.", "host"],
    ["go", "go_host"],
    ["go:4655", "go_host"],
    ["localhost:0", "port"],
  ];
  for (const [input, code] of cases) {
    const result = normalizeDirectoryUrl(input);
    assert.equal(result.ok, false, JSON.stringify(input));
    if (result.ok) continue;
    assert.equal(result.code, code, `${JSON.stringify(input)}: ${result.message}`);
    assert.match(result.message, /^[A-Z].*\.$/s, `${JSON.stringify(input)} has a sentence to show`);
  }
});

test("an address without a scheme is refused, naming the http form instead of guessing it", () => {
  // Decided: no silent "http://". For a server elsewhere the right scheme
  // may be https, and a guess that is wrong fails later, with no message.
  /** @type {[string, string][]} */
  const cases = [
    ["localhost:4655", "http://localhost:4655"],
    ["localhost:4655/", "http://localhost:4655"],
    ["LOCALHOST", "http://localhost"],
    ["127.0.0.1:4655", "http://127.0.0.1:4655"],
    ["[::1]:4655", "http://[::1]:4655"],
    ["go.example.com", "http://go.example.com"],
  ];
  for (const [input, suggestion] of cases) {
    const result = normalizeDirectoryUrl(input);
    assert.deepEqual(
      result,
      { ok: false, code: "missing_scheme", message: `Start the address with http:// or https://, for example ${suggestion}.`, suggestion },
      input,
    );
    assert.deepEqual(normalizeDirectoryUrl(suggestion), { ok: true, origin: suggestion }, `the suggestion for ${input} is itself accepted`);
  }
  // A port alone gets the whole default-host address, not the IPv4 reading
  // of the number (4771 would be 0.0.18.163).
  for (const input of ["4771", ":4771", "4771/"]) {
    assert.deepEqual(
      normalizeDirectoryUrl(input),
      { ok: false, code: "missing_scheme", message: "Give the whole address, not only the port: for example http://localhost:4771.", suggestion: "http://localhost:4771" },
      input,
    );
  }
  for (const input of ["0", "65536", "99999"]) {
    assert.deepEqual(normalizeDirectoryUrl(input), { ok: false, code: "port", message: "The port must be a number from 1 to 65535." }, input);
  }
  // Text that is not a host either gets the plain sentence and no suggestion.
  assert.deepEqual(normalizeDirectoryUrl("local host"), { ok: false, code: "whitespace", message: "Remove the spaces, line breaks or other invisible characters inside the address." });
  const odd = normalizeDirectoryUrl("localhost%2f");
  assert.equal(odd.ok, false);
  assert.equal(odd.ok === false && odd.suggestion, undefined);
});

test("every accepted address makes a rule whose substitution Chrome can use", () => {
  // PRD Appendix B.2: Chrome silently ignores a rule whose substituted URL is
  // invalid. Substitute a few captures by hand and check the result parses
  // and stays on the Directory's origin.
  const accepted = ["http://localhost:4655", "https://go.example.com", "http://[::1]:4655", "http://bücher.example:8080", "http://127.1"];
  for (const input of accepted) {
    const parsed = normalizeDirectoryUrl(input);
    assert.ok(parsed.ok, input);
    if (!parsed.ok) continue;
    const substitution = buildRedirectRule(parsed.origin).action.redirect?.regexSubstitution ?? "";
    assert.equal(normalizeDirectoryUrl(parsed.origin).ok, true, `${parsed.origin} is stable`);
    for (const captured of ["", "roadmap", "gh/api-server/pr/12?tab=files#top", "a%20b%2Fc"]) {
      const url = new URL(substitution.replace("\\1", captured));
      assert.equal(url.origin, parsed.origin, `${input} + ${captured}`);
    }
  }
});

test("the host pattern of a Directory names its origin and every path", () => {
  assert.equal(hostPattern("http://localhost:4655"), "http://localhost:4655/*");
  assert.equal(hostPattern("https://go.example.com"), "https://go.example.com/*");
  assert.equal(hostPattern("http://[::1]:4655"), "http://[::1]:4655/*");
});

test("only the default Directory is covered by the manifest's host access", () => {
  assert.deepEqual([...REQUIRED_HOST_PATTERNS], ["*://go/*", "http://localhost:4655/*"]);
  assert.equal(needsOptionalAccess(DEFAULT_DIRECTORY_URL), false);
  for (const origin of ["http://localhost:4656", "https://localhost:4655", "http://127.0.0.1:4655", "http://[::1]:4655", "https://go.example.com"]) {
    assert.equal(needsOptionalAccess(origin), true, origin);
  }
});

test("originsToRelease gives up every optional origin but the new Directory's, and never a required one", () => {
  const granted = ["*://go/*", "http://localhost:4655/*", "http://localhost:4656/*", "https://go.example.com/*"];
  /** @type {[string, string, string[]][]} */
  const cases = [
    ["back to the default", "http://localhost:4655", ["http://localhost:4656/*", "https://go.example.com/*"]],
    ["to one already granted", "https://go.example.com", ["http://localhost:4656/*"]],
    ["to another", "http://localhost:4656", ["https://go.example.com/*"]],
  ];
  for (const [name, origin, released] of cases) {
    assert.deepEqual(originsToRelease(granted, origin), released, name);
  }
  assert.deepEqual(originsToRelease(["*://go/*", "http://localhost:4655/*"], "http://localhost:4655"), []);
  // The running manifest's own list is respected too, so a build that adds a
  // host permission never asks Chrome to remove it (Chrome would then refuse
  // the whole removal).
  assert.deepEqual(originsToRelease(["http://127.0.0.1:9/*", "http://localhost:4656/*"], "http://localhost:4655", ["http://127.0.0.1:9/*"]), ["http://localhost:4656/*"]);
  // The manifest's hosts as this code knows them are never released, even
  // when the running manifest reports none.
  assert.deepEqual(originsToRelease(["*://go/*", "http://localhost:4655/*", "http://x:1/*"], "http://x:1", []), []);
});

test("a scheme that is not http or https is named in the refusal", () => {
  for (const [input, scheme] of [["javascript:alert(1)", "javascript:"], ["data:text/html,x", "data:"], ["mailto:a@b.c", "mailto:"]]) {
    assert.deepEqual(normalizeDirectoryUrl(input), { ok: false, code: "scheme", message: `The address must start with http:// or https://, not ${scheme}.` }, input);
  }
  assert.deepEqual(normalizeDirectoryUrl("ftp://example.com"), { ok: false, code: "scheme", message: "The address must start with http:// or https://, not ftp://." });
});

test("goLinkPath recognizes exactly the navigations the rule redirects", () => {
  const filter = new RegExp(buildRedirectRule(DEFAULT_DIRECTORY_URL).condition.regexFilter ?? "");
  /** @type {[string, string | null][]} */
  const cases = [
    ["http://go/", ""],
    ["https://go/", ""],
    ["http://go/roadmap", "roadmap"],
    ["https://go/gh/x?y=1", "gh/x?y=1"],
    ["http://go", null],
    ["http://go.example.com/x", null],
    ["http://localhost:4655/", null],
    ["chrome-extension://id/onboarding/onboarding.html", null],
  ];
  for (const [url, path] of cases) {
    assert.equal(goLinkPath(url), path, url);
    assert.equal(filter.test(url), path !== null, `${url}: the rule agrees`);
  }
});

test("the storage keys and the first-run page are fixed names", () => {
  // Renaming a key would lose every user's saved Directory: pin them.
  assert.deepEqual({ ...STORAGE_KEYS }, { directoryUrl: "directoryUrl", firstRunDone: "firstRunDone", firstRunSawGo: "firstRunSawGo" });
  assert.deepEqual({ ...SESSION_KEYS }, { installHandled: "installHandled" });
  assert.equal(FIRST_RUN_PAGE, "onboarding/onboarding.html");
});
