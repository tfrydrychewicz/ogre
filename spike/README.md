# Chrome spike (milestone L0)

Throwaway code that checks, in a real Chromium, the browser behaviors the go-links design depends on: the 17 checks of Appendix B in [docs/IMPLEMENTATION_PLAN.md](../docs/IMPLEMENTATION_PLAN.md). Nothing here is product code. **This directory is deleted when L1 starts.**

Results and what they mean for the plan: [docs/spike-chrome.md](../docs/spike-chrome.md).

## Layout

| Path | What it is |
|---|---|
| `stub/main.go`, `stub/manual.html` | Stand-in Directory on port 4655. Logs each request as it arrived and answers `302`, `200`, or `404`. Also serves the manual checklist. Go standard library only |
| `bin/stub` | The built stub (listed in `.gitignore`) |
| `extension/` | Manifest V3 extension with the redirect rule of plan section 3.7. Load it unpacked as it is |
| `extension-omnibox/` | Generated copy that also registers the address-bar keyword `go/`. Regenerate with `node sync-variant.mjs` |
| `harness/` | Playwright tests. `tests/checks.spec.mjs` and `tests/omnibox.spec.mjs` hold the checks |
| `results/` | `automated.json` and `omnibox-raw.json` from the harness; `manual.json` from the checklist |
| `manual.sh` | Starts the stub and prints the steps for the manual checks |

## Automated checks

Needs Go, Node.js, ports 4655 and 4656 free, and `https://example.com` reachable.

```sh
cd spike/harness
npm install
npx playwright install chromium
npx playwright test
```

This runs 19 tests in about two minutes and writes `results/automated.json`. It starts only Playwright's bundled Chromium, headless, on temporary profiles. It never starts your installed Chrome.

## Manual checks

These need a person and Chrome stable. They were run on 2026-10-13 in Chrome 155.0.8059.40; the answers are in `results/manual.json`. To repeat them:

```sh
spike/manual.sh
```

The script builds and starts the stub, then prints what to do: start Chrome with a throwaway profile, load `extension/` unpacked on `chrome://extensions`, and open `http://localhost:4655/-/spike/manual`. Follow the checklist in order and click "Save results"; it writes `results/manual.json`. Stop the stub with Ctrl+C when done.

## Limits

Automated results come from headless Chromium, not Chrome stable, and many use a stand-in for the real user action. [docs/spike-chrome.md](../docs/spike-chrome.md) says which.
