# Chrome spike (L0): results

| | |
|---|---|
| **Date** | 2026-10-13 (automated run finished 16:24 CEST; manual run 16:53 to 17:06 CEST) |
| **Status** | Complete: every check has a recorded result, none failed, and the L0.4 decisions are made |
| **Chrome used for the manual run** | Google Chrome 155.0.8059.40 stable, fresh throwaway profile |
| **Chromium used by automation** | 156.0.8078.4, Playwright's bundled "Google Chrome for Testing", headless, field trials off |
| **OS** | macOS 26.5 (build 25F71), arm64 |

This is the record that task L0.2 of [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) asks for. The checks are those of the plan's Appendix B. The code that produced the results lived in `spike/`, which was deleted when L1 started, as planned; it is in the repository's history at the tag `l0-spike`, and the `spike/` paths and commands below refer to that tag. The raw results are kept in `docs/spike-results/`: `automated.json` and `omnibox-raw.json` (automation) and `manual.json` (the run by hand), with local file paths replaced by `<repo>`, `<tmp>`, and `~`, the two path-derived extension ids replaced by `<spike-extension-id>` and `<spike-extension-omnibox-id>`, and two location-specific Google suggestions replaced by a placeholder.

## What this establishes

**The design holds on Chrome stable.** The owner ran the 25-step checklist by hand in Chrome 155 with the spike extension loaded unpacked. Every step that had an expected outcome matched it.

- **Confirmed by hand on Chrome stable:** checks 1 to 6, 8 to 15, and 17. A fresh profile searches for `go/m2`; after `go/` has been typed once, `go/m3` navigates. The redirect works for typed links, clicked links (including one on a public HTTPS page, with no Local Network Access prompt), a bookmark, a link passed on Chrome's command line from Terminal while the test Chrome was running, `https://go/`, and both levels of "Always use secure connections". The same hand-off with Chrome closed (step L1) was reported as working, but the stub's log did not show the request. The plan's `open http://go/foo`, which goes through the default browser, was not tried. Query, fragment, and percent-encoding survive. With the stub stopped, the extension's unreachable page appears. The redirect rule survives a restart, and the address-bar keyword `go/` leaves plain go links alone.
- **Failed:** nothing.
- **Still proxy evidence only:** check 7 (`go` resolving elsewhere) has no manual step; it rests on the automated run in Chromium.
- **Recorded, with no expected outcome:** in Incognito, where the extension is not allowed, a go link ends on Chrome's error page (check 16). "Always use secure connections" is off in a fresh profile. Back from the unreachable page returns to the page the tab showed before, which differs from the headless run.

What the manual results are: the outcome the owner chose for each step on the checklist page. For 15 of the 25 steps the page also detected the request arriving at the stub and stored it. Seven of the other ten are steps the stub cannot see (C2, C3, D, G2, I, I2, L2), and in two no request was expected and none arrived (A, J). The tenth is step L1: the owner chose "Navigated to the stub", but the page, which watches for `/m14`, stored no such request. The hand-off with Chrome closed therefore rests on the owner's answer alone; the stub's log does not back it. No free-text notes were entered, so details such as the position of the autocomplete row, the `goSeen` and `lifecycle` lists, and whether Tab or Space enters keyword mode were not captured. The "Does `go` resolve here?" field was also left empty, so the results do not say whether a host called `go` exists on this network; the Incognito result (check 16) depends on that.

## Results

How each check was tested by automation:

- **Automated in Chromium**: the real action (a mouse click, a stopped server, a relaunch) in headless Chromium. Checks 4 and 14 also have a control run without the extension; check 12 has none.
- **Proxy via chrome://omnibox**: Playwright cannot type in the address bar. The debug page `chrome://omnibox` reports which match Chromium's autocomplete code would open on Enter. "Typed" visits were made with the DevTools command `Page.navigate` and transition type `typed`.
- **Proxy in Chromium**: another stand-in, named in the row.

