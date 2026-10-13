// godir spike: unreachable page. Shown by background.js in place of a
// Directory tab that failed to load.
//
// It never navigates by itself. Retry is a plain link the user clicks; if that
// navigation fails too, background.js shows this page again. No timers, no
// automatic retry, so there is no loop to get stuck in.

const params = new URLSearchParams(location.search);
const originalUrl = params.get("url") ?? "";
const error = params.get("error") ?? "";

/**
 * The Retry target, or null when the "url" parameter is not a plain web URL.
 * Anything else (javascript:, data:, chrome-extension:, garbage) gets no link.
 * @param {string} value
 * @returns {string | null}
 */
function retryTarget(value) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    return null;
  }
  return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : null;
}

/** @param {string} id */
function byId(id) {
  return /** @type {HTMLElement} */ (document.getElementById(id));
}

byId("original-url").textContent = originalUrl || "(none)";
byId("error").textContent = error || "(none)";

const retry = /** @type {HTMLAnchorElement} */ (byId("retry"));
const target = retryTarget(originalUrl);
if (target) {
  retry.href = target;
} else {
  retry.textContent = "Retry unavailable: no valid original URL";
}
