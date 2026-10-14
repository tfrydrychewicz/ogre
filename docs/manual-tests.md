# Manual tests

The checks no automated test can perform (plan section 4 and L4.8). Playwright cannot type into Chrome's address bar, cannot answer Chrome's permission prompts, and cannot load an extension into Chrome-branded builds; it also drives its own builds of Firefox and WebKit, not the Firefox and Safari people install. Everything else is in `make check`.

Run this list before each tag, on the current Chrome stable on macOS, and copy the filled-in tables into the tag's notes (or a commit on this file). Expect 25 to 35 minutes the first time: about 60 steps in three browsers, with two full Chrome restarts. That is an estimate; write down how long the run took, and replace it after the first real run. Write **pass**, **fail**, or what you saw in the Result column; a "Record" row has no right answer, only an observation to write down.

## What runs where

| Browser | How it is covered |
|---|---|
| Chrome stable, macOS | This list (sections A to G), by hand |
| Chromium | `make e2e`: the extension's redirect, PRD scenarios 1 to 7 through the extension, the accessibility scan in light and dark, 375 px width, every page without policy violations |
| Microsoft Edge | **Not run.** Edge is Chromium; the Chromium run stands in for it, and nothing here is known to differ. Treat Edge as covered by proxy only |
| Firefox | `make e2e-browsers` (Playwright's Firefox): scenarios 1 to 7 in the web UI, the accessibility scan, 375 px, the pattern highlighting. Section H: a short pass in the real Firefox |
| Safari | `make e2e-browsers` (Playwright's WebKit, a stand-in for Safari): the same as Firefox. Section H: a short pass in the real Safari |

Firefox and Safari use the Directory without the extension, which is Chrome's: a go link is opened at `http://localhost:4655/<name>`, or through a search shortcut (section G).

## Before you start (3 minutes)

| | |
|---|---|
| Date | |
| Commit (`git rev-parse --short HEAD`) | |
| Chrome version (`chrome://version`) | |
| macOS version | |
| Firefox version, Safari version | |
| Time the whole run took | |

0. Stop your own Ogre Directory if one runs: Ctrl+C in its terminal (from L5 on, `ogre service stop`). Then check that nothing listens on the default port: `lsof -nP -iTCP:4655 -sTCP:LISTEN` must print nothing. Otherwise step 1 fails ("another ogre … is already running there"), and the steps below would run against your real links. Start it again after [Cleanup](#cleanup).

1. In a terminal, from the repository, start a Directory on a scratch database (never your real one):

   ```sh
   mkdir -p /tmp/ogre-manual && go run ./cmd/ogre --db /tmp/ogre-manual/a.db
   ```

2. In a second terminal, add the two links this list uses:

   ```sh
   curl -s -X POST http://localhost:4655/-/api/v1/links -H 'Content-Type: application/json' \
     -d '{"description":"Product roadmap","routes":[{"pattern":"roadmap","destination":"https://example.com/roadmap"}]}'
   curl -s -X POST http://localhost:4655/-/api/v1/links -H 'Content-Type: application/json' \
     -d '{"description":"Daily report for one day","routes":[{"pattern":"reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}","destination":"https://example.com/daily?month={month}&day={day}"}]}'
   ```

3. In Chrome, make a fresh profile: the profile icon at the top right, **Add**, **Continue without an account**. A fresh profile matters: section A checks what Chrome does before it has ever seen `go/`. Do everything in Chrome in this profile, and delete it at the end.

## A. Loading the extension and the first run

Do these in order. Do not type `go/` anywhere until step A5 asks for it.

| # | Step | Expected | Result |
|---|---|---|---|
| A1 | Stop the Directory (Ctrl+C in its terminal). Open `chrome://extensions`, switch on **Developer mode**, **Load unpacked**, choose `extension/` | "Ogre" appears with no **Errors** button. The first-run page opens in a new tab by itself | |
| A2 | On `chrome://extensions`, **Details** under Ogre, **Site access** | Access to `go` and `localhost:4655` only; nothing like "all sites" | |
| A3 | Look at step 1 of the first-run page | It says "Not answering" and shows how to start the server (`go run ./cmd/ogre`, or `make run`). Step 2 waits for step 1 | |
| A4 | Start the Directory again (the command of "Before you start", step 1). Back on the first-run page, wait or click **Check again** | Step 1 turns done by itself; step 2 becomes active. No Chrome prompt about local network access appears | |
| A5 | Before step 2: in a new tab type `go/roadmap` and press Enter | **Record** whether Chrome searches (expected in a fresh profile, PRD risk R1) or opens the link | |
| A6 | Type `go/roadmap/` (trailing slash), Enter. Then `http://go/roadmap`, Enter | Both land on `https://example.com/roadmap` | |
| A7 | Do step 2 of the first-run page: new tab, type `go/`, Enter | The Directory's home page opens. Back on the first-run page, step 2 is marked done without you doing anything | |
| A8 | Step 3: new tab, type `go/roadmap` (no slash), Enter | Lands on `https://example.com/roadmap` | |
| A9 | Click **Finish** (or **Don't show this page again**). Quit Chrome completely (Cmd+Q), open it again in the same profile | The first-run page does **not** open again | |
| A10 | After the restart, type `go/roadmap` as the very first thing | Lands on `https://example.com/roadmap` (the redirect rule survived the restart; automated tests never see a real restart) | |

## B. Go links in the address bar, and from other places

B8 needs a go link on a public HTTPS page. It is added through the DevTools console, as in step E2 of the L0 spike ([spike-chrome.md](spike-chrome.md)), with this line:

```js
document.documentElement.innerHTML = '<a href="http://go/roadmap" style="font-size:3em">go/roadmap</a>'
```

| # | Step | Expected | Result |
|---|---|---|---|
| B1 | Type `go/`, Enter | The Directory's home page, `http://localhost:4655/-/` | |
| B2 | Type `go/reports/10/13`, Enter | `https://example.com/daily?month=10&day=13` | |
| B3 | Type `go/reprots/10/13`, Enter. Then press Enter again | A Directory page saying there is no `go/reprots/10/13`, with "Did you mean `go/reports/10/13`" focused; Enter lands on `…/daily?month=10&day=13` | |
| B4 | Type `go/reports/13/01`, Enter | The Directory's page for `go/reports`, saying `month` doesn't accept `13` and what it accepts | |
| B5 | Type `go/roadmap?x=1#top`, Enter | Lands on `https://example.com/roadmap#top` (a link without variables ignores the query; the fragment survives) | |
| B6 | Bookmark the page of B2's link: open `go/reports/10/13` from the bookmarks bar | Same as B2 | |
| B7 | Type `go/-/new?pattern=x&destination=https://example.com/`, Enter | The create form opens with pattern `x` and that destination filled in, and **no** "Warning: filled in by another site". (Typing gives `Sec-Fetch-Site: none`, as `e2e/fetch-metadata.spec.mjs` pins for Playwright's stand-in for typing.) Leave without saving | |
| B8 | Open `https://example.com` in a new tab. Open the DevTools console there (Cmd+Option+J), paste the line above, and press Enter (if Chrome refuses the paste, type `allow pasting`, Enter, and paste again). Click the large link the page now shows | Lands on `https://example.com/roadmap`. No Chrome prompt about devices on your local network, and no error page. The extension has no access to `example.com` (A2), so this is plan Appendix B checks 4 and 11. If Chrome asks or blocks, write down its exact words: that is the Local Network Access risk of plan section 9 | |
| B9 | Make Chrome the default browser (System Settings, **Desktop & Dock**, **Default web browser**), and click into a window of this profile, so it is the Chrome window used last. In Terminal run `open http://go/roadmap` | Chrome opens a tab in this profile that lands on `https://example.com/roadmap` (Appendix B check 5, the hand-off from another app; L0 never tried it). **Record** which profile the tab opened in. If you cannot make Chrome the default browser, write **not run** | |

## C. Custom Directory URL and the permission prompt

Start a second Directory in another terminal:

```sh
go run ./cmd/ogre --addr localhost:4656 --db /tmp/ogre-manual/b.db
```

| # | Step | Expected | Result |
|---|---|---|---|
| C1 | `chrome://extensions`, Ogre, **Details**, **Extension options** | The settings page, showing `http://localhost:4655` | |
| C2 | Enter `localhost:4656/x` and **Save** | Refused, with a message saying what is wrong; nothing changes | |
| C3 | Enter `http://localhost:4656`, **Test connection** | Chrome asks whether Ogre may access `localhost:4656` (one site). Click **Allow**: the page says an Ogre server answers and shows its version. On `chrome://extensions`, **Details**, **Site access** now lists `localhost:4656` besides `go` and `localhost:4655`, and nothing else | |
| C4 | **Save** | The page says go links now go to `http://localhost:4656`. Type `go/`, Enter: the home page of the empty second Directory (no links) at `localhost:4656` | |
| C5 | **Details**, **Site access** | `go`, `localhost:4655`, and `localhost:4656`; nothing broader | |
| C6 | On the settings page enter `http://localhost:4657` (nothing runs there), **Save** | Chrome first asks whether Ogre may access `localhost:4657`: click **Allow**. Then a warning that no server answers, with **Save anyway** and **Cancel**. Click **Cancel**: still `localhost:4656`. (Site access now lists `localhost:4657` too, until the next save or Chrome restart) | |
| C7 | Click **Use the default address**, then **Save** | Go links go to `localhost:4655` again (`go/roadmap` works). Site access lists neither `localhost:4656` nor `localhost:4657` | |
| C8 | Enter `http://127.0.0.1:4656`, **Save**, and when Chrome asks, click **Deny** | Not saved, with "Error: access not given"; go links still go to `localhost:4655` | |
| C9 | Enter `http://localhost:4656`, **Save**, **Allow**. Then on `chrome://extensions`, **Details**, **Site access**, remove `localhost:4656`. Reload the settings page | "Warning: access withdrawn", naming `localhost:4656`; `go/` still opens the second Directory. **Save** again, **Allow**: the warning is gone | |
| C10 | Click **Use the default address**, **Save**. Enter `http://localhost:4656`, **Test connection**, **Allow**, and do not save. Quit Chrome completely (Cmd+Q) and open it again | Site access lists `go` and `localhost:4655` only: access allowed for a test that was never saved is given up when Chrome starts (`onStartup`, which automated tests never see) | |

Stop the second Directory (Ctrl+C).

## D. Incognito

| # | Step | Expected | Result |
|---|---|---|---|
| D1 | Open an Incognito window (Cmd+Shift+N), type `go/roadmap/`, Enter | **Record.** In L0 this ended on Chrome's error page: the extension does not run in Incognito unless allowed | |
| D2 | `chrome://extensions`, Ogre, **Details**, switch on **Allow in Incognito**. In a new Incognito window type `go/roadmap/`, Enter | **Record.** If it lands on `https://example.com/roadmap`, remove "Not tested yet" from the setup page's Incognito section | |
| D3 | Switch **Allow in Incognito** off again | | |

## E. "Always use secure connections" (PRD Appendix B.3)

Setting: `chrome://settings/security`, **Always use secure connections**, with its two levels (insecure public sites only; all insecure sites, the strict level). It is off in a fresh profile.

| # | Step | Expected | Result |
|---|---|---|---|
| E1 | Setting off. Type `go/roadmap`, Enter | Lands on the destination, no warning | |
| E2 | Setting on, public-sites level. Type `go/roadmap`, Enter | Lands on the destination, no warning | |
| E3 | Setting on, strict level. Type `go/roadmap`, Enter | Lands on the destination, no warning. **Record** whether the address bar showed `https://go/roadmap` on the way (L0 left this open by hand) | |
| E4 | Still strict. Settings page of the extension, **Open the first-run page**; do its step 2 (`go/`, Enter) | Step 2 is marked done: the page accepts `https://go/` as well as `http://go/` | |
| E5 | Turn the setting off again | | |

## F. The setup page's search shortcut (open point from L3)

The setup page offers a search shortcut for browsers without the extension. Whether Chrome turns `/` into `%2F` in `%s` decides whether it works for links with variables.

| # | Step | Expected | Result |
|---|---|---|---|
| F1 | Open `http://localhost:4655/-/setup`, section 5. Add the shortcut exactly as it says (`chrome://settings/searchEngines`, **Site search**, **Add**) | Saved | |
| F2 | In the address bar type `go`, then Tab, then `roadmap`, Enter | Lands on `https://example.com/roadmap` | |
| F3 | Type `go`, Tab, `reports/10/13`, Enter | **Record the address the tab shows on the Directory's side** (the history entry, or the miss page's address). If it is `…/reports%2F10%2F13`, Chrome encodes the slash: the setup page must then say the shortcut works only for links without variables | |
| F4 | Type `go/roadmap` (no Tab) | Still opens the link through the extension: the shortcut does not take over plain go links | |
| F5 | Delete the shortcut | | |

## G. The background service after a reboot

Not applicable before L5: no milestone up to L4 installs a service (`ogre service install` is L5.1). From L5 on, this section gets a reboot test: install the service, restart the Mac, and type `go/roadmap` without opening a terminal.

## H. Firefox and Safari: PRD scenarios 1 to 7 in the web UI

Playwright's Firefox and WebKit run these in `make e2e-browsers`; this pass checks the real browsers. Use the Directory of "Before you start" in a private window of each browser. Go links are opened at `http://localhost:4655/<name>`.

| # | Step | Expected | Firefox | Safari |
|---|---|---|---|---|
| H1 | Scenario 1: open `http://localhost:4655/`, **New link**, pattern `wiki`, destination `https://example.com/wiki`, **Create link**; then open `http://localhost:4655/wiki` | "go/wiki is free" while typing; the link's page after saving; then `https://example.com/wiki` | | |
| H2 | Scenario 2: **New link**, pattern `daily/{month}/{day}`. Set both types to **Whole number** in the variables table. Type `10` and `13` into the preview | The type selectors open and work; the pattern becomes `daily/{month:int}/{day:int}`; the colored highlighting sits exactly over the typed text, also after scrolling a long pattern; the preview shows the final URL as you type. Leave without saving | | |
| H3 | Scenario 3: open `http://localhost:4655/reports/10/13` | `https://example.com/daily?month=10&day=13` | | |
| H4 | Scenario 4: open `http://localhost:4655/reprots/10/13`, press Enter | "Did you mean" focused; Enter lands as in H3 | | |
| H5 | Scenario 5: open `http://localhost:4655/reports/13/01` | Says `month` doesn't accept `13`, and what it accepts | | |
| H6 | Scenario 6: home page, type `daily report`, open `go/reports`, fill in `10` and `13`, **Open** | Search finds it; the launcher shows the final URL; Open lands as in H3 | | |
| H7 | Scenario 7: **Edit** `go/reports`, change the destination to `https://example.com/v2?m={month}&d={day}`, **Save changes**; open `http://localhost:4655/reports/1/2` | `https://example.com/v2?m=1&d=2` | | |
| H8 | Safari only: Develop menu (Settings, Advanced, "Show features for web developers"), **Show JavaScript Console**, then open `http://localhost:4655/-/new?pattern=a/{b}/{c}` | **Record** the console. WebKit reports "Refused to apply a stylesheet…" once per type selector on its own (see below); anything else is a failure | — | |

Then set H7's destination back if you keep using this database, or delete `/tmp/ogre-manual`.

### About the WebKit report in H8

Playwright's WebKit raises a content security policy `style-src-attr` report for every `<select>` it creates, even one in a static page with no script and no stylesheet: the engine refuses an inline style of its own, inside the select's built-in shadow tree. The select draws the same with and without the policy, and Chromium and Firefox raise nothing. The minimal repro is [e2e/webkit-select-csp.spec.mjs](../e2e/webkit-select-csp.spec.mjs); it fails the day WebKit stops doing it. The end-to-end watch lets exactly that report through in WebKit and nothing else ([e2e/lib/browsers.mjs](../e2e/lib/browsers.mjs)). H8 records whether the real Safari does the same.

## Cleanup

- Stop the Directory; `rm -rf /tmp/ogre-manual`.
- Start your own Directory again, the way you stopped it in step 0.
- Delete the Chrome profile made for this run.
