// @ts-check
// The manifest and the extension's files, checked without a browser: the
// permission audit of plan L4.4 (EXT-4, NFR-18), made permanent. A new
// permission, a content script, an inline script or a remote URL fails here
// before anyone loads the extension.
// Run with: npm run test:extension

import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { DEFAULT_DIRECTORY_URL, FIRST_RUN_PAGE, REQUIRED_HOST_PATTERNS } from "./lib/config.js";

const ROOT = path.dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(readFileSync(path.join(ROOT, "manifest.json"), "utf8"));

/**
 * Every file of the extension, relative to its root, except its tests:
 * what Chrome loads when the directory is loaded unpacked.
 *
 * @param {string} [dir]
 * @returns {string[]}
 */
function shippedFiles(dir = ROOT) {
  return readdirSync(dir).flatMap((name) => {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) return shippedFiles(full);
    const relative = path.relative(ROOT, full).split(path.sep).join("/");
    return relative.endsWith(".test.js") ? [] : [relative];
  });
}

/** @param {string} file relative to the extension's root */
const read = (file) => readFileSync(path.join(ROOT, file), "utf8");

/**
 * A script without its comment lines (those starting with //, /** or *),
 * which is how every comment in the extension is written. Comments may
 * name example URLs and JSDoc type imports; code may not.
 *
 * @param {string} file
 * @returns {string}
 */
const code = (file) =>
  read(file)
    .split("\n")
    .filter((line) => !/^\s*(?:\/\/|\/?\*)/.test(line))
    .join("\n");

test("the manifest identifies a Manifest V3 extension named Ogre", () => {
  assert.equal(manifest.manifest_version, 3);
  assert.equal(manifest.name, "Ogre");
  assert.match(manifest.version, /^\d+\.\d+\.\d+$/);
  // chrome.runtime.getContexts, which keeps a second first-run tab from
  // opening, arrived in Chrome 116.
  assert.equal(manifest.minimum_chrome_version, "116");
});

test("the permissions are exactly those of plan section 3.7 for this milestone", () => {
  // Adding a key here is a decision: say in extension/README.md why it is
  // needed, and in plan section 3.7.
  assert.deepEqual(manifest.permissions, ["declarativeNetRequestWithHostAccess", "storage", "webRequest"]);
  assert.deepEqual(manifest.host_permissions, ["*://go/*", `${DEFAULT_DIRECTORY_URL}/*`]);
  // Broad, but optional: Chrome grants only the one origin the user confirms
  // when saving a Directory at another address.
  assert.deepEqual(manifest.optional_host_permissions, ["http://*/*", "https://*/*"]);
  assert.equal(manifest.optional_permissions, undefined);
});

test("the manifest's host access and config.js name the same Directory", () => {
  // lib/config.js says "keep the two in step"; this is what keeps them so.
  assert.equal(DEFAULT_DIRECTORY_URL, "http://localhost:4655");
  assert.deepEqual(manifest.host_permissions, [...REQUIRED_HOST_PATTERNS]);
});

test("the manifest has only the keys this milestone needs", () => {
  assert.deepEqual(Object.keys(manifest).sort(), [
    "background",
    "description",
    "host_permissions",
    "icons",
    "manifest_version",
    "minimum_chrome_version",
    "name",
    "optional_host_permissions",
    "options_ui",
    "permissions",
    "version",
  ]);
});

test("there are no content scripts, no externally reachable parts and no relaxed security policy", () => {
  for (const key of [
    "content_scripts",
    "content_security_policy", // the default policy for extension pages stays
    "web_accessible_resources",
    "externally_connectable",
    "action", // the popup arrives in L6
    "omnibox", // the keyword arrives later (EXT-7)
    "options_page",
    "chrome_url_overrides",
    "sandbox",
    "key",
    "update_url",
  ]) {
    assert.equal(manifest[key], undefined, key);
  }
});

test("the service worker is background.js, loaded as a module", () => {
  // background.js uses import; without "type": "module" it does not start.
  assert.deepEqual(manifest.background, { service_worker: "background.js", type: "module" });
  assert.match(read("background.js"), /^import /m);
});

test("the settings page opens in a tab, and both pages exist", () => {
  assert.deepEqual(manifest.options_ui, { page: "options/options.html", open_in_tab: true });
  assert.ok(existsSync(path.join(ROOT, manifest.options_ui.page)));
  assert.ok(existsSync(path.join(ROOT, FIRST_RUN_PAGE)));
});

