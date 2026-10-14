# Ogre: build, run and check.
#
#   make run      start the Directory from source (ARGS="--db /tmp/x.db" passes flags)
#   make build    compile bin/ogre
#   make test     Go tests, with the race detector
#   make spec-pending  list the PRD fixture rows that wait for a later milestone
#   make fuzz     every fuzz target, FUZZTIME each (default 10s)
#   make bench    the benchmarks at 100,000 links (BENCH=. runs every benchmark)
#   make perf     the performance run over HTTP at 100,000 links (docs/perf.md)
#   make e2e      end-to-end tests in headless Chromium with the extension
#   make e2e-browsers  the web UI's tests in headless Firefox and WebKit
#   make check    everything; the gate before every commit
#
# The end-to-end tests run their own server at localhost:4655, the
# extension's default. With your own Ogre server there, run them on another
# port instead: OGRE_E2E_PORT=46555 make check (e2e/lib/harness.mjs).
#
# Two steps can be skipped, each out loud, when their tools cannot be had:
#   make check E2E_BROWSERS_OPTIONAL=1   Firefox and WebKit not installed
#   make check STATICCHECK_OPTIONAL=1    staticcheck cannot be fetched (offline)
#
# Only cmd/ and internal/ hold Go code.

GO_PACKAGES := ./cmd/... ./internal/...
GO_DIRS     := cmd internal
VERSION     ?= 0.2.0-dev
FUZZTIME    ?= 10s
FUZZMINIMIZETIME ?= 1s
# Which benchmarks "make bench" runs: a regular expression on their names.
# The default is the ones the plan sets targets for (L2: a hit under 1 ms and
# an unknown-name miss under 50 ms, in process, at 100,000 links), and the
# costliest request a saved link allows (NFR-8).
BENCH       ?= 100k|Worst
BENCHTIME   ?= 1s
# The spec tests, and the fixture rows they are expected to skip: row 18
# (query passthrough, RES-8) at each of the three levels it is replayed at.
SPEC_PACKAGES := ./internal/linkmodel ./internal/app ./cmd/ogre
SPEC_PENDING  := 'TestSpecRequests/18_go/gh/api-server?tab=readme' \
                 'TestSpecRequests/18_go/gh/api-server?tab=readme' \
                 'TestSpecRequestsThroughHTTP/18_go/gh/api-server?tab=readme'
# Pinned, so the gate is the same for the same commit; raise it on purpose.
STATICCHECK_VERSION ?= 2026.2.2
STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

# The plan (section 3.6) bans these in browser code: untrusted text goes in
# with textContent or DOM methods. Every DOM sink that parses a string as
# markup is listed, not only the two common ones; the pages' content security
# policy (Trusted Types) refuses the same sinks at run time in Chromium.
BANNED_DOM  := innerHTML|outerHTML|insertAdjacentHTML|document\.write|srcdoc|DOMParser|parseFromString|createContextualFragment|setHTMLUnsafe|parseHTMLUnsafe
BROWSER_DIRS := internal/web/static extension

.PHONY: run build test fuzz bench e2e e2e-browsers check clean \
        fmt-check vet staticcheck spec-pending typecheck test-extension test-web dom-guard

run:
	go run ./cmd/ogre $(ARGS)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/ogre ./cmd/ogre

test:
	go test -race -count=1 $(GO_PACKAGES)

# Rows of the PRD fixture (internal/linkmodel/testdata/spec.json) that wait
# for a later milestone are skipped by the spec tests, after their interim
# answer is checked. "go test" without -v says nothing about a skip, so a row
# could stay pending, or become pending, without anyone seeing it. This step
# prints every skipped row and fails unless they are exactly the ones listed
# in SPEC_PENDING: taking a row off the list is part of implementing it.
spec-pending:
	@out=$$(go test -count=1 -v -run 'Spec' $(SPEC_PACKAGES) 2>&1) || { echo "$$out" | tail -n 40; exit 1; }; \
	got=$$(echo "$$out" | sed -nE 's/^ *--- SKIP: ([^ ]+) .*/\1/p' | sort); \
	want=$$(printf '%s\n' $(SPEC_PENDING) | sort); \
	echo "spec rows pending a later milestone (skipped; their interim answer is checked):"; \
	if [ -n "$$got" ]; then echo "$$got" | sed 's/^/  /'; else echo "  none"; fi; \
	if [ "$$got" != "$$want" ]; then \
	  echo "spec-pending: the skipped rows differ from SPEC_PENDING in the Makefile; expected:"; \
	  printf '  %s\n' $(SPEC_PENDING); exit 1; \
	fi

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