The last column is the run by hand on Chrome 155 stable; the letters are the checklist's steps.

| # | Check | Automation (Chromium 156, headless) | By hand (Chrome 155 stable) |
|---|---|---|---|
| 1 | Typing `go/` and Enter reaches the stub | Pass, proxy via chrome://omnibox | **Pass** (B): the stub's home page |
| 2 | `go/foo` before `go/` was ever typed | Would search, proxy via chrome://omnibox | **Searched** (A), as expected |
| 3 | After one typed `go/`, `go/foo` navigates | Pass, proxy via chrome://omnibox | **Pass** (C) |
| 4 | Clicked link on a third-party HTTPS page, host access to `go` and the Directory only | Pass, automated. Forms on an HTTPS page did not arrive | **Pass** (E, E2): arrived from the Directory's own page and from a public HTTPS page |
| 5 | The same from a bookmark and from another app | Not attempted | **Pass** (F1, F2): bookmark; a link passed on Chrome's command line to the running test Chrome. With Chrome closed (L1): reported as arrived, not detected by the stub. The plan's `open http://go/foo` through the default browser was not tried |
| 6 | `https://go/foo`; `http://go/foo` with "Always use secure connections" on | Pass, proxy (setting written to the Preferences file) | **Pass** (G1, G3, G4): no warning at either level. The setting is off in a fresh profile (G2) |
| 7 | `go` does not resolve; `go` resolves elsewhere | Pass, proxy (resolver switch, not a hosts entry) | No manual step |
| 8 | Query reaches the stub; fragment survives | Pass, proxy (`page.goto`, not typed) | **Pass** (H1, H2) |
| 9 | `go/a%20b%2Fc` arrives byte for byte | Pass, proxy (`page.goto`, not typed) | **Pass** (H3): the stub received `/m9/a%20b%2Fc` |
| 10 | Autocomplete offers go links used before | Weak pass, proxy: last row, no inline completion | **Pass** (D): `go/m3` was offered. Its position was not recorded |
| 11 | Clicked link reaches the loopback Directory with no Local Network Access prompt or block | Pass, proxy (headless cannot show a prompt) | **Pass** (E2): arrived, no prompt |
| 12 | `onErrorOccurred` fires when the stub is stopped; the tab is replaced | Pass, automated. Back landed on the unreachable page again | **Pass** (I, I3): unreachable page shown; Retry worked after restart. Back went to the previous page (I2) |
| 13 | `onBeforeRequest` on `*://go/*` sees typed go navigations | Pass, proxy (programmatic and clicked only) | **Pass** (C2): both typed navigations listed, as `http://go/…` |
| 14 | Redirect works with the service worker stopped, and after a restart | Pass, automated. `onStartup` untested | **Pass** (L2, L3): after a restart `onStartup` fired and the rule was present, and a link then worked with the worker inactive. L1 (a go link as the first navigation after start): reported as arrived, not detected by the stub |
| 15 | Keyword `go/` leaves a plain `go/foo` unchanged | Pass, proxy via chrome://omnibox | **Pass** (K) |
| 16 | Incognito, extension not allowed there | Not attempted | **Recorded** (J): Chrome's error page |
| 17 | The extension can fetch the Directory's `meta` endpoint (added during L0) | Pass, proxy | **Pass** (C3): page and service worker both got 200, no prompt |

In the automated run, checks 4, 6, 7, 8, 9, and 14 each include a control without the extension, and the control had to miss the stub.

## Evidence from the automated run

Everything in this section comes from headless Chromium 156. Where the run by hand on Chrome stable adds something, the paragraph says so.

Relevance scores and match types below are Chromium's own, as exported by `chrome://omnibox`. The full export is in `docs/spike-results/omnibox-raw.json`.

### Checks 1 to 3: address-bar classification

