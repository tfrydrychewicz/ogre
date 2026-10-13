#!/usr/bin/env bash
# Builds the spike stub and runs it in the foreground for the manual Chrome
# checks (docs/IMPLEMENTATION_PLAN.md, Appendix B). It prints what to do first.
# It never starts Chrome: you do that yourself, in a throwaway profile.
set -euo pipefail

# The real path, without symlinks: Chrome derives an unpacked extension's ID from it.
SPIKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PORT=4655
cd "$SPIKE_DIR"

command -v go >/dev/null || { echo "manual.sh: go is not on PATH" >&2; exit 1; }
go build -o bin/stub stub/main.go

if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "manual.sh: port $PORT is already in use. Stop whatever holds it first:" >&2
  lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >&2
  exit 1
fi

# Step K loads extension-omnibox, which must be a current copy of extension.
if command -v node >/dev/null; then
  node sync-variant.mjs >/dev/null
else
  echo "manual.sh: warning: node is not on PATH, so extension-omnibox was not regenerated and may be out of date" >&2
fi
for dir in extension extension-omnibox; do
  [ -f "$dir/manifest.json" ] || echo "manual.sh: warning: $SPIKE_DIR/$dir/manifest.json is missing" >&2
done

# Chrome's ID for an unpacked extension: the first 32 hex digits of the
# SHA-256 of its path, written with the letters a to p.
extension_id() {
  printf %s "$1" | shasum -a 256 | cut -c1-32 | tr '0-9a-f' 'a-p'
}
STATUS_URL="chrome-extension://$(extension_id "$SPIKE_DIR/extension")/status.html"
RESTART_COMMAND="$SPIKE_DIR/manual.sh"

cat <<EOF

godir spike: manual Chrome checks
=================================
Restarting the stub for step I3? Nothing below needs repeating; go back to the checklist.

This window stays busy running the stub. Use a second Terminal window for the commands.

 1. Start Chrome with a fresh, throwaway profile. Your normal profile is not touched:

      P="\$(mktemp -d)"; open -na "Google Chrome" --args --user-data-dir="\$P" --no-first-run --no-default-browser-check

    Keep that Terminal window open: later steps use \$P again.
    If Chrome asks you to choose a search engine, pick any; it does not matter here.
    A second Chrome icon appears in the Dock. That one is the test Chrome.

 2. In the new Chrome window, open:  chrome://extensions

 3. Switch on "Developer mode" (top right).

 4. Click "Load unpacked" and choose this folder
    (in the file dialog, Cmd+Shift+G lets you paste the path):

      $SPIKE_DIR/extension

 5. Open the checklist and follow it from top to bottom:

      http://localhost:$PORT/-/spike/manual

    Until the checklist tells you to, do not type anything starting with "go/"
    in that Chrome: the first steps need a profile that has never seen it.

Needed later in the checklist (it shows these too):
  status page:        $STATUS_URL
                      (if chrome://extensions shows another ID for "godir spike", use that ID)
  steps F2 and L1:    open -na "Google Chrome" --args --user-data-dir="\$P" "http://go/m5b"   (L1: .../m14, with Chrome quit first)
  restart the stub:   $RESTART_COMMAND
  step K:             $SPIKE_DIR/extension-omnibox

When you are done:
  1. "Save results" on the checklist. It writes:  $SPIKE_DIR/results/manual.json
  2. Quit the test Chrome (click its window, Cmd+Q).
  3. Remove the throwaway profile, in the Terminal where P is set:  rm -rf "\$P"
  4. Stop this stub with Ctrl+C.

Each request the stub resolves is printed below as one JSON line.

EOF

exec bin/stub -addr "localhost:$PORT" -results-dir "$SPIKE_DIR/results" \
  -status-url "$STATUS_URL" -restart-command "$RESTART_COMMAND"
