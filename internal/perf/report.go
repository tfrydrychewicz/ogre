package perf

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// verdict is one requirement's outcome.
type verdict struct {
	nfr, outcome, detail string
}

const (
	pass       = "PASS"
	fail       = "FAIL"
	incomplete = "INCOMPLETE" // the run could not measure it properly
	notJudged  = "not judged"
)

// phaseOutcome judges one class: its 95th percentile of server time against
// its target, provided every request was answered as the class expects and
// every one of them was found in the request log.
func phaseOutcome(ph phase) string {
	switch {
	case ph.errors > 0 || ph.misfiled > 0:
		return fail
	case ph.missingRecords > 0 || len(ph.server) == 0:
		return incomplete
	case ph.class.target == 0:
		return "—"
	case summarize(ph.server).p95 <= ph.class.target:
		return pass
	}
	return fail
}

func capacityOutcome(r *Result) (string, string) {
	c := r.Capacity
	var why []string
	if c.errors > 0 {
		why = append(why, fmt.Sprintf("%d errors (%s)", c.errors, c.problem))
	}
	if c.misfiled > 0 || c.missing > 0 {
		why = append(why, fmt.Sprintf("%d records misfiled, %d missing", c.misfiled, c.missing))
	}
	if c.late > 0 {
		why = append(why, fmt.Sprintf("%d requests left late: the client could not keep the rate", c.late))
	}
	if c.achieved < 0.99*float64(c.rate) {
		why = append(why, fmt.Sprintf("achieved %.1f/s of %d/s", c.achieved, c.rate))
	}
	// A phase sent in a burst would show a higher achieved rate, not a
	// lower one; only its length gives it away. A paced phase's last request
	// leaves one interval before the end.
	if c.elapsed < c.duration*95/100 {
		why = append(why, fmt.Sprintf("the phase took %v of %v: the requests were not paced", c.elapsed.Round(time.Millisecond), c.duration))
	}
	if len(c.hitServer) == 0 || len(c.missServer) == 0 {
		why = append(why, fmt.Sprintf("%d hits and %d misses were measured: both are needed", len(c.hitServer), len(c.missServer)))
	}
	if p := summarize(c.hitServer).p95; p > targetHit {
		why = append(why, fmt.Sprintf("hit p95 %s ms under load exceeds NFR-1", ms(p)))
	}
	if p := summarize(c.missServer).p95; p > targetMiss {
		why = append(why, fmt.Sprintf("miss p95 %s ms under load exceeds NFR-2", ms(p)))
	}
	// Server time starts with the handler, so time a request spent queued
	// for the server's one CPU is not in it. The wait from each request's
	// scheduled time is, and must meet the same limits: a server that keeps
	// the rate only by making requests queue does not serve it.
	if p := summarize(c.hitWait).p95; p > targetHit {
		why = append(why, fmt.Sprintf("hit p95 %s ms from the scheduled send exceeds NFR-1's limit: requests queued", ms(p)))
	}
	if p := summarize(c.missWait).p95; p > targetMiss {
		why = append(why, fmt.Sprintf("miss p95 %s ms from the scheduled send exceeds NFR-2's limit: requests queued", ms(p)))
	}
	peak := max(r.RSSStart, r.RSSPeak, r.RSSEnd)
	switch {
	case peak == 0:
		why = append(why, "memory could not be read")
	case peak > targetRSS:
		why = append(why, fmt.Sprintf("peak RSS %s exceeds 512 MiB", mib(peak)))
	}
	if len(why) > 0 {
		return fail, strings.Join(why, "; ")
	}
	detail := fmt.Sprintf("%.1f/s of %d/s with no error; peak RSS %s ≤ 512 MiB", c.achieved, c.rate, mib(peak))
	if r.Options.GOMAXPROCS != 1 {
		// The rate was held, but not on one CPU.
		return notJudged, detail + "; but the server had more than one CPU (GOMAXPROCS is not 1)"
	}
	return pass, detail
}

// verdicts groups the classes by requirement: a requirement passes when every
// class it covers does.
func verdicts(r *Result) []verdict {
	var out []verdict
	for _, nfr := range []string{"NFR-1", "NFR-2", "NFR-3"} {
		v := verdict{nfr: nfr, outcome: pass}
		var parts []string
		for _, ph := range r.Phases {
			if ph.class.nfr != nfr {
				continue
			}
			o := phaseOutcome(ph)
			if o != pass && v.outcome == pass {
				v.outcome = o
			}
			parts = append(parts, fmt.Sprintf("%s: p95 %s ms", ph.class.label, ms(summarize(ph.server).p95)))
			if ph.errors > 0 {
				parts = append(parts, fmt.Sprintf("%d errors (%s)", ph.errors, ph.problem))
			}
			if ph.misfiled > 0 || ph.missingRecords > 0 {
				parts = append(parts, fmt.Sprintf("%d records misfiled, %d missing", ph.misfiled, ph.missingRecords))
			}
		}
		if len(parts) == 0 {
			continue
		}
		target := map[string]time.Duration{"NFR-1": targetHit, "NFR-2": targetMiss, "NFR-3": targetSearch}[nfr]
		v.detail = strings.Join(parts, "; ") + fmt.Sprintf(" (limit %d ms)", target.Milliseconds())
		out = append(out, v)
	}
	o, d := capacityOutcome(r)
	return append(out, verdict{nfr: "NFR-5", outcome: o, detail: d})
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	return summarize(d).p50
}