| Input | Fresh profile | After one typed visit to `http://go/` |
|---|---|---|
| `go/` | Navigate to `http://go/` (HistoryURL 1203) | Navigate |
| `go/foo` | Search (input type "unknown", Search 1300) | Navigate to `http://go/foo` (HistoryURL 1403) |
| `go/foo/` | Navigate (1203) | Navigate |
| `http://go/foo` | Navigate (1203) | Navigate |
| `go` | Search (851) | Navigate to `http://go/` (HistoryURL 1413) |

Other states recorded for `go/foo`:

- **First visit was a clicked link, never typed:** Chromium would navigate. History recorded that click with the transition `TYPED|CHAIN_START`.
- **Typed credit removed** by editing the history file (a synthetic "visited, not typed" state): Chromium would search.
- **Typed visit while the Directory was down:** the tab ended on the extension's unreachable page, history held no `http://go/` row afterwards, and Chromium would still search.
- **"Always use secure connections" at its strict level:** after one visit to `http://go/`, history holds `http://go/` with typed count 0 and `https://go/` with typed count 1. The default match for `go/foo` is a navigation to `https://go/foo`.

### Check 4: clicked links, forms, and other ways in

The extension held host permissions for `*://go/*` and `http://localhost:4655/*` only. The judged case is one real mouse click on a link injected into `https://example.com/`. The stub logged it with `Sec-Fetch-Site: cross-site`, `Sec-Fetch-Mode: navigate`, `Sec-Fetch-User: ?1`, and an empty `Referer`.

Twelve other shapes were recorded and not judged. Each had a control that missed.

| From | Shape | Arrived at the stub |
|---|---|---|
| HTTPS page | Link with `target=_blank`; `window.open`; `location.href`; link in an iframe with `target=_top` | Yes, all four |
| HTTPS page | Form, `GET` and `POST` | **No.** The tab stopped at Chrome's warning "The information you're about to submit is not secure", with the buttons "Go back" and "Send anyway" |
| Plain-HTTP page | Link | Yes, with that page's origin (`http://thirdparty.example/`) as `Referer` |
| Plain-HTTP page | Form `GET` | Yes, as `/hit/…?q=a+b` |
| Plain-HTTP page | Form `POST` | Yes, **as a `POST`**, with `Origin` set to the other site |
| The Directory's own page | Link | Yes, with `Sec-Fetch-Site: cross-site` |
| Another server | `302` to a go link | Yes, with `Sec-Fetch-Site: none` |
| Plain-HTTP page | Meta refresh | Yes, with no `Sec-Fetch-User` |

The control without the extension stopped at the same warning, so the warning comes from Chrome and not from the redirect. "Send anyway" was not clicked, so what happens after it is unknown.

### Check 6: HTTPS and HTTPS-First mode

The harness left Chromium's `HttpsUpgrades` feature on, as Chrome has it; Playwright normally switches it off. Each mode was confirmed to be on by a probe: a public-looking HTTP-only site that had to be blocked.

| Setting | Requests for `http://go/…` | What `onBeforeRequest` saw | Control without the extension |
|---|---|---|---|
| Defaults | `http://go/…` then `http://localhost:4655/…` | `http://go/…` | Not run for this row |
| Strict level (`https_only_mode_enabled`) | `http://go/…`, `https://go/…`, then `http://localhost:4655/…` | `https://go/…` only | `ERR_BLOCKED_BY_CLIENT` |
| Public-sites level (`https_first_balanced_mode_enabled`) | `http://go/…` then `http://localhost:4655/…` | `http://go/…` | `ERR_NAME_NOT_RESOLVED` |
| Defaults, address given as `https://go/…` | `https://go/…` then `http://localhost:4655/…` | Not recorded in this check | `ERR_NAME_NOT_RESOLVED` |

So in this build `http://go/` is upgraded to HTTPS only at the strict level. With default settings the probe site was silently upgraded and fell back to HTTP, while `http://go/` was not upgraded at all.

