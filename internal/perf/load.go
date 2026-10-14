package perf

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// class is one kind of request the run measures on its own.
type class struct {
	key   string // as in the report: "hit", "search_1"
	label string
	// target is the 95th-percentile limit of server time the PRD sets for
	// this kind of request, with its requirement; 0 when it sets none.
	target time.Duration
	nfr    string
	paths  []string // request URIs, cycled through
	// check says what is wrong with a response, or "" when it is what this
	// class expects.
	check func(*http.Response) string
	// logged says whether a request record belongs to this class, so that a
	// run whose requests were answered some other way is caught.
	logged func(requestRecord) bool
}

// Targets of the PRD's section 8.
const (
	targetHit    = 20 * time.Millisecond  // NFR-1
	targetMiss   = 200 * time.Millisecond // NFR-2
	targetSearch = 150 * time.Millisecond // NFR-3
	// targetRSS is the memory of NFR-5's modest instance. Container limits
	// are given in MiB, so 512 MiB it is.
	targetRSS = 512 << 20
)

func expectStatus(status int, mediaType string) func(*http.Response) string {
	return func(r *http.Response) string {
		if r.StatusCode != status {
			return fmt.Sprintf("status %d, want %d", r.StatusCode, status)
		}
		if mediaType != "" {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != mediaType {
				return fmt.Sprintf("content type %q, want %s", r.Header.Get("Content-Type"), mediaType)
			}
		}
		if status == http.StatusFound && r.Header.Get("Location") == "" {
			return "a redirect without a Location"
		}
		return ""
	}
}

func resolverDecision(decision string) func(requestRecord) bool {
	return func(r requestRecord) bool { return r.area == "resolver" && r.decision == decision }
}

// classes builds the measured classes from what the generator wrote.
func classes(gen *Generated) []class {
	search := func(queries []string) []string {
		out := make([]string, len(queries))
		for i, q := range queries {
			// The request search.js makes as the user types.
			out[i] = "/-/api/v1/links?q=" + url.QueryEscape(q)
		}
		return out
	}
	searchLogged := func(r requestRecord) bool {
		return r.area == "internal" && r.route == "/-/api/v1/links" && r.status == http.StatusOK
	}
	return []class{
		{key: "hit", label: "Hit (302)", target: targetHit, nfr: "NFR-1", paths: gen.Hits,
			check: expectStatus(http.StatusFound, ""), logged: resolverDecision("hit")},
		{key: "miss_known", label: "Miss page, known name", target: targetMiss, nfr: "NFR-2", paths: gen.KnownMisses,
			check: expectStatus(http.StatusNotFound, "text/html"), logged: resolverDecision("miss_known")},
		{key: "miss_unknown", label: "Miss page, unknown name", target: targetMiss, nfr: "NFR-2", paths: gen.UnknownMisses,
			check: expectStatus(http.StatusNotFound, "text/html"), logged: resolverDecision("miss_unknown")},
		{key: "search_1", label: "Search API, one word", target: targetSearch, nfr: "NFR-3", paths: search(gen.SearchOne),
			check: expectStatus(http.StatusOK, "application/json"), logged: searchLogged},
		{key: "search_2", label: "Search API, two words", target: targetSearch, nfr: "NFR-3", paths: search(gen.SearchTwo),
			check: expectStatus(http.StatusOK, "application/json"), logged: searchLogged},
		{key: "home", label: "Home page", paths: []string{"/-/"},
			check: expectStatus(http.StatusOK, "text/html"),
			// The mux pattern of the home page, as the request log names it.
			logged: func(r requestRecord) bool {
				return r.area == "internal" && r.route == "/-/{$}" && r.status == http.StatusOK
			}},
	}
}

// newClient is a client like a browser's for one site: connections are kept
// alive and reused, redirects are not followed (a hit's work ends with the
// 302; following it would measure example.com).
func newClient(concurrency int) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency + 16,
			MaxIdleConnsPerHost: concurrency + 16,
			IdleConnTimeout:     time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// outcome is one request as the client saw it.
type outcome struct {
	roundTrip time.Duration
	answered  bool   // an HTTP response arrived, so the server logged it
	problem   string // "" when the response was what the class expects
}

func do(ctx context.Context, client *http.Client, base string, c *class, uri string) outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+uri, nil)
	if err != nil {
		return outcome{problem: err.Error()}
	}
	// What a browser sends for a page; the API ignores it.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("User-Agent", "ogre-perf")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return outcome{roundTrip: time.Since(start), problem: err.Error()}
	}
	_, copyErr := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	o := outcome{roundTrip: time.Since(start), answered: true, problem: c.check(resp)}
	if o.problem == "" && copyErr != nil {
		o.problem = "reading the body: " + copyErr.Error()
	}
	if o.problem != "" {
		o.problem = fmt.Sprintf("GET %s: %s", uri, o.problem)
	}
	return o
}

// phase is what one class's measurement found.
type phase struct {
	class     *class
	requests  int
	errors    int
	problem   string // the first error, as an example
	roundTrip []time.Duration
	server    []time.Duration
	// misfiled counts request records that do not look like this class,
	// such as a hit logged during the miss phase.
	misfiled int
	// missingRecords counts answered requests whose record never arrived.
	missingRecords int
	elapsed        time.Duration
}

// ledger keeps the count of requests the server has answered, so each
// phase's records can be told apart in the request log: phases run one
// after the other, and before a phase starts every earlier record is in.
type ledger struct {
	srv      *server
	answered int // requests answered since the server started, as the client saw them
}

// settle waits for the records of every answered request and returns how
// many are missing.
func (l *ledger) settle() int {
	if l.srv.waitRecords(l.answered, 10*time.Second) {
		return 0
	}
	return l.answered - l.srv.recordCount()
}

// runClosed sends n requests of class c, concurrency at a time, each worker
// sending its next request as soon as the previous one is answered.
func runClosed(ctx context.Context, client *http.Client, l *ledger, c *class, n, concurrency int) phase {
	l.settle()
	first := l.answered
	ph := phase{class: c, requests: n}

	var next atomic.Int64
	results := make([][]outcome, concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for w := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n || ctx.Err() != nil {
					return
				}
				results[w] = append(results[w], do(ctx, client, l.srv.baseURL, c, c.paths[i%len(c.paths)]))
			}
		}()
	}
	wg.Wait()
	ph.elapsed = time.Since(start)

	for _, rs := range results {
		for _, o := range rs {
			ph.roundTrip = append(ph.roundTrip, o.roundTrip)
			if o.answered {
				l.answered++
			}
			if o.problem != "" {
				ph.errors++
				if ph.problem == "" {
					ph.problem = o.problem
				}
			}
		}
	}
	ph.missingRecords = l.settle()
	ph.server, ph.misfiled = tallyClass(c, l.srv.recordsBetween(first, l.answered))
	return ph
}

// tallyClass takes the server times of one phase's request records, and
// counts the records that do not belong to class c: a request the server
// answered some other way than the class claims (a hit logged during the
// miss phase) means the phase measured something else, and fails it.
func tallyClass(c *class, recs []requestRecord) (server []time.Duration, misfiled int) {
	for _, rec := range recs {
		server = append(server, rec.duration)
		if !c.logged(rec) {
			misfiled++
		}
	}
	return server, misfiled
}

// splitResolverRecords sorts the fixed-rate phase's records into hits and
// misses, and counts any other record as misfiled: the phase sends nothing
// but go links.
func splitResolverRecords(recs []requestRecord) (hits, misses []time.Duration, misfiled int) {
	for _, rec := range recs {
		switch {
		case rec.area == "resolver" && rec.decision == "hit":
			hits = append(hits, rec.duration)
		case rec.area == "resolver" && (rec.decision == "miss_known" || rec.decision == "miss_unknown"):
			misses = append(misses, rec.duration)
		default:
			misfiled++
		}
	}
	return hits, misses, misfiled
}

