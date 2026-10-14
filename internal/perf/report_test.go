package perf

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// The verdicts are what L4.7 delivers, so these tests build phases and
// results by hand, with no server, and check that each way a run can go
// wrong turns into a FAIL or INCOMPLETE, and that a run at the limits passes.

func TestTargetsAreThePRDs(t *testing.T) {
	// PRD section 8: NFR-1, NFR-2, NFR-3, and NFR-5's 512 MB instance.
	if targetHit != 20*time.Millisecond || targetMiss != 200*time.Millisecond ||
		targetSearch != 150*time.Millisecond || targetRSS != 512<<20 {
		t.Fatalf("targets: hit %v, miss %v, search %v, RSS %d", targetHit, targetMiss, targetSearch, targetRSS)
	}
}

// times returns n copies of d.
func times(n int, d time.Duration) []time.Duration {
	return slices.Repeat([]time.Duration{d}, n)
}

func hitClass() *class {
	return &class{key: "hit", label: "Hit (302)", target: targetHit, nfr: "NFR-1"}
}

func TestPhaseOutcome(t *testing.T) {
	// 95 of 100 at the target and 5 above: the 95th percentile is the
	// 95th value, still at the target.
	atLimit := append(times(95, targetHit), times(5, targetHit+time.Nanosecond)...)
	overLimit := append(times(94, targetHit), times(6, targetHit+time.Nanosecond)...)
	for _, tc := range []struct {
		name string
		ph   phase
		want string
	}{
		{"all at the target", phase{class: hitClass(), server: times(100, targetHit)}, pass},
		{"p95 exactly at the target", phase{class: hitClass(), server: atLimit}, pass},
		{"6 of 100 over the target", phase{class: hitClass(), server: overLimit}, fail},
		{"an error", phase{class: hitClass(), server: times(100, time.Millisecond), errors: 1}, fail},
		{"a misfiled record", phase{class: hitClass(), server: times(100, time.Millisecond), misfiled: 1}, fail},
		{"a missing record", phase{class: hitClass(), server: times(99, time.Millisecond), missingRecords: 1}, incomplete},
		{"nothing measured", phase{class: hitClass()}, incomplete},
		{"no target", phase{class: &class{label: "Home page"}, server: times(10, time.Hour)}, "—"},
		{"an error and no target", phase{class: &class{label: "Home page"}, server: times(10, 1), errors: 1}, fail},
	} {
		if got := phaseOutcome(tc.ph); got != tc.want {
			t.Errorf("%s: phaseOutcome = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// passingResult is a run that meets every requirement, just: each figure at
// its limit.
func passingResult() *Result {
	classes := []*class{
		hitClass(),
		{key: "miss_known", label: "Miss page, known name", target: targetMiss, nfr: "NFR-2"},
		{key: "search_1", label: "Search API, one word", target: targetSearch, nfr: "NFR-3"},
		{key: "home", label: "Home page"},
	}
	r := &Result{Options: Options{Links: QualifyingLinks, GOMAXPROCS: 1}}
	for _, c := range classes {
		limit := c.target
		if limit == 0 {
			limit = time.Second
		}
		r.Phases = append(r.Phases, phase{class: c, requests: 100, server: times(100, limit), roundTrip: times(100, limit)})
	}
	r.Capacity = capacity{
		rate: 200, duration: 30 * time.Second, elapsed: 30 * time.Second, sent: 6000, achieved: 0.99 * 200,
		hitServer: times(80, targetHit), missServer: times(20, targetMiss),
		hitWait: times(80, targetHit), missWait: times(20, targetMiss),
		cpuShare: -1,
	}
	r.RSSStart, r.RSSPeak, r.RSSEnd = 100<<20, targetRSS, 100<<20
	return r
}

func TestCapacityOutcome(t *testing.T) {
	if o, d := capacityOutcome(passingResult()); o != pass {
		t.Fatalf("the passing result: %s, %s", o, d)
	}
	for _, tc := range []struct {
		name   string
		change func(r *Result)
		reason string
	}{
		{"an error", func(r *Result) { r.Capacity.errors, r.Capacity.problem = 1, "status 500" }, "1 errors (status 500)"},
		{"a misfiled record", func(r *Result) { r.Capacity.misfiled = 1 }, "1 records misfiled"},
		{"a missing record", func(r *Result) { r.Capacity.missing = 1 }, "0 records misfiled, 1 missing"},
		{"a late send", func(r *Result) { r.Capacity.late = 1 }, "1 requests left late"},
		{"below 99% of the rate", func(r *Result) { r.Capacity.achieved = 197 }, "achieved 197.0/s of 200/s"},
		{"not paced", func(r *Result) { r.Capacity.elapsed = 28 * time.Second }, "were not paced"},
		{"no hits measured", func(r *Result) { r.Capacity.hitServer = nil }, "0 hits and 20 misses"},
		{"no misses measured", func(r *Result) { r.Capacity.missServer = nil }, "80 hits and 0 misses"},
		{"slow hits", func(r *Result) { r.Capacity.hitServer = times(80, 21*time.Millisecond) }, "hit p95 21.00 ms under load exceeds NFR-1"},
		{"slow misses", func(r *Result) { r.Capacity.missServer = times(20, 201*time.Millisecond) }, "miss p95 201 ms under load exceeds NFR-2"},
		{"queued hits", func(r *Result) { r.Capacity.hitWait = times(80, 21*time.Millisecond) }, "hit p95 21.00 ms from the scheduled send"},
		{"queued misses", func(r *Result) { r.Capacity.missWait = times(20, 201*time.Millisecond) }, "miss p95 201 ms from the scheduled send"},
		{"too much memory", func(r *Result) { r.RSSPeak = targetRSS + 1<<20 }, "peak RSS 513.0 MiB exceeds 512 MiB"},
		{"memory at the end", func(r *Result) { r.RSSEnd = targetRSS + 1<<20 }, "peak RSS 513.0 MiB"},
		{"memory unread", func(r *Result) { r.RSSStart, r.RSSPeak, r.RSSEnd = 0, 0, 0 }, "memory could not be read"},
	} {
		r := passingResult()
		tc.change(r)
		o, d := capacityOutcome(r)
		if o != fail || !strings.Contains(d, tc.reason) {
			t.Errorf("%s: capacityOutcome = %s, %q; want FAIL with %q", tc.name, o, d, tc.reason)
		}
	}

	r := passingResult()
	r.Options.GOMAXPROCS = 2
	if o, d := capacityOutcome(r); o != notJudged || !strings.Contains(d, "more than one CPU") {
		t.Errorf("GOMAXPROCS 2: %s, %q", o, d)
	}
	r.Options.GOMAXPROCS = -1
	if o, _ := capacityOutcome(r); o != notJudged {
		t.Errorf("GOMAXPROCS unset: %s", o)
	}
}

func TestVerdictsAndReport(t *testing.T) {
	r := passingResult()
	vs := verdicts(r)
	var got []string
	for _, v := range vs {
		got = append(got, v.nfr+" "+v.outcome)
	}
	if want := []string{"NFR-1 PASS", "NFR-2 PASS", "NFR-3 PASS", "NFR-5 PASS"}; !slices.Equal(got, want) {
		t.Fatalf("verdicts = %v, want %v", got, want)
	}
	var out bytes.Buffer
	if !writeReport(&out, r) {
		t.Fatalf("the passing result did not pass:\n%s", out.String())
	}
	if strings.Contains(out.String(), "DRY RUN") {
		t.Errorf("a run at %d links is reported as a dry run", QualifyingLinks)
	}

	for _, tc := range []struct {
		name   string
		change func(r *Result)
		want   string // a line of the requirements list
	}{
		{"a slow hit phase", func(r *Result) { r.Phases[0].server = times(100, targetHit+1) }, "NFR-1  FAIL"},
		{"a slow miss phase", func(r *Result) { r.Phases[1].server = times(100, targetMiss+1) }, "NFR-2  FAIL"},
		{"a slow search phase", func(r *Result) { r.Phases[2].server = times(100, targetSearch+1) }, "NFR-3  FAIL"},
		{"a search record missing", func(r *Result) { r.Phases[2].missingRecords = 1 }, "NFR-3  INCOMPLETE"},
		{"capacity", func(r *Result) { r.Capacity.late = 1 }, "NFR-5  FAIL"},
		// The home page has no target, but must still be answered right.
		{"the home page failing", func(r *Result) { r.Phases[3].errors = 1 }, ""},
		{"the home page unlogged", func(r *Result) { r.Phases[3].missingRecords = 1 }, ""},
	} {
		r := passingResult()
		tc.change(r)
		var out bytes.Buffer
		if writeReport(&out, r) {
			t.Errorf("%s: the report passed:\n%s", tc.name, out.String())
		}
		if tc.want != "" && !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: the report lacks %q:\n%s", tc.name, tc.want, out.String())
		}
	}

	// A requirement not judged does not fail the run.
	r = passingResult()
	r.Options.GOMAXPROCS = -1
	if out.Reset(); !writeReport(&out, r) || !strings.Contains(out.String(), "NFR-5  not judged") {
		t.Errorf("a run with every core: \n%s", out.String())
	}
}

func TestExpectStatus(t *testing.T) {
	response := func(status int, contentType, location string) *http.Response {
		h := http.Header{}
		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
		if location != "" {
			h.Set("Location", location)
		}
		return &http.Response{StatusCode: status, Header: h}
	}
	redirect, page := expectStatus(http.StatusFound, ""), expectStatus(http.StatusNotFound, "text/html")
	for _, tc := range []struct {
		name  string
		check func(*http.Response) string
		resp  *http.Response
		want  string // "" for no problem
	}{
		{"a redirect", redirect, response(302, "", "https://example.com/"), ""},
		{"a 200 for a redirect", redirect, response(200, "text/html", ""), "status 200, want 302"},
		{"a redirect with no Location", redirect, response(302, "", ""), "a redirect without a Location"},
		{"a miss page", page, response(404, "text/html; charset=utf-8", ""), ""},
		{"a miss as JSON", page, response(404, "application/json", ""), `content type "application/json", want text/html`},
		{"a miss with no type", page, response(404, "", ""), "want text/html"},
	} {
		if got := tc.check(tc.resp); (tc.want == "" && got != "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: problem %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTallyClass(t *testing.T) {
	c := &class{logged: resolverDecision("hit")}
	server, misfiled := tallyClass(c, []requestRecord{
		{area: "resolver", decision: "hit", duration: 1},
		{area: "resolver", decision: "miss_known", duration: 2},
		{area: "internal", route: "/-/{$}", duration: 3},
		{area: "resolver", decision: "hit", duration: 4},
	})
	if !slices.Equal(server, []time.Duration{1, 2, 3, 4}) || misfiled != 2 {
		t.Fatalf("tallyClass = %v, %d misfiled; want every duration and 2 misfiled", server, misfiled)
	}
}

func TestSplitResolverRecords(t *testing.T) {
	hits, misses, misfiled := splitResolverRecords([]requestRecord{
		{area: "resolver", decision: "hit", duration: 1},
		{area: "resolver", decision: "miss_known", duration: 2},
		{area: "resolver", decision: "miss_unknown", duration: 3},
		{area: "internal", route: "/-/{$}", duration: 4},
		{area: "resolver", decision: "probe", duration: 5},
	})
	if !slices.Equal(hits, []time.Duration{1}) || !slices.Equal(misses, []time.Duration{2, 3}) || misfiled != 2 {
		t.Fatalf("splitResolverRecords = %v, %v, %d", hits, misses, misfiled)
	}
}