test("the icons exist as PNG files of the sizes the manifest claims", () => {
  assert.deepEqual(Object.keys(manifest.icons), ["16", "32", "48", "128"]);
  for (const [size, file] of Object.entries(manifest.icons)) {
    const bytes = readFileSync(path.join(ROOT, /** @type {string} */ (file)));
    assert.deepEqual([...bytes.subarray(0, 8)], [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], `${file} is a PNG`);
    // The IHDR chunk follows the signature: width and height, big-endian.
    assert.equal(bytes.readUInt32BE(16), Number(size), `${file} width`);
    assert.equal(bytes.readUInt32BE(20), Number(size), `${file} height`);
  }
});

test("pages load only their own files, with no inline script, style or handler", () => {
  const pages = shippedFiles().filter((file) => file.endsWith(".html"));
  assert.deepEqual(pages.sort(), ["onboarding/onboarding.html", "options/options.html"]);
  for (const page of pages) {
    const html = read(page);
    // Every script has a src, and it is a file of the extension.
    for (const match of html.matchAll(/<script\b([^>]*)>/gi)) {
      const src = /\bsrc="([^"]+)"/.exec(match[1])?.[1];
      assert.ok(src, `${page}: a script without src`);
      assert.ok(existsSync(path.join(ROOT, path.dirname(page), src)), `${page}: ${src} exists`);
    }
    assert.doesNotMatch(html, /<style\b/i, `${page}: a style element`);
    assert.doesNotMatch(html, /\sstyle=/i, `${page}: a style attribute`);
    assert.doesNotMatch(html, /\son[a-z]+=/i, `${page}: an inline event handler`);
    assert.doesNotMatch(html, /javascript:/i, `${page}: a javascript: URL`);
    // Nothing is loaded from a server: no src or href to http(s), and
    // every relative one names a file that exists.
    for (const match of html.matchAll(/\b(?:src|href)="([^"#]+)"/gi)) {
      const target = match[1];
      assert.doesNotMatch(target, /^[a-z][a-z0-9+.-]*:|^\/\//i, `${page}: ${target} is not one of the extension's files`);
      assert.ok(existsSync(path.join(ROOT, path.dirname(page), target)), `${page}: ${target} exists`);
    }
  }
});

test("no script evaluates strings as code or loads code from elsewhere", () => {
  const scripts = shippedFiles().filter((file) => file.endsWith(".js"));
  assert.ok(scripts.length >= 7, "the scan found the scripts");
  for (const file of scripts) {
    const source = code(file);
    assert.doesNotMatch(source, /\beval\s*\(|\bnew\s+Function\s*\(|\bimportScripts\s*\(/, `${file}: evaluates a string as code`);
    assert.doesNotMatch(source, /\bset(?:Timeout|Interval)\s*\(\s*["'`]/, `${file}: a timer with a string`);
    // Static imports are relative paths to the extension's own modules.
    for (const match of source.matchAll(/^\s*import\s[^"']*["']([^"']+)["']/gm)) {
      assert.match(match[1], /^\.\.?\//, `${file}: imports ${match[1]}`);
      assert.ok(existsSync(path.join(ROOT, path.dirname(file), match[1])), `${file}: ${match[1]} exists`);
    }
    assert.doesNotMatch(source, /\bimport\s*\(/, `${file}: a dynamic import`);
  }
});

test("the only server any script names is the Directory", () => {
  // NFR-18: no telemetry, no other server. Outside comments, a URL literal
  // in a script may name only the default Directory or the host go (in a
  // pattern or an example); anything else must be looked at.
  for (const file of shippedFiles().filter((name) => name.endsWith(".js"))) {
    // "$" ends a match: `http://${text}` builds a URL from what the user typed.
    for (const match of code(file).matchAll(/\bhttps?:\/\/[\w[][^\s"'`)\]},;$]*/g)) {
      if (/^https?:\/\/(?:localhost:4655|go)(?:[/.:]|$)/.test(match[0])) continue;
      assert.fail(`${file} names ${match[0]}; the extension contacts no server but the configured Directory`);
    }
  }
});

test("every file the extension ships is one of these kinds", () => {
  for (const file of shippedFiles()) {
    assert.match(file, /\.(?:js|html|css|json|png|md)$/, file);
  }
});