One limit: the setting was written into the profile's Preferences file, not chosen on the settings page. By hand, a fresh profile of Chrome 155 stable had the setting off (step G2), and `go` links reached the stub with no warning at both levels (steps G3, G4); whether Chrome changed the address to `https://go/` at the strict level was not recorded by hand.

### Check 7: `go` on the network

`--host-resolver-rules=MAP go 127.0.0.1:4656` stood in for a hosts entry, because a real one needs administrator rights. With the extension the Directory stub logged the request and the decoy on port 4656 logged none. Without the extension the decoy received it. This shows the rule acts before name resolution in Chromium; it does not exercise the macOS resolver or a real `/etc/hosts`.

### Check 9: what arrives for other paths and hosts

Thirty variants were recorded and not judged. They went through `page.goto`, so they show Chromium's URL handling, not what the address bar does with typed text.

| Asked | Stub received |
|---|---|
| `go/a/../b`, `go/a/%2E%2E/b` | `/b` |
| `go/a/./b`, `go/a/%2e/b` | `/a/b` |
| `go/%2e%2e/x` | `/x` |
| `go//double` | `//double` (unchanged) |
| `go/a/%2E%2E%2Fb`, `go/a/..%2Fb` | Unchanged |
| `go/a%2fb%2Fc` | Unchanged: both hex cases arrive as sent |
| `go/CamelCase/UPPER` | Unchanged |
| `go/a b?q=c d` | `/a%20b?q=c%20d` |
| `go/a+b?q=c+d` | Unchanged |
| `go/a?x=%2F&y=a%26b` | Unchanged |
| `go/a\|"<>b` | `/a%7C%22%3C%3Eb` (percent-encoded) |
| A trailing space | Trimmed |
| A tab inside the path | Removed (`/ab`) |
| `go/a\b` | `/a/b` |
| `go/zażółć/日本` | UTF-8, percent-encoded |
| `%00`, `%0A`, `%2520`, `%3F%23` | Unchanged |
| A 4,001-character path | Whole |
| `go/a%zzb`, `go/%` | Redirected, then answered `400` by Go's HTTP server; the stub's handler never ran |

Host spellings: `GO/` and `go:80/` were redirected. `go./`, `go:8080/`, and `user@go/` were not (`ERR_NAME_NOT_RESOLVED`).

### Check 10: autocomplete

After a typed visit to `http://go/` and then one to `http://go/hit/x`, typing `go/h` lists that link as row 8 of 8 (HistoryURL 1390), below the typed text and the search engine's suggestions. `go/hit` gives row 8 of 8 and `go/` row 7 of 7. The default match stays `http://go/h` with no inline completion, so Enter does not open the used link. Suggestions that Chrome learns from address-bar picks could not be produced here. By hand on Chrome stable, typing `go/m` after using `go/m3` did offer `go/m3` (step D); which row it was in was not recorded.

### Check 11: Local Network Access

A click on `https://example.com/` to `http://go/hit/c11-click` ended on the stub with no failed request and an empty console. From the same page, `fetch("http://localhost:4655/…")` was rejected with "Permission was denied for this request to access the `loopback` address space", and the permission state read "prompt". So this build enforces the rule for sub-resources and did not apply it to the navigation. The result was the same with Chromium's field-trial testing configuration switched on.

"No prompt" is an inference: a navigation that needed the permission could not have been granted it in headless mode, so it would not have arrived. Chrome stable applies server-side field trials that were off here, and it is a different version. By hand on Chrome 155 stable, a go link clicked on a public HTTPS page arrived with no prompt (step E2).

### Check 12: the unreachable page

With the stub stopped, a go link produced `net::ERR_CONNECTION_REFUSED` in `onErrorOccurred`, and `chrome.tabs.update` replaced the tab with `unreachable.html`. No `tabs` permission was used. The same held for the Directory's address opened directly, and for a copy of the extension whose only host access is `http://localhost:4655/*`.

