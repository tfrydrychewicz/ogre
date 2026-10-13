// Unit tests for the pure extension logic. Run with: npm run test:extension

import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_DIRECTORY_URL, RULE_ID, buildRedirectRule, planRuleUpdate, sameRedirectRule } from "./config.js";

test("the default Directory is a godir started with its defaults", () => {
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
    ["http://go/gh/godir/pr/12?tab=files#top", "gh/godir/pr/12?tab=files#top"],
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
