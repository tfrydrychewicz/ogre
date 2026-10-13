// Helpers for asking Chromium's own autocomplete code what the address bar
// would do with a given input, through the debug page chrome://omnibox.
//
// Playwright cannot type into the real address bar. chrome://omnibox runs the
// same AutocompleteController with the same providers for a text you give it
// and lists the matches; the first match, when it is allowed to be the default,
// is the one Enter would open. The page can export everything as JSON, which
// is what these helpers read.

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { freshProfileDir } from "./env.mjs";

const OMNIBOX_URL = "chrome://omnibox";

/**
 * A fresh profile directory in which Chromium's internal debug pages are
 * switched on. Since Chromium 13x chrome://omnibox shows "Internal debugging
 * pages are currently disabled" until the Local State pref below is true (the
 * same pref the button on chrome://chrome-urls sets).
 *
 * @param {object} [prefs] profile settings to start with, written to Default/Preferences
 */
export function freshProfileWithDebugPages(prefs) {
  const dir = freshProfileDir();
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, "Local State"), JSON.stringify({ internal_only_uis_enabled: true }));
  if (prefs) {
    mkdirSync(path.join(dir, "Default"), { recursive: true });
    writeFileSync(path.join(dir, "Default", "Preferences"), JSON.stringify(prefs));
  }
  return dir;
}

/** The fields of one autocomplete match that matter here, straight from the export. */
function trimMatch(match, index) {
  return {
    index,
    provider: match.providerName,
    type: match.type,
    relevance: match.relevance,
    allowedToBeDefaultMatch: match.allowedToBeDefaultMatch,
    contents: match.contents,
    description: match.description,
    fillIntoEdit: match.fillIntoEdit,
    inlineAutocompletion: match.inlineAutocompletion,
    destinationUrl: match.destinationUrl,
    transition: match.transition,
    isSearchType: match.isSearchType,
    keyword: match.keyword,
    associatedKeyword: match.associatedKeyword,
    fromPrevious: match.fromPrevious,
  };
}

/** "url-what-you-typed [HistoryURL 1403] -> http://go/foo", for evidence a person can scan. */
export function describeMatch(match) {
  if (!match) return "(none)";
  const keyword = match.keyword ? ` keyword=${match.keyword}` : "";
  return `${match.type} [${match.provider} ${match.relevance}${keyword}] -> ${match.destinationUrl}`;
}

/**
 * Runs one input through chrome://omnibox and returns the final result set.
 *
 * The page is reloaded for every query and "Reset autocomplete controller" is
 * ticked, so no match can be carried over from an earlier input.
 *
 * defaultMatch is the first combined result when allowedToBeDefaultMatch is
 * true: Chromium sorts the default match to the top, and it is what pressing
 * Enter opens. It is null if the first result may not be the default.
 *
 * @param {import("@playwright/test").Page} page any page of the context; it is navigated to chrome://omnibox
 * @param {string} text the address-bar input
 * @param {object} [options]
 * @param {boolean} [options.inKeywordMode=false] tick "In keyword mode": the state the address bar is in
 *   after the user accepted a keyword with Tab or Space
 * @param {number} [options.timeout=10000] how long to wait for "all providers done"
 */
export async function queryOmnibox(page, text, { inKeywordMode = false, timeout = 10_000 } = {}) {
  await page.goto(OMNIBOX_URL);
  if (!page.url().startsWith("chrome://omnibox")) {
    throw new Error(`chrome://omnibox did not open (landed on ${page.url()}); are internal debug pages enabled?`);
  }
  // Playwright's CSS engine pierces the open shadow roots of <omnibox-input>.
  await page.locator("#omnibox-input #reset-autocomplete-controller").check();
  if (inKeywordMode) await page.locator("#omnibox-input #in-keyword-mode").check();
  await page.locator("#omnibox-input #input-text").fill(text);

  // Each response is rendered as an <output-results-group>; the newest one
  // carries "all providers done = true" when the controller has finished.
  await page
    .waitForFunction(
      () => {
        const groups = document.querySelector("#omnibox-output")?.shadowRoot?.querySelectorAll("output-results-group");
        const last = groups?.[groups.length - 1];
        const details = last?.shadowRoot?.querySelector("output-results-details");
        return details?.shadowRoot?.querySelector("#done")?.textContent.trim() === "true";
      },
      null,
      { timeout },
    )
    .catch(() => {
      // Not done in time: the export below says so in its "done" field.
    });

  const downloading = page.waitForEvent("download", { timeout: 10_000 });
  await page.locator("#omnibox-input #export-file").click();
  const download = await downloading;
  const exported = JSON.parse(readFileSync(await download.path(), "utf8"));
  await download.delete();

  // responsesHistory: one entry per query; each entry lists the controller's
  // successive updates for that query, the last being the final state.
  const updates = exported.responsesHistory.at(-1) ?? [];
  const final = updates.at(-1);
  if (!final || final.inputText !== text) {
    throw new Error(`chrome://omnibox export has no result for ${JSON.stringify(text)}`);
  }
  const matches = final.combinedResults.map(trimMatch);
  const defaultMatch = matches[0]?.allowedToBeDefaultMatch ? matches[0] : null;
  return {
    input: text,
    queryInputs: exported.queryInputs,
    done: final.done,
    updates: updates.length,
    inputType: final.type,
    host: final.host,
    isTypedHost: final.isTypedHost,
    defaultMatch,
    matches,
    version: exported.versionDetails.version,
  };
}