- **Tab state at the moment of the error:** `chrome.tabs.get` returned `url` null and `pendingUrl` null. A guard of the form "does the tab still show the failed URL" cannot be decided inside the handler. The spike skips `net::ERR_ABORTED` and non-active documents instead.
- **Retry while still down:** exactly one more error, and the tab was on the unreachable page again. No loop.
- **Back button:** the tab went back to the failed address, which failed and was replaced again. One more error each time. By hand on Chrome stable this differed: Back returned to the page the tab showed before (step I2).
- **Retry after restart:** reached the stub.

### Check 13: what the extension sees

`goSeen` held `http://go/hit/seen`, `https://go/hit/seen-https`, and `http://go/hit/seen-click`; the click came from a page the extension has no access to. At the strict HTTPS-First level the listener sees only the `https://go/` URL (check 6). Typed navigations could not be automated; by hand on Chrome stable the listener recorded both typed ones, `http://go/` and `http://go/m3` (step C2).

### Check 14: service worker and restart

- **Worker stopped** through the DevTools protocol (status "stopped", no worker target): `http://go/` redirected. The navigation woke the worker, because it has `webRequest` listeners.
- **Rule-only extension** with no `webRequest` listeners: redirected while its worker stayed stopped.
- **Relaunch on the same profile:** the first navigation, 856 ms after launch, redirected. Both extensions installed the rule once across two sessions and found it present at the second.
- **Go link as the browser's first navigation** (Chromium started with the URL on its command line, on a profile where the extension had already run in an earlier session): redirected. Without the extension the stub saw nothing in about 5 seconds.

Limits: `--load-extension` fires `onInstalled` with reason `install` on every launch and never `onStartup`, so automation cannot show the start-up of a normally loaded extension. By hand on Chrome stable, after Chrome was quit and started again the extension's log showed `onStartup` with the rule present (step L2), and a typed link then reached the stub after the worker had been left to go inactive (step L3; the request was detected, the worker's state is the owner's reading of `chrome://extensions`). For the go link opened with Chrome closed (step L1) the owner chose "Navigated to the stub", but the checklist page detected no request for `/m14`, unlike every other step that reached the stub. That case is reported, not corroborated, and is worth repeating. A tab restored from a previous session was not tried.

### Check 15: the keyword `go/`

Compared `spike/extension` with `spike/extension-omnibox`, fresh and after a typed visit, for `go/foo`, `go/`, `go/foo/`, `http://go/foo`, and `go`: the default match is identical in every case. In a fresh profile the keyword changes `go/ foo` (with a space) from a search into the extension's keyword match (Keyword 1100). After a typed visit to `http://go/` the default for `go/ foo` is the same with and without the keyword (a navigation to `http://go/%20foo`), and the keyword match is the second row. The keyword attaches a keyword hint to the default match for `go/` in both states, and after the typed visit also for `go` and `go/ foo`. By hand on Chrome stable, with the keyword variant loaded, a typed `go/m15` still navigated to the stub (step K). Whether Space or Tab enters keyword mode in the real address bar is untested.

### Three extra checks

These are in `automated.json` beyond the 16. All pass.

| Id | What it shows |
|---|---|
| `extra-extension-fetch` | The extension's page and its service worker both got HTTP 200 from `http://localhost:4655/-/api/v1/meta`. Proxy: a fetch that needed a prompt would have failed in headless. This is the connection test of L4.2, now check 17 of Appendix B. By hand on Chrome stable both fetches succeeded with no prompt (step C3). |
| `extra-broad-filter` | An `onErrorOccurred` listener registered with the filter `http://*/*`, `https://*/*` received the event only for the one host the extension had access to. |
| `extra-subresource` | An iframe and a `fetch()` aimed at `http://go/` were not redirected. The rule lists `main_frame` only, as designed. |

