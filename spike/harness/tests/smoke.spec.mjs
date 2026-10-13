// Proves that an unpacked Manifest V3 extension loads in Playwright's headless
// Chromium on this machine. Uses a tiny extension written to a temp dir, so it
// depends on neither spike/extension nor the stub.

import { expect, test } from "@playwright/test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromiumVersion, extensionIdForDir, launchWithExtension } from "../lib/env.mjs";

test("headless Chromium loads an unpacked MV3 extension and starts its service worker", async () => {
  const extensionDir = mkdtempSync(path.join(os.tmpdir(), "godir-spike-smoke-ext-"));
  writeFileSync(
    path.join(extensionDir, "manifest.json"),
    JSON.stringify({
      manifest_version: 3,
      name: "godir spike smoke",
      version: "0.0.1",
      permissions: ["declarativeNetRequestWithHostAccess"],
      host_permissions: ["*://go/*"],
      background: { service_worker: "background.js", type: "module" },
    }),
  );
  writeFileSync(path.join(extensionDir, "background.js"), "globalThis.smokeLoaded = true;\n");
  writeFileSync(path.join(extensionDir, "page.html"), "<!doctype html><title>smoke page</title><p id=ok>ok</p>\n");

  const started = Date.now();
  const { context, extensionId, serviceWorker } = await launchWithExtension({ extensionDir });
  const launchMs = Date.now() - started;
  try {
    expect(serviceWorker.url()).toBe(`chrome-extension://${extensionId}/background.js`);
    expect(extensionId).toMatch(/^[a-p]{32}$/);
    expect(extensionId).toBe(extensionIdForDir(extensionDir));

    // The worker runs our code and has the extension APIs the spike needs.
    const inWorker = await serviceWorker.evaluate(async () => ({
      loaded: globalThis.smokeLoaded,
      runtimeId: chrome.runtime.id,
      name: chrome.runtime.getManifest().name,
      dynamicRules: await chrome.declarativeNetRequest.getDynamicRules(),
    }));
    expect(inWorker).toEqual({ loaded: true, runtimeId: extensionId, name: "godir spike smoke", dynamicRules: [] });

    // Extension pages open (status.html and unreachable.html will rely on this).
    const page = await context.newPage();
    await page.goto(`chrome-extension://${extensionId}/page.html`);
    await expect(page.locator("#ok")).toHaveText("ok");

    const version = await chromiumVersion(context);
    expect(version).toMatch(/^\d+\.\d+\.\d+\.\d+$/);
    const userAgent = await page.evaluate(() => navigator.userAgent);
    console.log(`chromium ${version}; launch + service worker in ${launchMs} ms; extension id ${extensionId}`);
    console.log(`user agent: ${userAgent}`);
  } finally {
    await context.close();
    rmSync(extensionDir, { recursive: true, force: true });
  }
});