func durations(d []time.Duration) string {
	parts := make([]string, len(d))
	for i, v := range d {
		parts[i] = ms(v) + " ms"
	}
	return strings.Join(parts, ", ")
}

// writeReport prints the run's results, then the same results as Markdown
// rows for docs/perf.md. It returns whether every judged requirement passed
// and nothing went wrong in any class.
func writeReport(out io.Writer, r *Result) bool {
	o, m := r.Options, r.Machine
	qualifying := o.Links >= QualifyingLinks
	gomaxprocs := fmt.Sprint(o.GOMAXPROCS)
	if o.GOMAXPROCS < 0 {
		gomaxprocs = "unset (every core)"
	}

	fmt.Fprintf(out, "\nOgre performance run (plan L4.7)\n")
	if !qualifying {
		fmt.Fprintf(out, "\n*** DRY RUN: %d links. The requirements are stated at %d; these verdicts say the tool works, not that the product meets them. ***\n",
			o.Links, QualifyingLinks)
	}
	fmt.Fprintf(out, "\n  Directory  %d links, seed %d (generated in %v, written in %v; database %s)\n",
		o.Links, o.Seed, r.GenerateIn.Round(time.Millisecond), r.SeedIn.Round(time.Millisecond), mib(r.DBBytes))
	fmt.Fprintf(out, "  Binary     %s (%s)\n", o.Bin, r.OgreVer)
	fmt.Fprintf(out, "  Machine    %s; %s; %d cores; %s", m.OS, m.CPU, m.Cores, m.Memory)
	if m.Battery != "" {
		fmt.Fprintf(out, "; %s", m.Battery)
	}
	fmt.Fprintf(out, "\n  Build      %s; commit %s; %s\n", m.Go, m.Commit, m.Date)
	fmt.Fprintf(out, "  Server     GOMAXPROCS=%s, request log at level info\n", gomaxprocs)
	fmt.Fprintf(out, "  Client     net/http with keep-alive, %d in flight, %d requests per class after %d of warm-up\n",
		o.Concurrency, o.Requests, max(1, o.Requests/10))

	fmt.Fprintf(out, "\nServer time is the request log's duration (handler start to end); round trip is what the client saw. Milliseconds.\n\n")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintf(tw, "class\tn\tp50\tp95\tp99\tmax\t\trt p50\trt p95\trt p99\t\tlimit p95\tverdict\t\n")
	for _, ph := range r.Phases {
		s, rt := summarize(ph.server), summarize(ph.roundTrip)
		limit := "—"
		if ph.class.target > 0 {
			limit = fmt.Sprintf("%d %s", ph.class.target.Milliseconds(), ph.class.nfr)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t\t%s\t%s\t%s\t\t%s\t%s\t\n", ph.class.label, s.n,
			ms(s.p50), ms(s.p95), ms(s.p99), ms(s.max), ms(rt.p50), ms(rt.p95), ms(rt.p99), limit, phaseOutcome(ph))
	}
	_ = tw.Flush()
	for _, ph := range r.Phases {
		if ph.errors > 0 {
			fmt.Fprintf(out, "  %s: %d errors, for example %s\n", ph.class.label, ph.errors, ph.problem)
		}
		if ph.misfiled > 0 || ph.missingRecords > 0 {
			fmt.Fprintf(out, "  %s: %d log records of another kind of request, %d answered requests not in the log\n",
				ph.class.label, ph.misfiled, ph.missingRecords)
		}
	}

	fmt.Fprintf(out, "\nStartup to the first successful health check: %s (median %s ms)\n", durations(r.Startups), ms(median(r.Startups)))
	if r.RSSError != "" {
		fmt.Fprintf(out, "Memory: could not be read (%s)\n", r.RSSError)
	} else {
		fmt.Fprintf(out, "Memory (RSS): %s after startup, %s at most while serving (sampled every 250 ms), %s after the load\n",
			mib(r.RSSStart), mib(max(r.RSSStart, r.RSSPeak, r.RSSEnd)), mib(r.RSSEnd))
	}
	c := r.Capacity
	cpu := "unknown"
	if c.cpuShare >= 0 {
		cpu = fmt.Sprintf("%.1f%% of one core", 100*c.cpuShare)
	}
	fmt.Fprintf(out, "Fixed rate: %d/s for %v: %d sent, %.1f/s answered, %d errors, %d late; server CPU %s\n",
		c.rate, c.duration, c.sent, c.achieved, c.errors, c.late, cpu)
	fmt.Fprintf(out, "  server time p95: hits %s ms (n=%d), misses %s ms (n=%d); round trip p95 %s ms, p99 %s ms\n",
		ms(summarize(c.hitServer).p95), len(c.hitServer), ms(summarize(c.missServer).p95), len(c.missServer),
		ms(summarize(c.roundTrip).p95), ms(summarize(c.roundTrip).p99))
	fmt.Fprintf(out, "  from the scheduled send to the answer, p95: hits %s ms, misses %s ms; the phase took %v\n",
		ms(summarize(c.hitWait).p95), ms(summarize(c.missWait).p95), c.elapsed.Round(time.Millisecond))

	vs := verdicts(r)
	fmt.Fprintf(out, "\nRequirements")
	if !qualifying {
		fmt.Fprintf(out, " (dry run at %d links: not a verdict on the product)", o.Links)
	}
	fmt.Fprintln(out)
	passed := true
	for _, v := range vs {
		fmt.Fprintf(out, "  %-6s %-10s %s\n", v.nfr, v.outcome, v.detail)
		if v.outcome != pass && v.outcome != notJudged {
			passed = false
		}
	}
	for _, ph := range r.Phases {
		if o := phaseOutcome(ph); o == fail || o == incomplete {
			passed = false // the home page has no target but must still work
		}
	}

	writeMarkdown(out, r, vs)
	return passed
}