// capacity is what the fixed-rate phase (NFR-5) found.
type capacity struct {
	rate     int // requests per second asked for
	duration time.Duration
	sent     int
	errors   int
	problem  string
	// late counts requests sent more than 100 ms after their scheduled
	// time: the client could not keep the rate.
	late int
	// elapsed is how long the phase took, from the first scheduled send to
	// the last answer. A paced phase lasts about duration; one that ends
	// much sooner sent its requests in a burst and measured something else.
	elapsed time.Duration
	// achieved is answered requests per second over the phase.
	achieved float64
	// cpuShare is the server's CPU time over the phase divided by its
	// length: 0.25 means a quarter of one core. Negative when unknown.
	cpuShare   float64
	hitServer  []time.Duration
	missServer []time.Duration
	// hitWait and missWait run from each request's scheduled time to the
	// end of its answer: what a user whose browser asked at that moment
	// would wait. Unlike server time, which starts when the handler does,
	// they include time spent in the accept backlog or waiting for the
	// server's one CPU, which is the delay load adds; and unlike round
	// trip, they include a send the client made late.
	hitWait   []time.Duration
	missWait  []time.Duration
	roundTrip []time.Duration
	misfiled  int
	missing   int
}

// runRate sends rate requests a second for d, open-loop: each request leaves
// at its scheduled time whether or not earlier ones were answered, as
// independent users' browsers do. Eight in ten are hits; one in ten names a
// known link with a value no route takes; one in ten names no link.
func runRate(ctx context.Context, client *http.Client, l *ledger, cls []class, rate int, d time.Duration) capacity {
	byKey := map[string]*class{}
	for i := range cls {
		byKey[cls[i].key] = &cls[i]
	}
	hit, known, unknown := byKey["hit"], byKey["miss_known"], byKey["miss_unknown"]

	l.settle()
	first := l.answered
	cp := capacity{rate: rate, duration: d, cpuShare: -1}
	n := int(d.Seconds() * float64(rate))
	interval := time.Second / time.Duration(rate)

	cpuBefore, cpuErr := cpuTime(l.srv.pid())
	results := make([]outcome, n)
	waits := make([]time.Duration, n)
	isHit := make([]bool, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n && ctx.Err() == nil; i++ {
		due := start.Add(time.Duration(i) * interval)
		time.Sleep(time.Until(due))
		if time.Since(due) > 100*time.Millisecond {
			cp.late++
		}
		c := hit
		switch i % 10 {
		case 3:
			c = known
		case 7:
			c = unknown
		}
		uri := c.paths[(i/10)%len(c.paths)]
		isHit[i] = c == hit
		cp.sent++
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = do(ctx, client, l.srv.baseURL, c, uri)
			waits[i] = time.Since(due)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	cp.elapsed = elapsed
	if cpuAfter, err := cpuTime(l.srv.pid()); err == nil && cpuErr == nil {
		cp.cpuShare = (cpuAfter - cpuBefore).Seconds() / elapsed.Seconds()
	}

	answered := 0
	for i, o := range results[:cp.sent] {
		cp.roundTrip = append(cp.roundTrip, o.roundTrip)
		if o.answered {
			answered++
			if isHit[i] {
				cp.hitWait = append(cp.hitWait, waits[i])
			} else {
				cp.missWait = append(cp.missWait, waits[i])
			}
		}
		if o.problem != "" {
			cp.errors++
			if cp.problem == "" {
				cp.problem = o.problem
			}
		}
	}
	l.answered += answered
	cp.achieved = float64(answered) / elapsed.Seconds()
	cp.missing = l.settle()
	cp.hitServer, cp.missServer, cp.misfiled = splitResolverRecords(l.srv.recordsBetween(first, l.answered))
	return cp
}

// rssSampler records the server's resident memory every interval and keeps
// the largest value seen.
type rssSampler struct {
	stop chan struct{}
	done chan struct{}
	peak int64
	err  error // the last error, if sampling failed
}

func sampleRSS(pid int, every time.Duration) *rssSampler {
	s := &rssSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			// Only this goroutine writes peak and err until done is closed.
			if b, err := residentBytes(pid); err != nil {
				s.err = err
			} else if b > s.peak {
				s.peak = b
			}
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
	}()
	return s
}

// finish stops sampling and returns the peak.
func (s *rssSampler) finish() (int64, error) {
	close(s.stop)
	<-s.done
	if s.peak == 0 && s.err != nil {
		return 0, s.err
	}
	return s.peak, nil
}
