# godir: build, run and check.
#
#   make run      start the Directory from source (ARGS="--db /tmp/x.db" passes flags)
#   make build    compile bin/godir
#   make test     Go tests, with the race detector
#   make fuzz     every fuzz target, FUZZTIME each (default 10s)
#   make e2e      end-to-end tests in headless Chromium with the extension
#   make check    everything; the gate before every commit
#
# Only cmd/ and internal/ hold Go code.

GO_PACKAGES := ./cmd/... ./internal/...
GO_DIRS     := cmd internal
VERSION     ?= 0.1.0-dev
FUZZTIME    ?= 10s
FUZZMINIMIZETIME ?= 1s
# Pinned, so the gate is the same for the same commit; raise it on purpose.
STATICCHECK_VERSION ?= 2026.2.2
STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

# The plan (section 3.6) bans these in browser code: untrusted text goes in
# with textContent or DOM methods.
BANNED_DOM  := innerHTML|insertAdjacentHTML
BROWSER_DIRS := internal/web/static extension

.PHONY: run build test fuzz e2e check clean \
        fmt-check vet staticcheck typecheck test-extension dom-guard

run:
	go run ./cmd/godir $(ARGS)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/godir ./cmd/godir

test:
	go test -race -count=1 $(GO_PACKAGES)

# Each fuzz target runs alone, because "go test -fuzz" accepts one target and
# one package at a time. The targets are found by name, so a new one needs no
# change here.
#
# -fuzzminimizetime: by default Go spends up to 60s shrinking every input that
# reaches new code, and runs nothing else meanwhile. In a 10s run that left
# two of the three targets idle after the first 3s ("0/sec"); with 1s they
# run about ten to fifty times as many inputs. A failing input is still
# shrunk, for up to 1s.
#
# The retry: now and then "go test -fuzz" reports the end of its own time
# limit as a failure ("context deadline exceeded" and nothing else), a race
# in the fuzzing engine at the moment the time is up. It happened once in
# about thirty runs here. That exact outcome, with no failing input found, is
# said out loud and the target is run again, twice at most. A real failure
# always writes a failing input and is never retried.
fuzz:
	@set -e; \
	files=$$(grep -rlE '^func Fuzz' --include='*_test.go' $(GO_DIRS) || true); \
	if [ -z "$$files" ]; then echo "fuzz: no fuzz targets found"; exit 1; fi; \
	for file in $$files; do \
	  pkg=./$$(dirname $$file); \
	  for target in $$(sed -nE 's/^func (Fuzz[A-Za-z0-9_]*)\(.*/\1/p' $$file); do \
	    attempt=1; \
	    while :; do \
	      echo "== fuzz $$pkg $$target for $(FUZZTIME)"; \
	      log=$$(mktemp); status=0; \
	      go test -run='^$$' -fuzz="^$$target\$$" -fuzztime=$(FUZZTIME) -fuzzminimizetime=$(FUZZMINIMIZETIME) $$pkg >$$log 2>&1 || status=$$?; \
	      cat $$log; \
	      if [ $$status -eq 0 ]; then rm -f $$log; break; fi; \
	      if [ $$attempt -lt 3 ] && grep -qE '^ +context deadline exceeded$$' $$log && ! grep -q 'Failing input written to' $$log; then \
	        rm -f $$log; attempt=$$((attempt + 1)); \
	        echo "*** fuzz: $$target found no failing input; go test reported the end of its own time limit as a failure. Running it again (attempt $$attempt of 3). ***"; \
	        continue; \
	      fi; \
	      rm -f $$log; exit 1; \
	    done; \
	  done; \
	done

e2e:
	npm run e2e

fmt-check:
	@unformatted=$$(gofmt -l $(GO_DIRS)); \
	if [ -n "$$unformatted" ]; then \
	  echo "gofmt: these files need formatting (run: gofmt -w $(GO_DIRS)):"; echo "$$unformatted"; exit 1; \
	fi; \
	echo "gofmt: ok"

vet:
	go vet $(GO_PACKAGES)

# staticcheck is fetched on demand rather than added to go.mod. It is part of
# the gate: when it cannot be fetched or run (no network, or a release that
# does not support this Go version yet) the step fails. To get through such a
# moment knowingly, run "make check STATICCHECK_OPTIONAL=1": the step is then
# skipped, out loud. When it runs, its findings always fail the build.
staticcheck:
	@if $(STATICCHECK) -version >/dev/null 2>&1; then \
	  echo "$(STATICCHECK) $(GO_PACKAGES)"; \
	  $(STATICCHECK) $(GO_PACKAGES) && echo "staticcheck: ok"; \
	else \
	  echo "*** staticcheck: could not fetch or run it; the reason follows ***"; \
	  $(STATICCHECK) -version 2>&1 | tail -n 5 || true; \
	  if [ "$(STATICCHECK_OPTIONAL)" = "1" ]; then \
	    echo "*** staticcheck: SKIPPED because STATICCHECK_OPTIONAL=1 ***"; \
	  else \
	    echo "staticcheck: failed; it is part of the gate (STATICCHECK_OPTIONAL=1 skips it)"; exit 1; \
	  fi; \
	fi

typecheck:
	npm run typecheck

test-extension:
	npm run test:extension

# grep exits 0 on a match, 1 on none and 2 on an error such as a missing
# directory. Only "1" may pass: a guard that searched nothing has not shown
# that nothing is there.
dom-guard:
	@for dir in $(BROWSER_DIRS); do \
	  if [ ! -d "$$dir" ]; then echo "dom-guard: $$dir is missing, so it cannot be checked"; exit 1; fi; \
	done; \
	grep -rnE '$(BANNED_DOM)' $(BROWSER_DIRS); status=$$?; \
	if [ $$status -eq 0 ]; then \
	  echo "dom-guard: innerHTML and insertAdjacentHTML are banned in browser code (plan section 3.6); use textContent or DOM methods"; exit 1; \
	elif [ $$status -ne 1 ]; then \
	  echo "dom-guard: grep failed (exit status $$status), so nothing was checked"; exit 1; \
	fi; \
	echo "dom-guard: ok"

check: fmt-check vet staticcheck test fuzz typecheck test-extension dom-guard e2e
	@echo "make check: all passed"

clean:
	rm -rf bin e2e/test-results e2e/playwright-report

# The steps of check run in the order listed, also under "make -j".
.NOTPARALLEL:
