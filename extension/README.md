# The Ogre extension

A Manifest V3 extension for Chrome. It sends every go link you open (`go/name`, `http://go/name`, `https://go/name`) to your Directory, the Ogre server, which by default runs at `http://localhost:4655`.

There is no build step. To load it, open `chrome://extensions`, switch on "Developer mode", choose "Load unpacked", and select this directory. Chrome 116 or newer.

## What you see

- **The first-run page** opens once, when the extension is installed. It checks that your Ogre server answers (and says how to start it if not: `go run ./cmd/ogre` or `make run` in your checkout, or `bin/ogre`), then asks you to type `go/` and press Enter, and notices by itself when you have. Typing `go/` once teaches Chrome that `go` is a site, so that afterwards `go/name` opens the link instead of searching for it. The page asks for that only once the server answers, because a `go/` visit that fails teaches Chrome nothing. Last, it has you try a link without the trailing slash and explains the fallback: type `go/` once more, or write `go/name/` or `http://go/name`. Once it has seen `go/` (or you choose "Don't show this page again") it never opens by itself again; the settings page links to it.
- **The settings page** (on `chrome://extensions`, Details under Ogre, then "Extension options"; or the Extensions menu in the toolbar, the three dots next to Ogre, then "Options") holds the Directory URL. "Test connection" asks the server's `/-/api/v1/meta` whether it is an Ogre server speaking API version 1, and shows its version. Save tests the same way first; an address where nothing answers can still be saved after a warning, for a server you will start later. An address that is not plainly `http://` or `https://`, a host and an optional port (no path, no user name, not the host `go`) cannot be saved, and the page says what to change. An address typed without `http://` is refused with the corrected form, not guessed.

## What it asks for, and why

**`declarativeNetRequestWithHostAccess`** lets the extension register one redirect rule: a request for a page on the host `go`, over http or https, is sent to the same path on the Directory. Chrome applies the rule itself, so the extension never sees the pages you visit, and the rule keeps working while the extension's background script is stopped. This permission acts only on the hosts listed below.

**Host `*://go/*`** is what lets the redirect rule act on go links. `go` is a bare host name that exists on no public network, so this covers go links and nothing else.

**Host `http://localhost:4655/*`** is the default address of a Directory on your own computer. The settings and first-run pages need it to ask that server whether it is running.

**`storage`** keeps the Directory URL you saved, and whether the first-run page is done, in Chrome's local extension storage on this computer. Nothing is synced.

**`webRequest`** lets the first-run page notice that you typed `go/`. It observes only: it cannot block, change or delay a request. It listens only while the first-run page is open, and Chrome reports to it only requests to hosts the extension has access to, which here means go links. Nothing it sees is stored or sent. It cannot tell a typed `go/` from one a web page in another tab opened (Chrome names the initiating page only to an extension with access to it), so such a page could mark the first-run steps done; telling them apart would take the `webNavigation` permission, which is not worth it for a hint.

**Optional host access to `http://*/*` and `https://*/*`** is declared so that a Directory at another address can be allowed. It is never granted as a whole: when you test or save another address, Chrome asks you about that one server, and grants that one origin. When you save, the extension gives up access to any server other than the one saved (never the default one, which it always has). Access allowed only to test an address that was never saved is given up the next time Chrome starts, or when the extension is installed or updated.

The extension asks for nothing else. It has no content scripts and cannot read the content of any page, your tabs or your history. It loads no code from anywhere: every script is in this directory, and its pages run under Chrome's default policy for extension pages, with no inline script or style. It collects nothing and talks to no server but the Directory you configured, and only when one of its pages asks the Directory whether it is running.

## How it works inside

- **One writer.** Only the service worker changes the redirect rule, the stored URL and the granted access, one change at a time. The settings page asks it with a message (`ogre:set-directory`); a message from anything but the extension's own pages is ignored. The rule is replaced in a single `updateDynamicRules` call that removes the old rule and adds the new one, then the URL is stored, then old access is released. If the rule or the storage cannot be written, nothing is left changed.
- **The rule follows the stored URL.** The worker checks the rule when the extension is installed or updated, when Chrome starts, each time the worker starts, and whenever the stored URL changes. It writes only when the rule is missing or wrong.
- **Storage keys** (`chrome.storage.local`): `directoryUrl` (the Directory's origin, absent until you save one), `firstRunDone` (`true` once the first-run page is finished or dismissed), `firstRunSawGo` (`true` once it saw `go/`). In `chrome.storage.session`, kept in memory only: `installHandled` (when the worker last finished handling an install event, so the end-to-end tests can wait for it).
- **The first-run page follows the stored URL** too: saved in the settings page while it is open, the new address is checked at once.

## Files

| File | Does |
|---|---|
| `manifest.json` | Name, version, icons, the settings page, and the permissions above |
| `background.js` | Service worker: keeps the redirect rule in line with the stored URL, opens the first-run page once, and changes the Directory when the settings page asks |
| `lib/config.js` | The rules, with no browser calls: what a valid Directory URL is and its origin, the redirect rule, which access to release, the storage keys |
| `lib/api.js` | The connection test: `GET /-/api/v1/meta` with a timeout |
| `lib/settings.js` | Reading the stored Directory, asking Chrome for access, and asking the worker to change the Directory |
| `lib/page.js` | Small helpers for the pages; text goes in as text, never as markup |
| `options/` | The settings page |
| `onboarding/` | The first-run page |
| `shared/page.css` | The pages' stylesheet: the web UI's color tokens and components, copied, since the extension cannot load the Directory's |
| `icons/` | The icon at 16, 32, 48 and 128 pixels |
| `*.test.js` | Tests for the rules, the connection test, the service worker (against a stand-in for Chrome), and the manifest, including a scan that fails on a new permission, an inline script, a string evaluated as code, or a URL of another server. Run them from the repository root with `npm run test:extension` |

## Testing it

`npm run test:extension` runs the unit tests above. The end-to-end tests load this directory into Playwright's Chromium (`e2e/extension.spec.mjs`, `e2e/settings.spec.mjs`, `e2e/first-run.spec.mjs`, `e2e/passthrough.spec.mjs`, `e2e/fetch-metadata.spec.mjs`).

Headless Chromium never answers Chrome's permission prompt: a `chrome.permissions.request` that would show it stays pending. The tests stand in for the user's "Allow" with `chrome.developerPrivate.addHostPermission(extensionId, "http://localhost:4656/*")`, run on `chrome://extensions`, which is what Chrome's own "Site access" settings call. For a host this extension lists as optional, that records the approval without activating it; the extension's own `chrome.permissions.request` for the origin then resolves `true` at once, with no prompt, so the settings page's request code still runs (`approveHostAccess` in `e2e/lib/harness.mjs`). Writing `directoryUrl` to storage directly also re-points the rule, but skips the access check the worker makes on a save.

What only a person can check in real Chrome is in [docs/manual-tests.md](../docs/manual-tests.md): the permission prompt itself and Deny, typing in the address bar, and start-up after a real restart (Chromium started with `--load-extension` reports a fresh install on every launch and never a browser start).