## Consequences for the plan

No check failed, in automation or by hand, so no "if it fails" action of Appendix B is triggered. The results below were not expected by the plan. The edits they call for have been made in the plan (sections 2.1, 3.3, 3.7, 3.8, tasks L4.3, L4.5, L5.3, Appendix B) and in PRD Appendix B.

| Finding | Check | Consequence |
|---|---|---|
| In headless Chromium the used link was offered only in the last row, with no inline completion. By hand on Chrome stable it was offered | 10 | The documentation may say that Chrome can offer a go link used before: by hand, one link typed about two minutes earlier was offered, and its row was not recorded. It should not promise inline completion or a position in the list. Nothing to build. |
| Once the host is learned, a bare `go` plus Enter opens `http://go/` instead of searching | 3 | A side effect of the first-run step, by proxy evidence only: the manual run had no step for a bare `go`. Worth a line on the setup page once it has been checked by hand. |
| A visit while the Directory is down does not teach Chrome the host | 3 | The first-run step of L4.3 must wait until the Directory answers before asking the user to type `go/`. |
| At the strict HTTPS-First level the address becomes `https://go/` before the extension sees it, and the host is learned as `https://go/` | 3, 6, 13 | The rule must keep matching both schemes, as it does. The first-run page must accept `https://go/` as the navigation it waits for. PRD Appendix B item 3 needs rewording: `go` was upgraded only at the strict level. |
| A form on an HTTPS page that submits to `http://go/` stops at Chrome's insecure-form warning | 4 | A limitation to document. No fallback in Appendix B covers it. |
| A cross-site form `POST` to `http://go/` reaches the Directory as a `POST` with the other site's `Origin`. A link on the Directory's own page arrives as `Sec-Fetch-Site: cross-site` | 4 | The write guards of section 3.8 must not be applied to the resolver path, and the resolver must refuse `POST` (section 3.3 already limits it to `GET` and `HEAD`). |
| Invalid percent-escapes get Go's bare `400` before any handler | 9 | The "short page" of section 3.3 cannot be shown for them. The status code already matches the plan. |
| `%2f` and `%2F` both arrive as sent; dot segments are resolved by Chromium before sending | 9 | L2.5 stands. Its decoding must treat both hex cases alike, which percent-decoding does. |
| `chrome.tabs.get` shows neither the failed URL nor a pending URL when the error fires | 12 | L5.3 cannot guard on the tab's URL inside the handler. |
| Back from the unreachable page: headless Chromium landed on the unreachable page again; Chrome stable returned to the page the tab showed before | 12 | In the one case tried by hand (a go link typed in a new tab), Chrome stable returned to the page before. L5.3 should test Back, also after a clicked link, and keep that behavior. |
| `--load-extension` fires `onInstalled` on every launch and never `onStartup`. An extension loaded unpacked through `chrome://extensions` on Chrome stable did fire `onStartup`, with the rule present | 14 | In Playwright the first-run page of L4.3 would open in every session. Start-up behavior stays on the manual list. |
| Typed navigations could not be automated. By hand, `onBeforeRequest` saw both typed go navigations | 13 | The first-run page of L4.3 can confirm the `go/` step by itself. |
| In Incognito, with the extension not allowed there, a go link ends on Chrome's error page | 16 | Say so on the setup page, with how to allow the extension in Incognito. |
| "Always use secure connections" is off in a fresh Chrome 155 profile | 6 | The strict-level behavior (`https://go/`) is an opt-in case, not the default. |

Check 11 was the result most likely to differ on Chrome stable. It did not: the clicked link on a public HTTPS page arrived with no prompt (step E2). Local Network Access rules vary by Chrome version and field trial, so this check belongs on the manual list for each release.

### Section 2 decisions (L0.4)

