// godir spike: status page. Shows what the extension has installed and seen.

const DIRECTORY_META = "http://localhost:4655/-/api/v1/meta";

const stateEl = /** @type {HTMLPreElement} */ (document.getElementById("state"));
const clearButton = /** @type {HTMLButtonElement} */ (document.getElementById("clear"));
const probeButton = /** @type {HTMLButtonElement} */ (document.getElementById("probe"));

/** What this page's own fetch of the Directory's meta endpoint gave; null until tried. */
let pageMeta = null;

/**
 * Asks the Directory for its meta endpoint, once from this extension page and
 * once from the service worker (which stores its answer as "workerMeta").
 */
async function probe() {
  const result = { from: "extension page", url: DIRECTORY_META, time: new Date().toISOString() };
  try {
    const response = await fetch(DIRECTORY_META, { cache: "no-store", signal: AbortSignal.timeout(3000) });
    result.ok = response.ok;
    result.status = response.status;
    result.body = await response.text();
  } catch (err) {
    result.ok = false;
    result.error = String(err);
  }
  pageMeta = result;
  try {
    await chrome.runtime.sendMessage({ type: "probeMeta" });
  } catch (err) {
    console.error("[godir spike] the service worker did not answer the probe request", err);
  }
  await render();
}

/** Reads the current state and prints it as JSON into <pre id="state">. */
async function render() {
  try {
    const [rules, stored] = await Promise.all([
      chrome.declarativeNetRequest.getDynamicRules(),
      chrome.storage.local.get({ goSeen: [], directoryErrors: [], lifecycle: [], workerMeta: null }),
    ]);
    stateEl.textContent = JSON.stringify(
      {
        extensionId: chrome.runtime.id,
        rules,
        lifecycle: stored.lifecycle,
        meta: { page: pageMeta, worker: stored.workerMeta },
        goSeen: stored.goSeen,
        directoryErrors: stored.directoryErrors,
      },
      null,
      2,
    );
  } catch (err) {
    stateEl.textContent = JSON.stringify({ extensionId: chrome.runtime.id, statusError: String(err) }, null, 2);
  }
}

// Clearing goes through the service worker, which queues it behind any append
// it has under way. Writing storage from here could be undone by such an append.
clearButton.addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "clear" });
  await render();
});
probeButton.addEventListener("click", () => void probe());

void probe();
setInterval(render, 1000);