// writeMarkdown prints the rows of docs/perf.md's tables for this run.
func writeMarkdown(out io.Writer, r *Result, vs []verdict) {
	o, m := r.Options, r.Machine
	fmt.Fprintf(out, "\n----- Markdown for docs/perf.md -----\n\n")
	fmt.Fprintf(out, "| Field | Value |\n|---|---|\n")
	rows := [][2]string{
		{"Date", m.Date},
		{"Commit", m.Commit},
		{"Ogre", r.OgreVer},
		{"Machine", fmt.Sprintf("%s; %s; %d cores; %s", m.OS, m.CPU, m.Cores, m.Memory)},
		{"Power", m.Battery},
		{"Go", m.Go},
		{"Links", fmt.Sprintf("%d (seed %d), database %s", o.Links, o.Seed, mib(r.DBBytes))},
		{"Server", fmt.Sprintf("GOMAXPROCS=%d", o.GOMAXPROCS)},
		{"Client", fmt.Sprintf("%d in flight, %d requests per class, warm-up %d", o.Concurrency, o.Requests, max(1, o.Requests/10))},
	}
	if o.GOMAXPROCS < 0 {
		rows[7][1] = "GOMAXPROCS unset (every core)"
	}
	for _, row := range rows {
		fmt.Fprintf(out, "| %s | %s |\n", row[0], row[1])
	}

	fmt.Fprintf(out, "\n| Request | n | Server p50 | Server p95 | Server p99 | Round trip p95 | Limit (p95) | Verdict |\n")
	fmt.Fprintf(out, "|---|--:|--:|--:|--:|--:|---|---|\n")
	for _, ph := range r.Phases {
		s, rt := summarize(ph.server), summarize(ph.roundTrip)
		limit := "none"
		if ph.class.target > 0 {
			limit = fmt.Sprintf("%d ms (%s)", ph.class.target.Milliseconds(), ph.class.nfr)
		}
		fmt.Fprintf(out, "| %s | %d | %s ms | %s ms | %s ms | %s ms | %s | %s |\n",
			ph.class.label, s.n, ms(s.p50), ms(s.p95), ms(s.p99), ms(rt.p95), limit, phaseOutcome(ph))
	}

	c := r.Capacity
	fmt.Fprintf(out, "\n| Measure | Result |\n|---|---|\n")
	fmt.Fprintf(out, "| Startup to first health check | %s (median %s ms) |\n", durations(r.Startups), ms(median(r.Startups)))
	fmt.Fprintf(out, "| RSS after startup / peak / after load | %s / %s / %s |\n",
		mib(r.RSSStart), mib(max(r.RSSStart, r.RSSPeak, r.RSSEnd)), mib(r.RSSEnd))
	cpu := "unknown"
	if c.cpuShare >= 0 {
		cpu = fmt.Sprintf("%.1f%% of one core", 100*c.cpuShare)
	}
	fmt.Fprintf(out, "| Fixed rate %d/s for %v | %.1f/s answered, %d errors; server CPU %s; server p95 hits %s ms, misses %s ms; from the scheduled send p95 hits %s ms, misses %s ms |\n",
		c.rate, c.duration, c.achieved, c.errors, cpu, ms(summarize(c.hitServer).p95), ms(summarize(c.missServer).p95),
		ms(summarize(c.hitWait).p95), ms(summarize(c.missWait).p95))

	fmt.Fprintf(out, "\n| Requirement | Verdict | Detail |\n|---|---|---|\n")
	for _, v := range vs {
		fmt.Fprintf(out, "| %s | %s | %s |\n", v.nfr, v.outcome, v.detail)
	}
}