| Decision | State |
|---|---|
| Stack | The owner confirmed Go on 2026-10-13, after comparing it with Node.js. |
| Name matching (Q5) | The owner decided on 2026-10-13: names ignore case only; `-` and `_` are significant. |
| Working name `godir` | Used throughout the spike. Since decided for the extension, which is called Ogre; `godir` remains the working name of the server (Q8). |
| Port 4655 | Used by every run; free on the development machine each time. |
| Address-bar keyword `go/` for L6.8 | Confirmed: check 15 passes by proxy, and by hand for one typed link in a profile that already knew the host (step K). Keyword mode itself (Tab or Space after `go/`) was not tried by hand. |

## The manual run

Done on 2026-10-13 by the owner, in Chrome 155.0.8059.40 started with a throwaway profile and `spike/extension` loaded unpacked. All 25 steps of the checklist were answered, including the optional ones. To repeat it:

1. Run `spike/manual.sh`. It builds and starts the stub on port 4655 and prints the steps below with full paths.
2. Start Chrome with a throwaway profile, using the command the script prints. Your normal profile is not touched.
3. On `chrome://extensions`, switch on "Developer mode", click "Load unpacked", and choose `spike/extension`. If the button does nothing, drag the folder onto the page instead.
4. Open `http://localhost:4655/-/spike/manual` and follow its steps in order. Until the checklist says so, type nothing that starts with `go/`: the first steps need a profile that has never seen it.
5. Click "Save results". This writes `spike/results/manual.json`.
6. Quit the test Chrome, remove the throwaway profile, and stop the stub with Ctrl+C.

| Check | Manual steps |
|---|---|
| 1 | B |
| 2 | A |
| 3 | C |
| 4 | E (link on the Directory's own page), E2 (public HTTPS page) |
| 5 | F1 (bookmark), F2 (hand-off to the running test Chrome), L1 (hand-off with Chrome closed) |
| 6 | G1 to G4 |
| 7 | None |
| 8 | H1, H2 |
| 9 | H3 |
| 10 | D |
| 11 | E2 |
| 12 | I, I2, I3 |
| 13 | C2 |
| 14 | L1, L2, L3 |
| 15 | K |
| 16 | J |
| 17 | C3 |

**Limits.** The manual results are one run, on one machine, on one Chrome version, recorded as the outcome chosen for each step. The proxy evidence shows what Chromium 156's code decides in a headless browser with field trials off. Where both exist and agree, the result is as solid as this spike can make it: that is checks 1 to 4, 6, 8, 9, 11 to 15, and 17, apart from the Back button of check 12. Checks 5 and 16 have a manual result only, check 7 an automated result only, and for check 10 the manual run did not record the row, so it neither confirms nor contradicts the last-row finding. Windows, Linux, and ChromeOS were not tried; the plan leaves them to C2.7.

## How to rerun the automated checks

Ports 4655 and 4656 must be free, and `https://example.com` must be reachable (checks 4 and 11 record "inconclusive" without it).

```sh
cd spike/harness
npm install
npx playwright install chromium
npx playwright test                    # 19 tests, about two minutes
npx playwright test -g "check 12:"     # one check
```

The harness is not in the current tree; check out the tag first with `git checkout l0-spike`. Results are merged by check id into `spike/results/automated.json` (the copies published in this repository are in `docs/spike-results/`); delete that file first for a clean record. Each check carries its own time and Chromium version. The command line stored in the file shows a temporary profile path from the session that made the run.

After changing `spike/extension/`, regenerate the keyword variant with `node spike/sync-variant.mjs`. The stub is rebuilt by the harness; to build it by hand, run `go build -o bin/stub stub/main.go` in `spike/`.

The harness only ever starts Playwright's bundled Chromium, headless, on temporary profiles. One helper (`startChromiumWithUrl` in `spike/harness/lib/env.mjs`) starts that Chromium binary directly, because Playwright refuses a URL argument at launch. Online runs send the test inputs, such as `go/foo`, to Google's suggestion service, as Chrome does.