// Suggestions fetched from the default search engine vary from run to run, so
// summaries count them instead of listing them. (The default match is always
// listed, whatever its type.)
const SERVER_SUGGESTION = /^(search-suggest|navsuggest)/;

/** What pressing Enter on this default match does. */
function enterWould(match) {
  if (!match) return "unknown (no default match)";
  if (match.provider === "Keyword") return `keyword ${match.keyword}`;
  return match.isSearchType ? "search" : "navigate";
}

/**
 * One short record per query for the evidence: classification, default match,
 * and the other local matches. The full match list stays in the raw file.
 */
export function summarize(result) {
  const rest = result.matches.slice(result.defaultMatch ? 1 : 0);
  const local = rest.filter((match) => !SERVER_SUGGESTION.test(match.type));
  return {
    input: result.input,
    ...(result.queryInputs.inKeywordMode ? { inKeywordMode: true } : {}),
    inputType: result.inputType,
    isTypedHost: result.isTypedHost,
    allProvidersDone: result.done,
    enterWould: enterWould(result.defaultMatch),
    default: describeMatch(result.defaultMatch),
    others: local.map(describeMatch),
    serverSuggestionsOmitted: rest.length - local.length,
  };
}

// ui::PageTransition, as stored in visits.transition.
const CORE_TYPES = [
  "LINK",
  "TYPED",
  "AUTO_BOOKMARK",
  "AUTO_SUBFRAME",
  "MANUAL_SUBFRAME",
  "GENERATED",
  "AUTO_TOPLEVEL",
  "FORM_SUBMIT",
  "RELOAD",
  "KEYWORD",
  "KEYWORD_GENERATED",
];
const QUALIFIERS = {
  0x01000000: "FORWARD_BACK",
  0x02000000: "FROM_ADDRESS_BAR",
  0x04000000: "HOME_PAGE",
  0x08000000: "FROM_API",
  0x10000000: "CHAIN_START",
  0x20000000: "CHAIN_END",
  0x40000000: "CLIENT_REDIRECT",
  0x80000000: "SERVER_REDIRECT",
};

function decodeTransition(value) {
  const bits = Number(BigInt.asUintN(32, BigInt(value)));
  const names = [CORE_TYPES[bits & 0xff] ?? `core${bits & 0xff}`];
  for (const [bit, name] of Object.entries(QUALIFIERS)) {
    if ((bits & Number(bit)) !== 0) names.push(name);
  }
  return names.join("|");
}

/**
 * Reads what the profile's history database holds, to show which state a
 * query ran against. Call it only after the browser using the profile has
 * closed (the file is locked while Chromium runs, and written on exit).
 *
 * @param {string} userDataDir
 * @returns {{urls: object[], visits: object[]}}
 */
export function readHistory(userDataDir) {
  const database = new DatabaseSync(path.join(userDataDir, "Default", "History"), { readOnly: true });
  try {
    const urls = database
      .prepare("SELECT id, url, visit_count, typed_count, hidden FROM urls ORDER BY id")
      .all()
      .map((row) => ({ ...row }));
    const visits = database
      .prepare(
        "SELECT v.id, u.url, v.transition, v.from_visit FROM visits v JOIN urls u ON u.id = v.url ORDER BY v.id",
      )
      .all()
      .map((row) => ({
        id: row.id,
        url: row.url,
        fromVisit: row.from_visit,
        transition: decodeTransition(row.transition),
      }));
    return { urls, visits };
  } finally {
    database.close();
  }
}

/**
 * Removes the "typed" credit from every http://go/ URL in a closed profile's
 * history file: typed_count becomes 0 and the visits' core transition becomes
 * LINK. The visits themselves stay. This builds a state Chromium does not
 * produce by itself (see the spec), to show what the typed credit changes.
 *
 * @param {string} userDataDir
 * @returns {{urlsChanged: number, visitsChanged: number}}
 */
export function removeTypedCreditForGo(userDataDir) {
  const database = new DatabaseSync(path.join(userDataDir, "Default", "History"));
  try {
    const urls = database.prepare("UPDATE urls SET typed_count = 0 WHERE url LIKE 'http://go/%'").run();
    const visits = database
      .prepare(
        "UPDATE visits SET transition = transition & ~255 WHERE url IN (SELECT id FROM urls WHERE url LIKE 'http://go/%')",
      )
      .run();
    return { urlsChanged: Number(urls.changes), visitsChanged: Number(visits.changes) };
  } finally {
    database.close();
  }
}
