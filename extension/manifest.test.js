// The manifest, checked without a browser: a mistake here is otherwise only
// seen when the end-to-end tests fail to load the extension.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { DEFAULT_DIRECTORY_URL } from "./lib/config.js";

const manifest = JSON.parse(readFileSync(new URL("./manifest.json", import.meta.url), "utf8"));

test("the manifest identifies a Manifest V3 extension named Ogre", () => {
  assert.equal(manifest.manifest_version, 3);
  assert.equal(manifest.name, "Ogre");
  assert.match(manifest.version, /^\d+\.\d+\.\d+$/);
});

test("the permissions are exactly those of plan section 3.7 for this milestone", () => {
  // The redirect rule, with no access beyond the hosts below.
  assert.deepEqual(manifest.permissions, ["declarativeNetRequestWithHostAccess"]);
  // The go links the rule acts on, and the default Directory.
  assert.deepEqual(manifest.host_permissions, ["*://go/*", `${DEFAULT_DIRECTORY_URL}/*`]);
  assert.equal(manifest.optional_permissions, undefined);
  assert.equal(manifest.optional_host_permissions, undefined);
});

test("the host permission and the redirect rule name the same Directory", () => {
  // lib/config.js says "keep the two in step"; this is what keeps them so.
  assert.equal(DEFAULT_DIRECTORY_URL, "http://localhost:4655");
  assert.ok(manifest.host_permissions.includes("http://localhost:4655/*"));
});

test("the service worker is background.js, loaded as a module", () => {
  // background.js uses import; without "type": "module" it does not start.
  assert.deepEqual(manifest.background, { service_worker: "background.js", type: "module" });
  const source = readFileSync(new URL("./background.js", import.meta.url), "utf8");
  assert.match(source, /^import /m);
});

test("there are no pages, scripts or externally reachable parts yet", () => {
  for (const key of ["content_scripts", "action", "options_page", "options_ui", "web_accessible_resources", "externally_connectable"]) {
    assert.equal(manifest[key], undefined, key);
  }
});
