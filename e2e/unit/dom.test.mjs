// Unit tests for the web UI's lib/dom.js safeURL: the second line, behind
// the server's own check, that keeps a javascript: or data: URL out of an
// href or src. A browser reads a URL with its URL parser, which drops tabs
// and line breaks anywhere and control characters at the ends, so the
// check must read it the same way. Run with: npm run test:web

import assert from "node:assert/strict";
import { test } from "node:test";

import { safeURL } from "../../internal/web/static/js/lib/dom.js";

test("http, https, mailto and relative URLs are allowed", () => {
  for (const url of [
    "https://example.com/a?b=c#d",
    "http://localhost:4655/x",
    "HTTPS://EXAMPLE.COM/",
    "mailto:someone@example.com",
    "/-/links/reports",
    "/-/new?pattern=a%2Fb",
    "#launch",
    "reports",
    "//other.example/x",
  ]) {
    assert.equal(safeURL(url), true, url);
  }
});

test("script and data URLs are refused however they are written", () => {
  for (const url of [
    "javascript:alert(1)",
    "JavaScript:alert(1)",
    " javascript:alert(1)",
    "java\tscript:alert(1)",
    "java\nscript:alert(1)",
    "java\rscript:alert(1)",
    "\u0001javascript:alert(1)",
    "\u0000javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "d\tata:text/html,x",
    "vbscript:msgbox(1)",
    "blob:https://example.com/uuid",
    "file:///etc/passwd",
  ]) {
    assert.equal(safeURL(url), false, JSON.stringify(url));
  }
});

test("text the URL parser refuses is refused", () => {
  assert.equal(safeURL("http://[::1"), false);
});
