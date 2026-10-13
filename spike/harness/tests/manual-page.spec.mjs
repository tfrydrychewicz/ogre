// The manual checklist page (spike/stub/manual.html) cannot be tried by a
// person on every change, so this makes sure it loads and that its request
// detection does what its text promises. It records no check result: the
// manual checks themselves need a person and Chrome stable.
//
// The stub runs with a temporary results directory, so a manual.json saved by
// a real manual run is never touched.

import { expect, test } from "@playwright/test";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { DIRECTORY_URL, buildStub, launchWithoutExtension, startStub } from "../lib/env.mjs";

/** GET a raw path from the stub, the way a browser navigation would look in its log. */
function hit(rawPath, headers = {}) {
  return new Promise((resolve, reject) => {
    http
      .get({ host: "127.0.0.1", port: 4655, path: rawPath, headers: { Host: "localhost:4655", ...headers } }, (response) => {
        response.resume();
        response.on("end", resolve);
      })
      .on("error", reject);
  });
}

test("the manual checklist loads, detects requests as described, and saves", async () => {
  buildStub();
  const resultsDir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-manual-results-"));
  const stub = await startStub({ resultsDir });
  const { context } = await launchWithoutExtension();
  try {
    const page = await context.newPage();
    const problems = [];
    page.on("pageerror", (error) => problems.push(String(error)));
    page.on("console", (message) => message.type() === "error" && problems.push(message.text()));

    await page.goto(`${DIRECTORY_URL}/-/spike/manual`);
    await expect(page.locator("#stub")).toContainText("answering");
    const steps = await page.locator("li.step").count();
    expect(steps).toBeGreaterThanOrEqual(25);
    await expect(page.locator("#go-link-slot a")).toHaveCount(0); // no link to go before A, B and C are recorded

    const outcome = (id) => page.locator(`input[name="outcome-${id}"]:checked`);
    const seen = (id) => page.locator(`#seen-${id}`);

    // "/" before step A has an outcome is a direct visit, not step B.
    await hit("/");
    await hit("/m3", { "Sec-Purpose": "prefetch" }); // a speculative load is not the user's navigation
    await hit("/m2");
    await expect(outcome("A")).toHaveValue("navigated");
    await page.waitForTimeout(1500); // more than one poll of the log: the earlier "/" must stay ignored
    await expect(seen("B")).toContainText("has not received");
    await expect(seen("C")).toContainText("has not received");

    // Step B is marked as reached but never answered automatically.
    await hit("/");
    await expect(seen("B")).toContainText("A request for / arrived");
    await expect(outcome("B")).toHaveCount(0);

    await hit("/m3");
    await expect(outcome("C")).toHaveValue("navigated");
    await page.locator('input[name="outcome-B"][value="navigated"]').check();
    await expect(page.locator("#go-link-slot a")).toHaveAttribute("href", "http://go/m4");

    // An encoded path: exact arrival, altered arrival, and requests that only look like a step.
    await hit("/m9/a%20b%2Fc");
    await expect(outcome("H3")).toHaveValue("intact");
    await hit("/m5%20x");
    await expect(page.locator("#altered-F1")).toContainText("/m5%20x");
    await expect(outcome("F1")).toHaveCount(0);
    await hit("/m77");
    await expect(page.locator("#unmatched")).toContainText("/m77");

    // Answers are saved without a click, with the expected outcome and the comparison.
    await page.locator("#go-resolves").fill("no output");
    await expect(page.locator("#save-status")).toContainText("Saved to", { timeout: 5000 });
    await expect
      .poll(() => JSON.parse(readFileSync(path.join(resultsDir, "manual.json"), "utf8")).goResolves, { timeout: 5000 })
      .toBe("no output");
    const saved = JSON.parse(readFileSync(path.join(resultsDir, "manual.json"), "utf8"));
    const step = (id) => saved.steps.find((candidate) => candidate.step === id);
    expect(saved.steps).toHaveLength(steps);
    expect(step("A")).toMatchObject({ check: "2", expectedOutcome: "searched", outcome: "navigated", asExpected: false, autoDetected: true });
    expect(step("B")).toMatchObject({ check: "1", expectedOutcome: "navigated", outcome: "navigated", asExpected: true });
    expect(step("H3").detectedRequest.requestURI).toBe("/m9/a%20b%2Fc");
    expect(step("F1")).toMatchObject({ outcome: null, asExpected: null, alteredRequests: ["/m5%20x"] });
    expect(step("G2")).toMatchObject({ expectedOutcome: null, asExpected: null });
    expect(saved.unmatchedRequests).toEqual(["/m77"]);

    // Answers survive a reload (step L1 closes the browser).
    await page.reload();
    await expect(outcome("B")).toHaveValue("navigated");
    expect(problems).toEqual([]);
  } finally {
    await context.close();
    await stub.stop();
    rmSync(resultsDir, { recursive: true, force: true });
  }
});
