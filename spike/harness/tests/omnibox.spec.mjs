// Proxy evidence for the address-bar checks of the Chrome spike checklist
// (docs/IMPLEMENTATION_PLAN.md, Appendix B): checks 1, 2, 3, 10 and 15.
//
// Playwright cannot type into the address bar. These tests ask Chromium's own
// autocomplete code instead, through the debug page chrome://omnibox (see
// lib/omnibox.mjs): for a given input it lists the matches the address bar
// would build, and the first one, when allowed to be the default, is what
// Enter opens. That is evidence about Chromium's decision logic in Playwright's
// headless Chromium. It is NOT a person typing in Chrome stable, so every
// result here is recorded as "proxy" and the manual run still has to confirm it.
//
// "Typed" visits are made with the DevTools command Page.navigate and
// transitionType "typed": a browser-initiated navigation that history records
// with the TYPED transition, which is the part of a real Enter that the
// autocomplete code later reads. The history file is read back after the
// browser closes to show what was actually recorded.
//
// Run:  cd spike/harness && npx playwright test omnibox
// Needs port 4655 free. Search suggestions come from the network when it is
// there; they are counted, not judged. Full match lists go to
// spike/results/omnibox-raw.json.

import { expect, test } from "@playwright/test";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import {
  DIRECTORY_URL,
  EXTENSION_DIR,
  OMNIBOX_EXTENSION_DIR,
  RESULTS_DIR,
  buildStub,
  clearStubLog,
  launchWithExtension,
  recordResult,
  startStub,
  stubLog,
} from "../lib/env.mjs";
import {
  describeMatch,
  freshProfileWithDebugPages,
  queryOmnibox,
  readHistory,
  removeTypedCreditForGo,
  summarize,
} from "../lib/omnibox.mjs";

const RAW_FILE = path.join(RESULTS_DIR, "omnibox-raw.json");
const GO_HOME = "http://go/";
// "Always use secure connections" at its strict level (public and private sites).
const STRICT_HTTPS_PREF = "https_only_mode_enabled";
const USED_LINK = "http://go/hit/x";
const DEST_URL = `${DIRECTORY_URL}/-/spike/dest`;

// What a user might type. The first five are the checklist's; the last two
// only matter once the keyword "go/" is registered (check 15).
const BASE_INPUTS = ["go/foo", "go/", "go/foo/", "http://go/foo", "go"];
const KEYWORD_INPUTS = ["go/ foo", "go foo"];
const ALL_INPUTS = [...BASE_INPUTS, ...KEYWORD_INPUTS];

const PROXY_NOTE =
  "PROXY ONLY: this is Chromium's autocomplete code answering through chrome://omnibox in Playwright's headless " +
  "Chromium, not a person typing in Chrome stable. The manual run must still confirm it.";

/** The directory stub. The last scenario stops and restarts it, so it is not a const. */
let directory;

test.beforeAll(async () => {
  buildStub();
  directory = await startStub();
});

test.afterAll(async () => {
  await directory?.stop();
});

// ---------------------------------------------------------------------------
// Helpers

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const firstLine = (error) => String(error?.message ?? error).split("\n")[0];

/** Calls read() until ok(value) or the time is up; returns the last value either way. */
async function poll(read, ok, timeout = 5000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await read();
    if (ok(value) || Date.now() > deadline) return value;
    await sleep(100);
  }
}

/**
 * Launches Chromium with one extension on the given profile, waits for redirect
 * rule 1, and opens two tabs: one for chrome://omnibox, one to navigate with.
 */
async function openSession(userDataDir, extensionDir = EXTENSION_DIR) {
  const { context, extensionId, serviceWorker } = await launchWithExtension({ extensionDir, userDataDir });
  try {
    const rules = await poll(
      () => serviceWorker.evaluate(() => chrome.declarativeNetRequest.getDynamicRules()),
      (found) => found.some((rule) => rule.id === 1),
    );
    if (!rules.some((rule) => rule.id === 1)) throw new Error("the extension did not install redirect rule 1");
    return { context, extensionId, omnibox: await context.newPage(), tab: await context.newPage() };
  } catch (error) {
    await context.close();
    throw error;
  }
}

/**
 * The nearest thing to pressing Enter on a URL match that DevTools offers: a
 * browser-initiated navigation with page transition TYPED. Never throws;
 * reports where the tab ended up.
 */
