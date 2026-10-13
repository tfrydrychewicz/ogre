# Ogre, the godir extension

A Manifest V3 extension for Chrome. It sends every go link you open (`go/name`, `http://go/name`, `https://go/name`) to your godir Directory, which by default runs at `http://localhost:4655`.

There is no build step. To load it, open `chrome://extensions`, switch on "Developer mode", choose "Load unpacked", and select this directory.

## What it asks for, and why

**`declarativeNetRequestWithHostAccess`** lets the extension register one redirect rule: a request for a page on the host `go` is sent to the same path on the Directory. Chrome applies the rule itself, so the extension never sees the pages you visit, and the rule keeps working while the extension's background script is stopped. This permission acts only on the hosts listed below.

**Host `*://go/*`** is what lets the redirect rule act on go links. `go` is a bare host name that exists on no public network, so this covers go links and nothing else.

**Host `http://localhost:4655/*`** is the default address of a Directory on your own computer. The extension needs it to check that the Directory is running; that connection test arrives with the settings page.

The extension asks for nothing else: it cannot read your browsing history, your tabs, or the content of any page.

## Files

| File | Does |
|---|---|
| `manifest.json` | Name, version, and the permissions above |
| `background.js` | Service worker. Makes sure the redirect rule is installed: when the extension is installed, when the browser starts, and each time Chrome starts the worker. It writes only when the rule is missing or wrong |
| `lib/config.js` | The default Directory URL and the redirect rule. No browser calls, so it can be tested with Node.js |
| `*.test.js` | Tests for the rule, the service worker (against a stand-in for Chrome) and the manifest. Run them from the repository root with `npm run test:extension` |
