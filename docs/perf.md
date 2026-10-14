# Performance run (L4.7): method and results

| | |
|---|---|
| **Status** | Qualifying run at 100,000 links done on 2026-10-14 (three runs, median below): NFR-1, NFR-2, NFR-3 and NFR-5 pass. The first three runs failed NFR-5 on memory; the fix and its effect are recorded under [Results](#results). Not yet repeated from a clean checkout on mains power. |
| **Requirements** | NFR-1, NFR-2, NFR-3, NFR-5 of [PRD.md](PRD.md) section 8 |
| **Tool** | `cmd/ogre-perf` (code in `internal/perf`), run with `make perf` |
| **When** | Before each tag (plan section 4), on a quiet machine |

This is the record that task L4.7 of [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) asks for. The tool generates 100,000 links, starts the real `ogre` binary against them, measures it over HTTP, and judges the results against the PRD. It also prints the rows of the results tables below as Markdown, ready to paste in.

## The requirements

| Requirement | What it says | How the run judges it |
|---|---|---|
| NFR-1 | A hit takes at most 20 ms of server time at the 95th percentile, with 100,000 links. | p95 of server time for hits ≤ 20 ms |
| NFR-2 | A miss page, including its suggestions, is produced within 200 ms at the 95th percentile. | p95 of server time ≤ 200 ms, separately for known-name and unknown-name misses |
| NFR-3 | Search results return within 150 ms at the 95th percentile. | p95 of server time ≤ 150 ms, separately for one-word and two-word queries |
| NFR-5 | One modest instance (1 vCPU, 512 MB) serves 100,000 links at 200 resolutions per second. | See [Fixed rate](#the-fixed-rate-phase-nfr-5) |

A requirement fails as well when any request in its classes was answered other than expected (a wrong status or content type, or a request record of another kind in the server's log), because the run then measured something other than what its table says.

## How to run it

On the machine the numbers are for, from the repository root:

```sh
make perf
```

That builds `bin/ogre` and runs `go run ./cmd/ogre-perf -bin bin/ogre`. The dry run at 10,000 links took about a minute; at 100,000 links one run takes about two minutes (1:57 to 2:08 on the machine below, build included). Flags go in `PERF_ARGS`:

```sh
make perf PERF_ARGS="-links 10000 -rate-for 10s"     # a dry run, about a minute
make perf PERF_ARGS="-keep"                           # keep the database and the server's log
```

| Flag | Default | Meaning |
|---|---|---|
| `-links` | 100000 | Links to generate. Below 100,000 the report is marked as a dry run. |
| `-seed` | 1 | Picks the generated links and requests. The same seed gives the same ones on every machine. |
| `-requests` | 2000 | Requests per class, after a warm-up of a tenth as many that is not counted. |
| `-concurrency` | 1 | Requests of one class in flight at once. |
| `-gomaxprocs` | 1 | `GOMAXPROCS` of the server; `-1` leaves it unset. |
| `-rate`, `-rate-for` | 200, 30s | The fixed-rate phase. |
| `-startup-runs` | 3 | Times the server is started to measure startup. |
| `-dir`, `-keep` | | Where the database and `server.log` go, and whether they are kept. |
| `-bin` | `bin/ogre` | The binary to measure. |

The exit status is 0 when every requirement it judged passed, 1 when one did not, and 2 when the run could not be completed.

The run never touches a Directory you are using. It creates its database in a new temporary directory (removed afterwards unless `-keep`), refuses a `-dir` that already holds one, passes `--db` and `--addr` explicitly, drops every `OGRE_` variable from the server's environment, and listens on a free loopback port, never 4655.

### Before the qualifying run

The numbers are only worth recording on a quiet machine:

- [ ] On mains power, not battery (the report shows which; macOS slows the CPU down on battery).
- [ ] Nothing else heavy running: no other builds, test runs, agents, or browsers playing video. The client and the server share the machine.
- [ ] A clean checkout at the commit being tagged (the report shows `-dirty` otherwise).
- [ ] `make perf`, unchanged defaults. Paste the Markdown it prints into [Results](#results), and fill in the remaining fields by hand.

## Method

### The Directory

`internal/perf/generate.go` writes the links. The mix is meant to look like a real team's Directory:

| Kind | Share | Routes | Example |
|---|--:|---|---|
| A single address | 70% | `name` | `go/q3-okrs` to a document |
| A search box | 15% | `name/{q}`, half also `name` | `go/kb/vpn` |
| A tracker | 9% | `name`, `name/new`, `name/{id:int}`, `name/{key:regex([A-Z]+-[0-9]+)}/{tab}` | `go/ops/OPS-42/comments` |
| Dated reports | 4% | `name`, `name/{year:int}/{month:regex(…)}`, the same with `/{day:int}` | `go/sales-reports/2026/10` |
| Many areas | 2% | `name` and 5 to 19 more, up to the 20-route limit | `go/monorepo/area3/build-12` |

Names are one to three words from a team's vocabulary (systems, documents, teams), mostly joined with `-`, sometimes with a year or a number; they average about 15 characters. About one in seven links has no description; the others have a sentence of 8 to 112 characters, 50 on average. At 100,000 links there are about 166,000 routes. Half the variables carry a description and an example. Destinations are realistic URLs under `example.com`: document and sheet IDs, repositories, dashboards with query strings. Creation times are spread over two years. Every link passes the link model's save rules (`linkmodel.ValidateDraft`); the generator fails if one does not.

The links are written into the database in one transaction (`internal/perf/seed.go`) rather than through `store.CreateLink`, which syncs to disk once per link by design (NFR-7) and would take about five minutes for 100,000 links. The test `TestSeedMatchesStore` saves the same links both ways and requires the same links, IDs and history, so the bulk path cannot quietly drift from what the product writes. The server then opens the file and loads every link at startup the normal way, and that is part of what the run measures.

### The requests

The generator also writes the requests, because it knows which request fits which route. `TestWorkloadDecisions` replays every one of them against the resolver loaded from a seeded database and checks that each is the decision its class claims.

| Class | Request | Expected answer |
|---|---|---|
| Hit | A path one of the link's routes takes: `/name`, `/name/term`, `/name/1234`, `/name/OPS-42/comments`, `/name/2026/10` | `302` with a `Location` |
| Miss page, known name | A real name with a request none of its routes fits: a month of 13, a value of the wrong type, a segment too many | `404`, HTML |
| Miss page, unknown name | Half are one typo away from a real name (swapped, dropped, or doubled letter; sometimes capitalized; sometimes with a value to carry over), so the page has suggestions to rank; half are nothing like any name, so every name is looked at and rejected | `404`, HTML |
| Search API, one word | `GET /-/api/v1/links?q=…`, the request the search box makes while typing: a whole word, its first two to four letters, a misspelled word, a word from descriptions or destinations | `200`, JSON |
| Search API, two words | The same with two such words | `200`, JSON |
| Home page | `GET /-/` | `200`, HTML |

Each class has up to 5,000 different requests (500 queries for each kind of search), taken from links spread over the whole Directory, and cycles through them.

### What is measured

- **Server time** is the `duration` the server writes in its request log for each request: from when the request reaches the handler until the handler returns. It contains no network and no client. This is what NFR-1 to NFR-3 limit, and what the verdicts use. The run reads the server's log as it is written and checks each record against its class (`decision=hit`, `route=/-/api/v1/links`, and so on); the record count must equal the number of requests answered.
- **Round trip** is what the client saw, from sending the request until the whole body was read, over a kept-alive loopback connection. It is shown beside server time so that a slow write of a large response, which happens after the handler returns, would not go unnoticed.
- **Percentiles** use the nearest-rank method: p95 is the smallest time that at least 95% of requests did not exceed. No interpolation, so every figure is a time some request really took.
- **Each class runs on its own**, after a warm-up of a tenth as many requests that is not counted. By default one request is in flight at a time: the Directory is used by one person, and server time should then be the time the request takes, not time spent queued behind others. `-concurrency` raises it.
- **The server runs with `GOMAXPROCS=1`** for every phase: the one vCPU of NFR-5. The latency verdicts therefore hold on one core, the harder case.
- **Startup** is the time from starting the process to the first successful answer of `/-/healthz`. The server binds its port first and loads every link before it answers, so this includes opening the database and building the index. It is measured `-startup-runs` times; the last start serves the load.
- **Memory** is the resident set size of the server process (`ps -o rss` on macOS, `/proc/<pid>/status` on Linux): right after startup, the most seen while serving (sampled every 250 ms), and after the load.

### The fixed-rate phase (NFR-5)

For 30 seconds the client sends 200 requests a second at fixed times, whether or not earlier ones have been answered, as the browsers of independent users would. Eight in ten are hits, one in ten a known-name miss, one in ten an unknown-name miss. NFR-5 passes when:

- at least 99% of the asked rate was answered, with no error, and no request left more than 100 ms after its scheduled time;
- the phase lasted at least 95% of its 30 seconds, which shows the requests were paced and not sent in a burst;
- under that load, hits still meet NFR-1 and misses NFR-2 at the 95th percentile, both in server time and in the wait from each request's scheduled send to the end of its answer. Server time starts when the handler does, so time a request spent queued for the server's one CPU is not in it; the wait from the scheduled send includes it, and it is the delay load adds. It also includes the client's own work and the loopback round trip, so it is the stricter of the two;
- the server's memory never exceeded 512 MiB;
- the server ran with `GOMAXPROCS=1` (otherwise the verdict is "not judged").

The share of one core the server used during the phase is reported as the headroom.

### What this does not show

- **A cloud vCPU is slower than a laptop core.** `GOMAXPROCS=1` keeps the server's Go code on one core, but that core is the laptop's. The deployed form of NFR-5 is the container load test of plan task C2.9 (1 vCPU, 512 MB, in a container).
- **Client and server share the machine.** Under the fixed rate the client's own work competes with the server's; the round trip includes it, server time does not.
- **The request log is on.** Every request writes a log line to a pipe the tool reads. That is how the product runs, so it is counted; a server with logging off would be faster.
- **Memory is RSS as each system reports it.** A container's memory limit counts somewhat differently (it can include the page cache, for one). Unless the peak comes near 512 MiB, the difference does not change the verdict; if it does, C2.9 settles it.
- **No browser.** Rendering and the extension's redirect are not timed; NFR-4 covers the redirect, by design (a declarative rule).

## Results

### Qualifying run (100,000 links)

`make perf` with unchanged defaults, three times in a row, after the fix described [below](#nfr-5-failed-first-peak-memory). Each figure is the median of the three runs; the range in brackets is the lowest and highest of the three. Every run exited 0 with all four requirements passing.

| Field | Value |
|---|---|
| Date | 2026-10-14, runs started 18:23, 18:25, 18:27 CEST |
| Commit | d6d91bf-dirty: the uncommitted L4 work in the tree, plus the memory fix below |
| Ogre | ogre 0.1.0-dev, built by `make perf` (`make build` first) |
| Machine | Mac14,9 (MacBook Pro, Apple M2 Pro, 10 cores), 16 GiB; macOS 26.5 (25F71), darwin/arm64 |
| Power | Battery (65%, discharging). **Not on mains power**, as the checklist asks |
| Go | go1.26.1 darwin/arm64 |
| Links | 100,000 (seed 1), about 166,000 routes; database 101.3 MiB; generated in 0.67 s, written in 2.96 s |
| Server | GOMAXPROCS=1, request log at level info |
| Client | 1 in flight, 2000 requests per class, warm-up 200 |
| Run by | Claude (coding agent) for the owner |
| Other load on the machine | No other build, test or agent running. `top` just before: 86% idle, load average 2.42 / 5.59 / 5.38 (1, 5, 15 min; the 5- and 15-minute figures still held the agents that had just finished), the owner's Chrome about 10% of a core, an idle `go run ./cmd/ogre` in a terminal (its own port and database, not touched), 847 MiB of RAM free with 6.2 GiB compressed. Load average 2.47 when the three runs started. |

| Request | n | Server p50 | Server p95 | Server p99 | Round trip p95 | Limit (p95) | Verdict |
|---|--:|--:|--:|--:|--:|---|---|
| Hit (302) | 2000 | 0.004 ms [0.004–0.004] | 0.008 ms [0.008–0.008] | 0.011 ms [0.011–0.012] | 0.124 ms [0.117–0.135] | 20 ms (NFR-1) | PASS |
| Miss page, known name | 2000 | 0.048 ms [0.047–0.054] | 0.088 ms [0.087–0.111] | 0.152 ms [0.145–0.153] | 0.163 ms [0.163–0.267] | 200 ms (NFR-2) | PASS |
| Miss page, unknown name | 2000 | 2.29 ms [2.28–2.29] | 2.88 ms [2.87–2.88] | 3.00 ms [2.99–3.01] | 3.01 ms [3.00–3.02] | 200 ms (NFR-2) | PASS |
| Search API, one word | 2000 | 14.40 ms [14.29–14.50] | 18.93 ms [18.76–19.00] | 22.35 ms [22.31–22.63] | 19.34 ms [19.09–19.39] | 150 ms (NFR-3) | PASS |
| Search API, two words | 2000 | 14.98 ms [14.83–15.03] | 19.58 ms [18.97–19.73] | 23.86 ms [23.68–24.04] | 19.93 ms [19.68–20.12] | 150 ms (NFR-3) | PASS |
| Home page | 2000 | 4.90 ms [4.75–5.25] | 5.89 ms [5.62–6.16] | 6.38 ms [6.03–6.54] | 6.19 ms [5.93–6.53] | none | — |

| Measure | Result |
|---|---|
| Startup to first health check | 1469 ms (median of the three runs' medians); all nine starts 1415–1495 ms. No NFR limits it. |
| RSS after startup | 316.0 MiB [306.5–317.4] |
| RSS peak while serving | 412.3 MiB [402.6–417.3], limit 512 MiB (NFR-5) |
| RSS after the load (end of the run, after the fixed-rate phase) | 412.3 MiB [401.8–417.3] |
| Fixed rate 200/s for 30 s | 6000 sent, 200.0/s answered, 0 errors, 0 late in every run; the phase took 29.996 s. Server CPU 13.3% of one core [13.2–13.3]. Server p95: hits 0.034 ms [0.032–0.035], misses 6.03 ms [5.97–6.26]. From the scheduled send to the answer, p95: hits 1.90 ms [1.84–2.24], misses 7.73 ms [7.52–7.82]. |

| Requirement | Verdict | Detail |
|---|---|---|
| NFR-1 | PASS | Hit p95 0.008 ms against 20 ms; 0.034 ms under the fixed rate |
| NFR-2 | PASS | Known-name miss p95 0.088 ms, unknown-name miss p95 2.88 ms, against 200 ms; misses 6.03 ms under the fixed rate (7.73 ms from the scheduled send) |
| NFR-3 | PASS | One word p95 18.93 ms, two words p95 19.58 ms, against 150 ms |
| NFR-5 | PASS | 200.0/s of 200/s with no error and none late; peak RSS 412.3 MiB against 512 MiB (about 100 MiB, a fifth, to spare); GOMAXPROCS=1 |

What these numbers leave open:

- **Memory is the tight one.** The latency limits are met at 8 to 2,500 times below their limit; memory at 0.8 of it. The margin is smaller than it looks for two reasons. Peak RSS follows the Go collector's heap target, which is twice the live heap by default, so every megabyte that the index keeps costs about two of RSS. And the fix below shares compiled regular expressions: the generated Directory uses three distinct expressions, so sharing removes nearly all their cost here. A Directory where every regex route had its own expression would get none of that back and would land at about the limit again: about 45 MiB more live heap, roughly 90 MiB more RSS, which is where the runs before the fix were (517 to 536 MiB, less the few MiB the builder changes saved). Real Directories repeat expressions (a ticket key, a month), but how much is not known. C2.9's container test, which measures memory as the container's limit counts it, settles NFR-5 for the deployed form.
- **Search is the slowest request**, at about an eighth of its limit: it scans every link in memory (plan section 3.2). Not a problem at 100,000 links on this machine; it is the first thing to look at on a slower vCPU.
- **Startup takes about 1.5 s** at 100,000 links (it was 1.8 s before the fix): opening the database, reading every link and compiling it. No requirement limits it.
- Battery power and a working tree with uncommitted changes break two items of the checklist above. Re-run from a clean checkout on mains power before the tag; the run takes about six minutes for all three.

### NFR-5 failed first: peak memory

The same three runs before the fix (18:12, 18:14, 18:16 CEST, same machine and conditions) passed NFR-1 to NFR-3 with the same latencies as above, and **failed NFR-5 in every run: peak RSS 535.9, 517.3 and 522.5 MiB** against 512 MiB (RSS after startup 424.9, 412.9 and 405.3 MiB; startup 1813 ms median). Everything else about NFR-5 passed: 200.0/s answered, 0 errors, 0 late, server CPU 13.5%.

**Cause.** A heap profile (`runtime/pprof`, after `app.New` over the same 100,000 seeded links, collected) showed 211 MiB live, and 832 MiB allocated while loading. With the collector's default target of twice the live heap, that alone reaches about 420 MiB before RSS counts the runtime, the SQLite driver and request garbage. Of the live heap:

| What | Live heap | Why |
|---|--:|---|
| Compiled regular expressions (`linkmodel.newRegexType` → `regexp.Compile`) | 49.5 MiB, 23% | Every regex variable of every route compiled its own `*regexp.Regexp`, about 1.2 KiB each, roughly 41,000 of them (estimated from the mix), although they come from three distinct expressions |
| Other compiled patterns and templates (`linkmodel.Compile`) | 68 MiB | The parsed segments, canonical text and shape, templates |
| Search documents (`index.newDoc`) | 24 MiB | Lowercased text of each link, built by appending, so up to half of each buffer was unused capacity |
| Canonical text and shape of patterns (`linkmodel.newPattern`) | 12 MiB | Two builders per pattern, also grown by appending; a literal-only pattern, 70% of links, held the same text twice |
| Text read from SQLite, private copies of routes | 43 MiB | The links themselves (`index.own` keeps a private copy by design) |

**Fix** (internal/linkmodel and internal/index only):

- `internal/linkmodel/regexcache.go`: routes with the same expression share one compiled `*regexp.Regexp` (safe: it is safe for concurrent use and never changed after compiling). The cache holds weak pointers and a cleanup removes an entry once its expression is collected, so it never keeps an expression alive and expressions typed into the form cannot grow it. Tests in `regexcache_test.go`, including one that fails if the cleanup is removed (checked).
- `internal/linkmodel/pattern.go` (`newPattern`): builders sized before they are filled; a pattern without variables uses one string as its text and its shape.
- `internal/index/search.go` (`newDoc`): the builder is sized before it is filled.

Live heap after loading went from 211.5 to 161.4 MiB, and allocation while loading from 832 to 580 MiB. In the runs: peak RSS from 522.5 to 412.3 MiB (medians), RSS after startup from 412.9 to 316.0 MiB, startup from 1813 to 1469 ms. Latencies did not change beyond noise (the home page went from 6.0 to 4.9 ms at p50). `go test -race ./...`, `go vet ./...`, staticcheck 2026.2.2 and gofmt are clean.

### Dry run (10,000 links): proves the tool, not the product

Run while other work was going on on the same machine, on battery, from a working tree with uncommitted changes, by the tool as it was before the fixed-rate phase also judged the wait from each request's scheduled send and the phase's length; a later dry run at 2,000 links and 200/s for 5 s measured that wait at a p95 of 1.7 ms for hits and 2.0 ms for misses. The verdicts below say only that the tool works end to end; the numbers are not a measurement of the product. Command: `go run ./cmd/ogre-perf -bin <freshly built ogre> -links 10000`, otherwise the defaults. Exit status 0.

| Field | Value |
|---|---|
| Date | 2026-10-14 16:18 CEST |
| Commit | d6d91bf-dirty |
| Ogre | ogre 0.1.0-dev |
| Machine | darwin/arm64, macOS 26.5; Apple M2 Pro; 10 cores; 16 GiB |
| Power | Battery Power |
| Go | go1.26.1 |
| Links | 10000 (seed 1), database 9.8 MiB |
| Server | GOMAXPROCS=1 |
| Client | 1 in flight, 2000 requests per class, warm-up 200 |

| Request | n | Server p50 | Server p95 | Server p99 | Round trip p95 | Limit (p95) | Verdict |
|---|--:|--:|--:|--:|--:|---|---|
| Hit (302) | 2000 | 0.004 ms | 0.008 ms | 0.012 ms | 0.106 ms | 20 ms (NFR-1) | PASS |
| Miss page, known name | 2000 | 0.048 ms | 0.110 ms | 0.175 ms | 0.238 ms | 200 ms (NFR-2) | PASS |
| Miss page, unknown name | 2000 | 0.273 ms | 0.355 ms | 0.388 ms | 0.461 ms | 200 ms (NFR-2) | PASS |
| Search API, one word | 2000 | 1.65 ms | 2.21 ms | 2.56 ms | 2.53 ms | 150 ms (NFR-3) | PASS |
| Search API, two words | 2000 | 1.58 ms | 2.12 ms | 2.49 ms | 2.39 ms | 150 ms (NFR-3) | PASS |
| Home page | 2000 | 0.716 ms | 0.948 ms | 1.12 ms | 1.74 ms | none | — |

| Measure | Result |
|---|---|
| Startup to first health check | 163 ms, 161 ms, 165 ms (median 163 ms) |
| RSS after startup / peak / after load | 66.6 MiB / 85.7 MiB / 85.7 MiB |
| Fixed rate 200/s for 30s | 200.0/s answered, 0 errors; server CPU 7.2% of one core; server p95 hits 0.035 ms, misses 1.23 ms |