async function typedNavigate(tab, url, expected) {
  const session = await tab.context().newCDPSession(tab);
  try {
    const answer = await session.send("Page.navigate", { url, transitionType: "typed" });
    await tab.waitForURL(expected, { timeout: 10_000 }).catch(() => {});
    return {
      asked: url,
      how: 'CDP Page.navigate, transitionType "typed"',
      errorText: answer.errorText ?? null,
      finalUrl: tab.url(),
    };
  } finally {
    await session.detach().catch(() => {});
  }
}

/** A real mouse click on a link to href, placed on the stub page /m1. */
async function clickLink(tab, href, expected) {
  await tab.goto(`${DIRECTORY_URL}/m1`);
  await tab.evaluate((target) => {
    const link = document.createElement("a");
    link.id = "spike-link";
    link.href = target;
    link.textContent = target;
    document.body.append(link);
  }, href);
  await tab.click("#spike-link");
  await tab.waitForURL(expected, { timeout: 10_000 }).catch(() => {});
  return { asked: href, how: `mouse click on <a href> on ${DIRECTORY_URL}/m1`, finalUrl: tab.url() };
}

const logLines = (entries) => entries.map((entry) => `${entry.method} ${entry.host}${entry.requestURI}`);
const isNavigation = (match) => Boolean(match) && !match.isSearchType;
const sameMatch = (a, b) => a?.type === b?.type && a?.destinationUrl === b?.destinationUrl;

// ---------------------------------------------------------------------------
// The scenario: every browser state, queried once, shared by the five checks.

/**
 * states[key] = {how, queries, history}: how the profile got into that state
 * and the full chrome://omnibox answer for each input. history is what the
 * history file held once the browser had closed, so only the last state of
 * each browser session has it; it covers that session's earlier states too.
 */