# Benchmarks are found by name in every package, like the fuzz targets, so a
# new one that carries "100k" or "Worst" in its name needs no change here. They are not
# part of check: the numbers mean something only on a quiet machine. Every
# benchmark asserts its own outcome before the clock starts, so a run that
# prints numbers also resolved what it claims to have resolved.
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -benchtime=$(BENCHTIME) $(GO_PACKAGES)

# The performance run of plan L4.7 (NFR-1, NFR-2, NFR-3, NFR-5): 100,000
# links, the binary just built, measured over real HTTP; docs/perf.md has the
# method and the results. Not part of check, for the same reason as bench.
# PERF_ARGS passes flags: PERF_ARGS="-links 10000 -rate-for 10s" is a dry run.
.PHONY: perf
perf: build
	go run ./cmd/ogre-perf -bin bin/ogre $(PERF_ARGS)

e2e:
	npm run e2e

# NFR-20 beyond Chromium (plan L4.8): PRD scenarios 1 to 7 in the web UI, the
# accessibility scan and a phone's width, in Playwright's own Firefox and
# WebKit (e2e/browsers.spec.mjs). Those browsers are a separate download
# ("npx playwright install firefox webkit", needs the network), so this step
# first checks that they are there. Like staticcheck it is part of the gate:
# without them it fails and says how to get them, and
# "make check E2E_BROWSERS_OPTIONAL=1" skips it, out loud.
PLAYWRIGHT_BROWSERS_MISSING := node --input-type=module -e "import { firefox, webkit } from '@playwright/test'; import { existsSync } from 'node:fs'; const missing = [firefox, webkit].filter((b) => !existsSync(b.executablePath())).map((b) => b.name()); if (missing.length) { console.log(missing.join(' and ')); process.exit(1); }"

e2e-browsers:
	@if missing=$$($(PLAYWRIGHT_BROWSERS_MISSING) 2>&1); then \
	  npm run e2e:browsers; \
	else \
	  echo "*** e2e-browsers: Playwright's browsers are missing ($$missing); install them with: npx playwright install firefox webkit ***"; \
	  if [ "$(E2E_BROWSERS_OPTIONAL)" = "1" ]; then \
	    echo "*** e2e-browsers: SKIPPED because E2E_BROWSERS_OPTIONAL=1: Firefox and WebKit were NOT tested ***"; \
	  else \
	    echo "e2e-browsers: failed; it is part of the gate (E2E_BROWSERS_OPTIONAL=1 skips it)"; exit 1; \
	  fi; \
	fi

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

# Unit tests of the web UI's browser modules that need no browser
# (e2e/unit): the debounce every call made while typing relies on, and the
# URL check of lib/dom.js.
test-web:
	npm run test:web

# grep exits 0 on a match, 1 on none and 2 on an error such as a missing
# directory. Only "1" may pass: a guard that searched nothing has not shown
# that nothing is there.
dom-guard:
	@for dir in $(BROWSER_DIRS); do \
	  if [ ! -d "$$dir" ]; then echo "dom-guard: $$dir is missing, so it cannot be checked"; exit 1; fi; \
	done; \
	grep -rnE '$(BANNED_DOM)' $(BROWSER_DIRS); status=$$?; \
	if [ $$status -eq 0 ]; then \
	  echo "dom-guard: DOM sinks that parse markup ($(BANNED_DOM)) are banned in browser code (plan section 3.6); use textContent or DOM methods"; exit 1; \
	elif [ $$status -ne 1 ]; then \
	  echo "dom-guard: grep failed (exit status $$status), so nothing was checked"; exit 1; \
	fi; \
	echo "dom-guard: ok"

check: fmt-check vet staticcheck test spec-pending fuzz typecheck test-extension test-web dom-guard e2e e2e-browsers
	@echo "make check: all passed"

clean:
	rm -rf bin e2e/test-results e2e/playwright-report

# The steps of check run in the order listed, also under "make -j".
.NOTPARALLEL:
