// What the web UI's browser modules do, beyond the PRD's scenarios (plan
// L3.11): hostile text stays text, deleting asks for the version it showed,
// a taken name hands its route over to the existing link, renaming renames
// every route, values are judged as they are typed, answers that arrive late
// never overwrite newer ones, and long names fit a phone.
//
// These open the Directory at its own address in Playwright's bundled
// Chromium without the extension. Every page fails the test on a content
// security policy violation or a script error (lib/ui.mjs).
//
// Needs port 4655 free: the server under test listens there.

import { expect, test } from "@playwright/test";
import path from "node:path";

import { DIRECTORY_URL, buildServer, createLink, startFixture, startServer, tempDir } from "./lib/harness.mjs";
import { expectNoHorizontalScroll, launchPlainBrowser, watchPolicy } from "./lib/ui.mjs";

/** @type {import("./lib/harness.mjs").RunningServer | undefined} */
let server;
/** @type {import("./lib/harness.mjs").Fixture} */
let fixture;
/** @type {import("@playwright/test").Browser | undefined} */
let browser;

/** The link `reports` of PRD 5.7, with the fixture as its destination. */
function reportsDraft() {
  return {
    description: "Daily report for one day",
    routes: [
      {
        pattern: "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
        destination: fixture.url("/daily?month={month}&day={day}"),
        variables: { month: { description: "Month, 1 to 12", example: "10" }, day: { description: "Day of the month", example: "13" } },
      },
    ],
  };
}

test.beforeAll(async () => {
  test.setTimeout(240_000); // a first build compiles the SQLite driver
  buildServer();
  server = await startServer({ dbPath: path.join(tempDir("ogre-e2e-data-"), "ogre.db") });
  fixture = await startFixture();
  await createLink(reportsDraft());
  await createLink({
    description: "GitHub shortcuts",
    routes: [
      { pattern: "gh", destination: fixture.url("/acme") },
      { pattern: "gh/{repo}", destination: fixture.url("/acme/{repo}") },
      { pattern: "gh/{repo}/pr/{num:int}", destination: fixture.url("/acme/{repo}/pull/{num}") },
    ],
  });
  await createLink({ description: "GitHub issues", routes: [{ pattern: "ghi", destination: fixture.url("/acme/issues") }] });
  await createLink({ routes: [{ pattern: "search/{q}", destination: fixture.url("/search?q={q}") }] });
  browser = await launchPlainBrowser();
});

test.afterAll(async () => {
  const results = await Promise.allSettled([browser?.close(), fixture?.close(), server?.stop()]);
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
  }
  const ended = results[2].status === "fulfilled" ? results[2].value : undefined;
  if (server) expect(ended, server.output()).toBe("exit code 0");
});

test.afterEach(async ({}, testInfo) => {
  if (testInfo.status !== testInfo.expectedStatus && server) {
    await testInfo.attach("ogre output", { body: server.output() || "(none)", contentType: "text/plain" });
    console.log(`--- ogre output ---\n${server.output() || "(none)"}\n--- end of ogre output ---`);
  }
});

/**
 * A page in a new context with the policy watch on; the watch must have seen
 * nothing when use returns.
 *
 * @param {import("@playwright/test").BrowserContextOptions} options
 * @param {(page: import("@playwright/test").Page, watch: Awaited<ReturnType<typeof watchPolicy>>) => Promise<void>} use
 */
async function withPage(options, use) {
  if (!browser) throw new Error("the browser did not start");
  const context = await browser.newContext(options);
  try {
    const watch = await watchPolicy(context);
    await use(await context.newPage(), watch);
    watch.assertClean();
  } finally {
    await context.close();
  }
}

/**
 * The API's answer for a link, by ID.
 *
 * @param {number} id
 */
async function apiLink(id) {
  const response = await fetch(`${DIRECTORY_URL}/-/api/v1/links/${id}`);
  return { status: response.status, body: response.status === 200 ? await response.json() : null };
}

/**
 * Opens the create or edit form and waits for the server's first answer
 * about the draft.
 *
 * @param {import("@playwright/test").Page} page
 * @param {string} path
 */
async function openForm(page, path) {
  await page.goto(DIRECTORY_URL + path);
  await expect(page.locator("#link-form[data-ready]")).toBeAttached();
}

test("hostile text in a link stays text on every page", async () => {
  const markup = "<img src=x id=pwn>Daily";
  const note = "<b id=pwn2>d</b>";
  const example = "<i id=pwn3>";
  await createLink({
    description: markup,
    routes: [{ pattern: "hostile/{day}", destination: fixture.url("/h?d={day}"), variables: { day: { description: note, example } } }],
  });
  const injected = "#pwn, #pwn2, #pwn3";

  await withPage({}, async (page, watch) => {
    await test.step("search results built as you type", async () => {
      await page.goto(`${DIRECTORY_URL}/-/`);
      await page.locator("#q").pressSequentially("hostile");
      await expect(page.locator("#results .result-desc")).toHaveText(markup);
      await expect(page.locator(injected)).toHaveCount(0);
    });

    await test.step("the link page's launcher", async () => {
      await page.goto(`${DIRECTORY_URL}/-/links/hostile`);
      const field = page.locator("#launcher").getByLabel("day");
      await expect(field).toHaveAttribute("placeholder", `e.g. ${example}`);
      await expect(page.locator("#launcher .launcher-field .field-help")).toContainText(note);
      await expect(page.locator(injected)).toHaveCount(0);
    });

    await test.step("the edit form's variables table", async () => {
      await openForm(page, "/-/links/hostile/edit");
      const day = page.getByRole("group", { name: "Variable {day}" });
      await expect(day.getByLabel("Description")).toHaveValue(note);
      await expect(day.getByLabel("Example")).toHaveValue(example);
      await expect(page.locator("#preview .launcher-field .field-help")).toContainText(note);
      await expect(page.locator(injected)).toHaveCount(0);
    });

    await test.step("the create form's name conflict", async () => {
      await openForm(page, "/-/new");
      await page.getByLabel("Pattern").pressSequentially("hostile/x");
      await expect(page.locator("#name-status")).toContainText("go/hostile already exists");
      await expect(page.locator(injected)).toHaveCount(0);
    });

    await test.step("the miss page's suggestion", async () => {
      watch.allowStatus(`${DIRECTORY_URL}/hostil/x`);
      await page.goto(`${DIRECTORY_URL}/hostil/x`);
      await expect(page.locator(".miss-suggestion-description")).toHaveText(markup);
      await expect(page.locator(injected)).toHaveCount(0);
    });
  });
});

test("deleting sends the version the page showed, and refuses a link changed since", async () => {
  const created = await createLink({ routes: [{ pattern: "deleteme", destination: fixture.url("/gone") }] });

  await withPage({}, async (page, watch) => {
    // The refused DELETE is a 409, which the browser reports on the console.
    watch.allowStatus(`${DIRECTORY_URL}/-/api/v1/links/${created.id}?version=${created.version}`);
    /** @type {string[]} */
    const deletes = [];
    page.on("request", (request) => {
      if (request.method() === "DELETE") deletes.push(request.url());
    });
    await page.goto(`${DIRECTORY_URL}/-/links/deleteme/delete`);
    const button = page.getByRole("button", { name: "Delete go/deleteme" });
    await expect(button).toBeVisible();

    // Someone else changes the link after the page was shown.
    const changed = await fetch(`${DIRECTORY_URL}/-/api/v1/links/${created.id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ version: created.version, description: "changed", routes: [{ id: created.routes[0].id, pattern: "deleteme", destination: fixture.url("/gone") }] }),
    });
    expect(changed.status).toBe(200);

    await button.click();
    await expect(page.getByRole("alert")).toContainText("Changed since you opened this page");
    expect(deletes).toEqual([`${DIRECTORY_URL}/-/api/v1/links/${created.id}?version=${created.version}`]);
    expect((await apiLink(created.id)).status).toBe(200);

    // Seen as it is now, it can go.
    await page.reload();
    await Promise.all([page.waitForURL(`${DIRECTORY_URL}/-/`), page.getByRole("button", { name: "Delete go/deleteme" }).click()]);
    await expect(page.locator("#flash")).toContainText("Deleted go/deleteme.");
    expect(deletes[1]).toBe(`${DIRECTORY_URL}/-/api/v1/links/${created.id}?version=${created.version + 1}`);
    expect((await apiLink(created.id)).status).toBe(404);
  });
});

test("a taken name offers to add the route to that link, notes and all", async () => {
  const created = await createLink({ routes: [{ pattern: "standup", destination: fixture.url("/standup") }] });

  await withPage({}, async (page) => {
    await openForm(page, "/-/new");
    await page.getByLabel("Pattern").pressSequentially("standup/{team}");
    await page.getByLabel("Destination").fill(fixture.url("/standup?team={team}"));
    const team = page.getByRole("group", { name: "Variable {team}" });
    await team.getByLabel("Description").fill("Team name");
    await team.getByLabel("Example").fill("platform");

    const status = page.locator("#name-status");
    await expect(status).toContainText("Name taken: go/standup already exists.");
    await expect(status.getByRole("link", { name: "Open go/standup" })).toHaveAttribute("href", "/-/links/standup");
    await status.getByRole("button", { name: "Add this route to go/standup" }).click();

    await expect(page).toHaveURL(
      `${DIRECTORY_URL}/-/links/standup/edit?pattern=${encodeURIComponent("standup/{team}")}&destination=${encodeURIComponent(fixture.url("/standup?team={team}"))}`,
    );
    await expect(page.locator("#link-form[data-ready]")).toBeAttached();
    // The notice names what the new route opens.
    await expect(page.locator("#form-alert")).toContainText(`go/standup/{team} opens ${fixture.url("/standup?team={team}")}`);
    const handedOver = page.getByRole("group", { name: "Variable {team}" });
    await expect(handedOver.getByLabel("Description")).toHaveValue("Team name");
    await expect(handedOver.getByLabel("Example")).toHaveValue("platform");

    await Promise.all([page.waitForURL(`${DIRECTORY_URL}/-/links/standup`), page.getByRole("button", { name: "Save changes" }).click()]);
    await expect(page.locator("#flash")).toContainText("Saved go/standup.");
    const saved = (await apiLink(created.id)).body;
    expect(saved.routes.map((/** @type {{ pattern: string }} */ r) => r.pattern)).toEqual(["standup", "standup/{team}"]);
    expect(saved.routes[1].variables).toEqual([{ name: "team", type: "string", accepts: "any text", description: "Team name", example: "platform" }]);
  });
});

test("creating a link says so on its page", async () => {
  await withPage({}, async (page) => {
    await openForm(page, "/-/new");
    await page.getByLabel("Pattern").pressSequentially("fresh");
    await page.getByLabel("Destination").fill(fixture.url("/fresh"));
    await expect(page.locator("#name-status")).toContainText("go/fresh is free");
    await Promise.all([page.waitForURL(`${DIRECTORY_URL}/-/links/fresh`), page.getByRole("button", { name: "Create link" }).click()]);
    await expect(page.locator("#flash")).toContainText("Created go/fresh.");
    // Once: a reload shows the page without it.
    await page.reload();
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("go/fresh");
    await expect(page.locator("#flash")).toBeEmpty();
  });
});

test("the variables table writes a type where the server says the variable is, in any script", async () => {
  await withPage({}, async (page) => {
    for (const before of ["r/{a:regex(é+)}/{day}", "r/{a:regex(😀+)}/{day}", "r/{a:regex(ż😀ż+)}/{day}/{b}"]) {
      await test.step(before, async () => {
        await openForm(page, `/-/new?pattern=${encodeURIComponent(before)}`);
        const pattern = page.getByLabel("Pattern");
        await expect(pattern).toHaveValue(before);
        // Each variable's type selector is named after its variable.
        await page.getByRole("combobox", { name: "Type of {day}" }).selectOption("int");
        await expect(pattern).toHaveValue(before.replace("{day}", "{day:int}"));
      });
    }
  });
});

test("renaming the first route renames every route of the link", async () => {
  const created = await createLink({
    routes: [
      { pattern: "ren/{repo}", destination: fixture.url("/acme/{repo}") },
      { pattern: "ren/{repo}/issues/{n:int}", destination: fixture.url("/acme/{repo}/issues/{n}") },
      { pattern: "ren", destination: fixture.url("/acme") },
    ],
  });

  await withPage({}, async (page) => {
    await openForm(page, "/-/links/ren/edit");
    const patterns = page.getByLabel("Pattern");
    const first = patterns.nth(0);
    await first.click();
    await page.keyboard.press("Home");
    for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
    await page.keyboard.type("ame");
    await expect(first).toHaveValue("rename/{repo}");
    await expect(patterns.nth(1)).toHaveValue("rename/{repo}/issues/{n:int}");
    await expect(patterns.nth(2)).toHaveValue("rename");
    await expect(page.locator("#name-status")).toContainText("saving renames go/ren to go/rename");

    await Promise.all([page.waitForURL(`${DIRECTORY_URL}/-/links/rename`), page.getByRole("button", { name: "Save changes" }).click()]);
    const saved = (await apiLink(created.id)).body;
    expect(saved.name).toBe("rename");
    expect(saved.routes.map((/** @type {{ pattern: string }} */ r) => r.pattern)).toEqual(["rename/{repo}", "rename/{repo}/issues/{n:int}", "rename"]);
  });
});

test("the launcher judges each value as it is typed, before the others", async () => {
  await withPage({}, async (page) => {
    await page.goto(`${DIRECTORY_URL}/-/links/reports`);
    const launcher = page.locator("#launcher");
    const month = launcher.getByLabel("month");
    await expect(month).toHaveAttribute("placeholder", "e.g. 10");
    await month.fill("13");
    await expect(month).toHaveAttribute("aria-invalid", "true");
    await expect(launcher.locator(".launcher-field .field-error").first()).toHaveText(
      `month (Month, 1 to 12) doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$. For example: "10".`,
    );
    await expect(launcher.getByRole("button", { name: "Open" })).toHaveAttribute("aria-disabled", "true");
    await month.fill("10");
    await expect(month).not.toHaveAttribute("aria-invalid");
    await expect(launcher.locator(".launcher-field .field-error").first()).toBeHidden();
  });
});

test("the regular expression field explains a bracket left open in its own terms", async () => {
  await withPage({}, async (page) => {
    await openForm(page, "/-/new");
    await page.getByLabel("Pattern").pressSequentially("rx/{v}");
    const variable = page.getByRole("group", { name: "Variable {v}" });
    await variable.getByLabel("Type").selectOption("regex");
    const expression = variable.getByLabel("Regular expression");
    for (const [typed, says] of [
      ["[0-9", "a character class is opened with [ and never closed with ]"],
      ["(a|b", "a parenthesis is opened and never closed"],
    ]) {
      await expression.fill(typed);
      const messages = variable.locator(".field-messages");
      await expect(messages).toContainText(says);
      await expect(messages).not.toContainText("regex(");
    }
    await expression.fill("[0-9]+");
    await expect(page.getByLabel("Pattern")).toHaveValue("rx/{v:regex([0-9]+)}");
    await expect(variable.locator(".variable-accepts")).toHaveText("Accepts text matching [0-9]+.");
  });
});

test("the form's preview opens a draft's destination in a new tab", async () => {
  await withPage({}, async (page) => {
    await openForm(page, "/-/new");
    await page.getByLabel("Pattern").pressSequentially("tabbed");
    await page.getByLabel("Destination").fill(fixture.url("/tabbed"));
    const preview = page.locator("#preview");
    await expect(preview).toContainText("Opens in a new tab.");
    const [opened] = await Promise.all([page.context().waitForEvent("page"), preview.getByRole("button", { name: "Open" }).click()]);
    await opened.waitForURL(fixture.url("/tabbed"));
    expect(page.url()).toBe(`${DIRECTORY_URL}/-/new`);
    await opened.close();
  });
});

test("search sends the query encoded and keeps the server's order", async () => {
  await createLink({ description: "R&D budget", routes: [{ pattern: "rnd", destination: fixture.url("/rnd") }] });

  await withPage({}, async (page) => {
    await page.goto(`${DIRECTORY_URL}/-/`);
    await page.locator("#q").pressSequentially("r&d");
    await expect(page.locator("#search-status")).toHaveText("1 link matches.");
    await expect(page.locator("#results .result-link")).toHaveText(["go/rnd"]);

    // Nothing found: the new link it offers starts from the query.
    await page.locator("#q").fill("");
    await page.locator("#q").pressSequentially("no such thing");
    await expect(page.locator("#search-status")).toHaveText("No link matches your search.");
    await expect(page.locator("#search-create-link")).toHaveAttribute("href", "/-/new?pattern=no%20such%20thing");

    await page.locator("#q").fill("");
    await page.locator("#q").pressSequentially("gh");
    const api = await fetch(`${DIRECTORY_URL}/-/api/v1/links?q=gh`).then((response) => response.json());
    const order = api.links.map((/** @type {{ name: string }} */ link) => `go/${link.name}`);
    expect(order.length).toBeGreaterThan(1);
    await expect(page.locator("#results .result-link")).toHaveText(order);
  });
});

test("Enter in the search box never opens a result of an older query", async () => {
  await withPage({}, async (page) => {
    await page.goto(`${DIRECTORY_URL}/-/`);
    await page.locator("#q").pressSequentially("gh");
    await expect(page.locator("#results .result").first()).toBeVisible();
    // More is typed, and Enter pressed before the new answer is in: the
    // results on screen are for "gh", so the server answers "ghzzz".
    await page.keyboard.type("zzz");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(`${DIRECTORY_URL}/-/?q=ghzzz`);
    await expect(page.locator("#search-status")).toHaveText("No link matches your search.");
    // Nothing found: the new link it offers starts from the query.
    await expect(page.locator("#search-create-link")).toHaveAttribute("href", "/-/new?pattern=ghzzz");
  });
});

test("the launcher encodes each value, and the destination gets it whole", async () => {
  await withPage({}, async (page) => {
    await page.goto(`${DIRECTORY_URL}/-/links/search`);
    const launcher = page.locator("#launcher");
    await launcher.getByLabel("q").fill("a/b?c#d");
    await expect(launcher.locator(".launcher-built")).toContainText("go/search/a%2Fb%3Fc%23d");
    await expect(launcher.locator(".launcher-output")).toContainText(fixture.url("/search?q=a%2Fb%3Fc%23d"));
    await Promise.all([page.waitForURL(fixture.url("/search?q=a%2Fb%3Fc%23d")), launcher.getByLabel("q").press("Enter")]);
    expect(fixture.requestsFor("/search?q=a%2Fb%3Fc%23d")).toHaveLength(1);
  });
});

test("copy buttons appear with JavaScript and copy the go link", async () => {
  await withPage({ permissions: ["clipboard-read", "clipboard-write"] }, async (page) => {
    await page.goto(`${DIRECTORY_URL}/-/links/reports`);
    const copy = page.locator('button[data-copy="go/reports"]');
    await expect(copy).toBeVisible();
    await expect(copy).toHaveText("Copy go link");
    await copy.click();
    await expect(copy).toHaveText("Copied");
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("go/reports");
    await expect(page.getByRole("button", { name: "Copy URL" })).toBeVisible();
  });
});

test("search says it shows the best results only when more matched", async () => {
  for (let i = 1; i <= 20; i++) {
    await createLink({ routes: [{ pattern: `zcap${String(i).padStart(2, "0")}`, destination: fixture.url("/cap") }] });
  }
  await withPage({}, async (page) => {
    for (const [extra, want] of [
      [false, "20 links match."],
      [true, "20 links match; showing the best 20."],
    ]) {
      if (extra) await createLink({ routes: [{ pattern: "zcap21", destination: fixture.url("/cap") }] });
      // The server's page and the results built as you type say the same.
      await page.goto(`${DIRECTORY_URL}/-/?q=zcap`);
      await expect(page.locator("#search-status")).toHaveText(String(want));
      await expect(page.locator("#results .result")).toHaveCount(20);
      await page.locator("#q").fill("");
      await page.locator("#q").pressSequentially("zcap");
      await expect(page.locator("#search-status")).toHaveText(String(want));
      await expect(page.locator("#results .result")).toHaveCount(20);
    }
  });
});

test("an answer that arrives after more was typed is not drawn over the newer text", async () => {
  await withPage({}, async (page) => {
    await openForm(page, "/-/new");
    const pattern = page.getByLabel("Pattern");
    await pattern.pressSequentially("jreports/{month}");
    await expect(page.getByRole("group", { name: "Variable {month}" })).toBeVisible();
    await expect(page.locator("#name-status")).toContainText("go/jreports is free");

    // From now on the server's answers about the draft are held back.
    /** @type {import("@playwright/test").Route[]} */
    const held = [];
    await page.route("**/-/api/v1/links/validate", (route) => {
      held.push(route);
    });
    // Saving asks first; while it waits, the user types at the start.
    await page.getByRole("button", { name: "Create link" }).click();
    await expect.poll(() => held.length).toBe(1);
    await pattern.press("Home");
    await pattern.press("x");
    await expect(pattern).toHaveValue("xjreports/{month}");
    await expect.poll(() => held.length).toBe(2); // the check of the new text
    // The answer about the old text arrives: the draft has no destination,
    // so the save stops, and the answer must not be taken for the new text.
    await held[0].continue();
    await expect(page.locator("#form-alert")).toContainText("This link can't be saved yet");
    await page.getByRole("group", { name: "Variable {month}" }).getByLabel("Type").selectOption("int");
    await expect(pattern).toHaveValue("xjreports/{month:int}");
    await page.unroute("**/-/api/v1/links/validate");
    await held[1].continue().catch(() => {}); // aborted by the newer check
  });
});

test("long names and words fit a phone on every page that shows them", async () => {
  const name = `long${"x".repeat(56)}`;
  const word = "y".repeat(120);
  await createLink({ description: `A description with ${word} in it`, routes: [{ pattern: `${name}/{n:int}`, destination: fixture.url(`/${word}?n={n}`) }] });

  await withPage({ viewport: { width: 375, height: 800 }, isMobile: true, hasTouch: true }, async (page, watch) => {
    for (const [label, target, status] of [
      ["unknown-name miss page", `/${name}z/abc/def`, 404],
      ["known-name miss page", `/${name}/notanumber`, 404],
      ["link page", `/-/links/${name}`, 200],
      ["delete confirmation", `/-/links/${name}/delete`, 200],
      ["edit form", `/-/links/${name}/edit`, 200],
      ["search", `/-/?q=${name}`, 200],
    ]) {
      await test.step(String(label), async () => {
        if (status !== 200) watch.allowStatus(`${DIRECTORY_URL}${target}`);
        const response = await page.goto(`${DIRECTORY_URL}${target}`);
        expect(response?.status()).toBe(status);
        await expect(page.locator("main")).toContainText(name.slice(0, 20));
        await expectNoHorizontalScroll(page, String(label));
      });
    }
  });
});