async function runScenario() {
  const states = {};
  const visits = {};
  const begin = (key, how) => {
    states[key] = { how, queries: [], history: null };
  };
  const ask = async (session, key, inputs, options) => {
    for (const input of inputs) states[key].queries.push(await queryOmnibox(session.omnibox, input, options));
  };
  // History is written on another thread. Wait until the autocomplete code
  // sees the change, or give up after 5 s and let the recorded queries show it.
  const waitUntil = (session, input, seen) => poll(() => queryOmnibox(session.omnibox, input), seen);
  const waitForTypedHost = (session) => waitUntil(session, "go/", (result) => result.isTypedHost);
  const closeAndReadHistory = async (session, userDataDir, lastKey) => {
    await session.context.close();
    if (states[lastKey]) states[lastKey].history = readHistory(userDataDir);
  };

  try {
    // --- Plain extension: fresh, then taught by one typed visit, then a go link used.
    {
      const profile = freshProfileWithDebugPages();
      let session = await openSession(profile);
      try {
        begin("plainFresh", "New profile, spike/extension loaded, nothing visited.");
        await ask(session, "plainFresh", ALL_INPUTS);

        await clearStubLog();
        visits.home = await typedNavigate(session.tab, GO_HOME, `${DIRECTORY_URL}/`);
        visits.home.stubLog = logLines(await stubLog());
        await waitForTypedHost(session);
        begin("plainTyped", `Same profile after one visit to ${GO_HOME} (${visits.home.how}).`);
        await ask(session, "plainTyped", ALL_INPUTS);

        visits.used = await typedNavigate(session.tab, USED_LINK, DEST_URL);
        const offersUsedLink = (result) => result.matches.some((match) => match.destinationUrl === USED_LINK);
        await waitUntil(session, "go/h", offersUsedLink);
        begin("plainUsed", `Same profile after also visiting ${USED_LINK} (${visits.used.how}).`);
        await ask(session, "plainUsed", ["go/h", "go/hit", "go/"]);
      } finally {
        await closeAndReadHistory(session, profile, "plainUsed");
      }

      // Chromium credits the first visit to a single-label host as typed
      // whatever the real transition was (see plainClicked below), so a
      // "visited but never typed" profile has to be made by editing the file.
      const edit = removeTypedCreditForGo(profile);
      session = await openSession(profile);
      try {
        begin(
          "plainUntyped",
          "SYNTHETIC: same profile, browser closed, typed credit removed from the history file " +
            `(typed_count = 0 on ${edit.urlsChanged} http://go/ URLs, ${edit.visitsChanged} visits set to LINK), relaunched.`,
        );
        await ask(session, "plainUntyped", [...BASE_INPUTS, "go/h"]);
      } finally {
        await closeAndReadHistory(session, profile, "plainUntyped");
      }
    }

    // --- Plain extension: the first visit to go is a clicked link, never typed.
    {
      const profile = freshProfileWithDebugPages();
      const session = await openSession(profile);
      try {
        visits.clicked = await clickLink(session.tab, "http://go/hit/clicked", DEST_URL);
        await waitForTypedHost(session);
        begin("plainClicked", `New profile; the only visit to go was a ${visits.clicked.how} to http://go/hit/clicked.`);
        await ask(session, "plainClicked", ["go/foo", "go/", "go"]);
      } finally {
        await closeAndReadHistory(session, profile, "plainClicked");
      }
    }

    // --- Plain extension, "Always use secure connections" at its strict level:
    // Chromium rewrites http://go/ to https://go/ before anything else sees it.
    // Which URL does the host get taught under?
    {
      const profile = freshProfileWithDebugPages({ [STRICT_HTTPS_PREF]: true });
      const session = await openSession(profile);
      try {
        visits.strict = await typedNavigate(session.tab, GO_HOME, `${DIRECTORY_URL}/`);
        await waitForTypedHost(session);
        begin("plainStrict", `New profile with the setting "${STRICT_HTTPS_PREF}" on; one visit to ${GO_HOME} (${visits.strict.how}).`);
        await ask(session, "plainStrict", ["go/foo", "go/", "go"]);
      } finally {
        await closeAndReadHistory(session, profile, "plainStrict");
        // Chromium rewrites the file on exit; a setting it did not know would be gone.
        const prefs = JSON.parse(readFileSync(path.join(profile, "Default", "Preferences"), "utf8"));
        visits.strict.settingAfterExit = { [STRICT_HTTPS_PREF]: prefs[STRICT_HTTPS_PREF] ?? null };
      }
    }

    // --- Omnibox variant (keyword "go/"): fresh, then taught the same way.
    {
      const profile = freshProfileWithDebugPages();
      const session = await openSession(profile, OMNIBOX_EXTENSION_DIR);
      try {
        begin("keywordFresh", 'New profile, spike/extension-omnibox loaded (keyword "go/"), nothing visited.');
        await ask(session, "keywordFresh", ALL_INPUTS);
        await ask(session, "keywordFresh", ["go/ foo"], { inKeywordMode: true });

        visits.keywordHome = await typedNavigate(session.tab, GO_HOME, `${DIRECTORY_URL}/`);
        await waitForTypedHost(session);
        begin("keywordTyped", `Same profile after one visit to ${GO_HOME} (${visits.keywordHome.how}).`);
        await ask(session, "keywordTyped", ALL_INPUTS);
        await ask(session, "keywordTyped", ["go/ foo"], { inKeywordMode: true });
      } finally {
        await closeAndReadHistory(session, profile, "keywordTyped");
      }
    }

    // --- Plain extension, Directory stopped: does a typed go/ still teach the host?
    {
      const profile = freshProfileWithDebugPages();
      const session = await openSession(profile);
      try {
        await directory.stop();
        visits.down = await typedNavigate(session.tab, GO_HOME, /\/unreachable\.html/);
        await waitForTypedHost(session);
        begin("plainDown", `New profile; one visit to ${GO_HOME} (${visits.down.how}) while the stub was stopped.`);
        await ask(session, "plainDown", ["go/foo", "go/"]);
      } finally {
        await closeAndReadHistory(session, profile, "plainDown").catch(() => {});
        directory = await startStub();
      }
    }

    return { states, visits };
  } finally {
    writeRaw(states, visits);
  }
}

function writeRaw(states, visits) {
  const anyQuery = Object.values(states).flatMap((state) => state.queries)[0];
  mkdirSync(RESULTS_DIR, { recursive: true });
  writeFileSync(
    RAW_FILE,
    `${JSON.stringify(
      {
        generatedAt: new Date().toISOString(),
        chromiumVersion: anyQuery?.version ?? null,
        source:
          "chrome://omnibox JSON export (final response per query, combinedResults), Playwright's headless Chromium. " +
          "defaultMatch is combinedResults[0] when allowedToBeDefaultMatch is true.",
        visits,
        states,
      },
      null,
      2,
    )}\n`,
  );
}

let scenario;
const collected = () => (scenario ??= runScenario());

/**
 * Defines the test for one check. The first test to run pays for the scenario;
 * the rest reuse it. judge() returns {status, observed, evidence}. If the
 * scenario fails, each check is recorded as inconclusive with the error.
 */
function check(id, title, judge) {
  test(`check ${id}: ${title}`, async () => {
    test.setTimeout(200_000);
    let result;
    try {
      result = judge(await collected());
    } catch (error) {
      recordResult({
        id,
        title,
        mode: "proxy",
        status: "inconclusive",
        observed: `The check did not run to the end: ${firstLine(error)}`,
        evidence: { error: String(error?.stack ?? error), rawFile: RAW_FILE },
      });
      throw error;
    }
    recordResult({ id, title, mode: "proxy", ...result });
    console.log(`check ${id}: ${result.status.toUpperCase()} - ${result.observed}`);
    if (result.status === "inconclusive") {
      test.info().annotations.push({ type: "inconclusive", description: result.observed });
    }
    expect(result.status, result.observed).not.toBe("fail");
  });
}

/** Accessors over the scenario's states. */
function reader(states) {
  const find = (key, input, inKeywordMode = false) => {
    const query = states[key].queries.find(
      (candidate) => candidate.input === input && candidate.queryInputs.inKeywordMode === inKeywordMode,
    );
    if (!query) throw new Error(`no query ${JSON.stringify(input)} in state ${key}`);
    return query;
  };
  const table = (key, inputs) => ({
    how: states[key].how,
    queries: states[key].queries.filter((query) => !inputs || inputs.includes(query.input)).map(summarize),
  });
  const goRows = (key) => states[key].history?.urls.filter((row) => row.url.startsWith("http://go/")) ?? [];
  const goVisits = (key) => states[key].history?.visits.filter((row) => row.url.startsWith("http://go/")) ?? [];
  /** Rows for the host go under either scheme, as "url (typed_count n)". */
  const goRowsAnyScheme = (key) =>
    (states[key].history?.urls ?? [])
      .filter((row) => /^https?:\/\/go\//.test(row.url))
      .map((row) => `${row.url} (typed_count ${row.typed_count})`);
  return { find, table, goRows, goVisits, goRowsAnyScheme };
}

// ---------------------------------------------------------------------------
// The checks

check(1, "Typing go/ and pressing Enter reaches the stub", ({ states, visits }) => {
  const { find, table } = reader(states);
  const query = find("plainFresh", "go/");
  const match = query.defaultMatch;
  const navigates = isNavigation(match) && match.destinationUrl === GO_HOME;
  const reached = visits.home.finalUrl === `${DIRECTORY_URL}/` && visits.home.stubLog.includes("GET localhost:4655/");

  let status = "inconclusive";
  if (navigates && reached) status = "pass";
  else if (match?.isSearchType || (navigates && !reached)) status = "fail";

  return {
    status,
    observed:
      `In a fresh profile the default match for "go/" is ${describeMatch(match)} ` +
      `(input classified as "${query.inputType}"), so Enter would ${summarize(query).enterWould}. ` +
      `Opening that URL as a typed navigation ended on ${visits.home.finalUrl}; ` +
      `the stub logged ${JSON.stringify(visits.home.stubLog)}. ` +
      PROXY_NOTE,
    evidence: {
      query: table("plainFresh", ["go/"]),
      navigation: visits.home,
      rawFile: RAW_FILE,
    },
  };
});

check(2, "Before go/ was ever typed, go/foo: record whether Chrome searches or navigates", ({ states }) => {
  const { find, table } = reader(states);
  const query = find("plainFresh", "go/foo");
  const would = summarize(query).enterWould;
  const enterWouldFor = (input) => `"${input}" -> ${summarize(find("plainFresh", input)).enterWould}`;
  return {
    // The check asks for a record, so a clear answer either way passes.
    status: query.defaultMatch && query.done ? "pass" : "inconclusive",
    observed:
      `In a fresh profile "go/foo" is classified as "${query.inputType}" and the default match is ` +
      `${describeMatch(query.defaultMatch)}: Enter would ${would}` +
      `${would === "search" ? ", as the plan expects" : ", which the plan did NOT expect (it expects a search)"}. ` +
      `For comparison, Enter would: ${BASE_INPUTS.map(enterWouldFor).join("; ")}. ` +
      PROXY_NOTE,
    evidence: { fresh: table("plainFresh", BASE_INPUTS), rawFile: RAW_FILE },
  };
});

check(3, "After go/ has been typed once, go/foo navigates", ({ states, visits }) => {
  const { find, table, goRows, goVisits, goRowsAnyScheme } = reader(states);
  const after = find("plainTyped", "go/foo");
  const strict = find("plainStrict", "go/foo");
  const strictOn = visits.strict.settingAfterExit[STRICT_HTTPS_PREF] === true;
  const navigates = isNavigation(after.defaultMatch) && after.defaultMatch.destinationUrl === "http://go/foo";
  // Read when that browser session closed, so it also holds the later visit to USED_LINK.
  const homeRow = goRows("plainUsed").find((row) => row.url === GO_HOME) ?? null;
  const taught = homeRow?.typed_count > 0;

  let status = "inconclusive";
  if (navigates && taught) status = "pass";
  else if (taught && after.defaultMatch && !navigates) status = "fail";

  const would = (key) => {
    const query = find(key, "go/foo");
    return `Enter would ${summarize(query).enterWould} (isTypedHost ${query.isTypedHost})`;
  };
  return {
    status,
    observed:
      `"go/foo" by state. Fresh: ${would("plainFresh")}. ` +
      `After one typed visit to ${GO_HOME} (history row: ${JSON.stringify(homeRow)}): ` +
      `default match ${describeMatch(after.defaultMatch)}, ${would("plainTyped")}. ` +
      `First visit to go was a clicked link, never typed: ${would("plainClicked")}; ` +
      `history recorded that click as ${JSON.stringify(goVisits("plainClicked").map((row) => row.transition))}. ` +
      `Typed credit removed by editing the history file (synthetic "visited, not typed"): ${would("plainUntyped")}. ` +
      `Typed visit while the Directory was down (tab ended on ${visits.down.finalUrl.split("?")[0]}): ${would("plainDown")}; ` +
      `http://go/ rows in history afterwards: ${goRows("plainDown").length}. ` +
      `With "Always use secure connections" at its strict level (${STRICT_HTTPS_PREF}, ${strictOn ? "still true in the profile after exit" : "NOT confirmed on"}), ` +
      `one visit to ${GO_HOME}: history holds ${JSON.stringify(goRowsAnyScheme("plainStrict"))}, and the default match for "go/foo" is ` +
      `${describeMatch(strict.defaultMatch)}, ${would("plainStrict")}; so there the host is taught under https://go/. ` +
      `The "typed" visit was made with ${visits.home.how}, not by typing. ` +
      PROXY_NOTE,
    evidence: {
      fresh: table("plainFresh", ["go/foo"]),
      afterTypedVisit: table("plainTyped", BASE_INPUTS),
      firstVisitWasALinkClick: table("plainClicked"),
      typedCreditRemoved: table("plainUntyped", BASE_INPUTS),
      directoryDown: table("plainDown"),
      strictHttpsFirst: table("plainStrict"),
      navigations: visits,
      history: {
        afterTypedVisits: states.plainUsed.history,
        firstVisitWasALinkClick: states.plainClicked.history,
        typedCreditRemoved: states.plainUntyped.history,
        directoryDown: states.plainDown.history,
        strictHttpsFirst: states.plainStrict.history,
      },
      rawFile: RAW_FILE,
    },
  };
});

check(10, "The address bar's autocomplete offers go links used before", ({ states, visits }) => {
  const { find, table, goRows } = reader(states);
  const offers = (key, input) => find(key, input).matches.filter((match) => match.destinationUrl === USED_LINK);
  const offered = offers("plainUsed", "go/h");
  const inHistory = goRows("plainUsed").some((row) => row.url === USED_LINK);

  let status = "inconclusive";
  if (offered.length > 0) status = "pass";
  else if (inHistory) status = "fail";

  // Where in the dropdown: "row 8 of 8" is the last row, below everything else.
  const list = (key, input) => {
    const total = find(key, input).matches.length;
    const matches = offers(key, input);
    return matches.length
      ? matches.map((match) => `row ${match.index + 1} of ${total}: ${describeMatch(match)}`).join(", ")
      : `not offered (${total} rows)`;
  };
  const defaultForPrefix = find("plainUsed", "go/h").defaultMatch;
  return {
    status,
    observed:
      `With ${USED_LINK} visited once (${visits.used.how}; tab ended on ${visits.used.finalUrl}), ` +
      `"go/h" offers it in ${list("plainUsed", "go/h")}. The rows above it are the typed text and search suggestions from the search engine, ` +
      `whose number varies. The default match for "go/h" stays ${describeMatch(defaultForPrefix)} with inline completion ` +
      `${JSON.stringify(defaultForPrefix?.inlineAutocompletion ?? "")}, so the used link is a suggestion to pick, not what Enter opens, ` +
      `and nothing is completed in the address bar. ` +
      `"go/hit" offers it in ${list("plainUsed", "go/hit")}. "go/" offers it in ${list("plainUsed", "go/")}. ` +
      `With the typed credit removed (synthetic), "go/h" offers it in ${list("plainUntyped", "go/h")}. ` +
      `"Pass" here means only that the link is somewhere in the list; whether that row is visible in Chrome's real dropdown is for the manual run (step D). ` +
      "Suggestions learned from address-bar picks (the Shortcuts provider) cannot be produced here. " +
      PROXY_NOTE,
    evidence: {
      afterUsingTheLink: table("plainUsed"),
      typedCreditRemoved: table("plainUntyped", ["go/h"]),
      navigation: visits.used,
      history: states.plainUsed.history,
      rawFile: RAW_FILE,
    },
  };
});

check(15, "Registering the address-bar keyword go/ leaves a plain go/foo plus Enter unchanged", ({ states }) => {
  const { find } = reader(states);
  const pairs = [
    ["fresh", "plainFresh", "keywordFresh"],
    ["after one typed visit to http://go/", "plainTyped", "keywordTyped"],
  ];
  const rows = pairs.flatMap(([state, plainKey, keywordKey]) =>
    ALL_INPUTS.map((input) => {
      const plain = find(plainKey, input);
      const keyword = find(keywordKey, input);
      return {
        state,
        input,
        plainDefault: describeMatch(plain.defaultMatch),
        keywordDefault: describeMatch(keyword.defaultMatch),
        sameDefault: sameMatch(plain.defaultMatch, keyword.defaultMatch),
        // The hint "press Tab to use <extension>" hangs on a match through this field.
        keywordHintOnDefault: keyword.defaultMatch?.associatedKeyword || null,
        keywordMatchesAdded: keyword.matches
          .filter((match) => match.keyword === "go/")
          .map((match) => `#${match.index} ${describeMatch(match)} (may be default: ${match.allowedToBeDefaultMatch})`),
        addedInShort: keyword.matches
          .filter((match) => match.keyword === "go/")
          .map((match) => `"${input}" at #${match.index} (${match.relevance})`),
      };
    }),
  );
  const inKeywordMode = pairs.map(([state, , keywordKey]) => ({ state, ...summarize(find(keywordKey, "go/ foo", true)) }));

  const base = rows.filter((row) => BASE_INPUTS.includes(row.input));
  const changedBase = base.filter((row) => !row.sameDefault);
  const changedOther = rows.filter((row) => !BASE_INPUTS.includes(row.input) && !row.sameDefault);
  const complete = base.every((row) => row.plainDefault !== "(none)" && row.keywordDefault !== "(none)");

  let status = "inconclusive";
  if (changedBase.length > 0) status = "fail";
  else if (complete) status = "pass";

  const show = (row) => `"${row.input}" (${row.state}): ${row.plainDefault} becomes ${row.keywordDefault}`;
  const hints = rows.filter((row) => row.keywordHintOnDefault).map((row) => `"${row.input}" (${row.state})`);
  const added = pairs.map(
    ([state]) => `${state}: ${rows.filter((row) => row.state === state).flatMap((row) => row.addedInShort).join(", ") || "none"}`,
  );
  return {
    status,
    observed:
      `Default match with spike/extension against spike/extension-omnibox (keyword "go/"), fresh and after a typed visit to ${GO_HOME}, ` +
      `for ${BASE_INPUTS.map((input) => `"${input}"`).join(", ")}: ` +
      `${changedBase.length === 0 ? "identical in every case" : `DIFFERENT: ${changedBase.map(show).join("; ")}`}. ` +
      `With a space after the keyword the default does change: ${changedOther.length ? changedOther.map(show).join("; ") : "no case"}. ` +
      `Keyword hint attached to the default match for: ${hints.join(", ") || "none"}. ` +
      `Matches the keyword adds, by input, position and relevance: ${added.join("; ")}. ` +
      `In keyword mode (after Tab or Space accepts the hint) "go/ foo" gives: ${inKeywordMode.map((row) => `${row.state}: ${row.default}`).join("; ")}. ` +
      PROXY_NOTE,
    evidence: { comparison: rows.map(({ addedInShort, ...row }) => row), inKeywordMode, rawFile: RAW_FILE },
  };
});
