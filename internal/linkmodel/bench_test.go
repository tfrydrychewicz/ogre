package linkmodel

import (
	"fmt"
	"testing"
)

// benchLink is a link at the limit of what can be saved: MaxRoutes routes,
// of every length and with every type, eight of them with a regular
// expression. Matching it is the most a single resolution can cost.
func benchLink(tb testing.TB) CompiledLink {
	tb.Helper()
	routes := benchRoutes()
	if len(routes) != MaxRoutes {
		tb.Fatalf("the benchmark link has %d routes, want %d", len(routes), MaxRoutes)
	}
	link, problems := ValidateDraft(Draft{Routes: routes})
	if problems.Count() != 0 {
		tb.Fatalf("the benchmark link does not validate: %+v", problems)
	}
	for i := range link.Routes {
		link.Routes[i].ID = int64(i + 1)
	}
	return Compile(link)
}

func benchRoutes() []DraftRoute {
	return []DraftRoute{
		{Pattern: "svc", Destination: "https://svc.example.com/"},
		{Pattern: "svc/new", Destination: "https://svc.example.com/new"},
		{Pattern: "svc/settings", Destination: "https://svc.example.com/settings"},
		{Pattern: "svc/{name}", Destination: "https://svc.example.com/s/{name}"},
		{Pattern: "svc/{id:int}", Destination: "https://svc.example.com/by-id/{id}"},
		{Pattern: "svc/{key:regex([A-Z]+-[0-9]+)}", Destination: "https://tracker.example.com/browse/{key}"},
		{Pattern: "svc/{sha:regex([0-9a-f]{40})}", Destination: "https://svc.example.com/commit/{sha}"},
		{Pattern: "svc/{name}/logs", Destination: "https://logs.example.com/?service={name}"},
		{Pattern: "svc/{name}/deploys", Destination: "https://deploy.example.com/{name}"},
		{Pattern: "svc/{name}/{env:regex(prod|staging|dev)}", Destination: "https://svc.example.com/s/{name}?env={env}"},
		{Pattern: "svc/{name}/{build:int}", Destination: "https://ci.example.com/{name}/builds/{build}"},
		{Pattern: "svc/{name}/{version:regex(v[0-9]+\\.[0-9]+\\.[0-9]+)}", Destination: "https://svc.example.com/s/{name}/releases/{version}"},
		{Pattern: "svc/{name}/{other}", Destination: "https://svc.example.com/s/{name}/{other}"},
		{Pattern: "svc/{name}/pr/{num:int}", Destination: "https://code.example.com/{name}/pull/{num}"},
		{Pattern: "svc/{name}/issue/{key:regex([A-Z]+-[0-9]+)}", Destination: "https://tracker.example.com/{name}/{key}"},
		{Pattern: "svc/{name}/metrics/{window:regex([0-9]+[smhd])}", Destination: "https://metrics.example.com/d/{name}?from=now-{window}"},
		{Pattern: "svc/{name}/cost/{month:regex(^(0?[1-9]|1[0-2])$)}", Destination: "https://cost.example.com/{name}?month={month}"},
		{Pattern: "svc/{name}/report/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", Destination: "https://reports.example.com/{name}/daily?month={month}&day={day}"},
		{Pattern: "svc/{name}/slo/{target:number}/{window:regex([0-9]+d)}", Destination: "https://slo.example.com/{name}?target={target}&window={window}"},
		{Pattern: "svc/{a}/{b}/{c}/{d}/{e}/{f}", Destination: "https://svc.example.com/{a}/{b}/{c}/{d}/{e}/{f}"},
	}
}

// The benchmark link and the requests below must do what the benchmarks
// assume, or the numbers measure something else.
func TestBenchLink(t *testing.T) {
	c := benchLink(t)
	for _, tt := range []struct {
		segments []string
		pattern  string // the route that must win; "" for a miss
		dest     string
	}{
		{benchHitLiteral, "svc/settings", "https://svc.example.com/settings"},
		{benchHitRegex, "svc/{name}/report/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "https://reports.example.com/billing-api/daily?month=10&day=13"},
		{benchHitContested, "svc/{name}/{version:regex(v[0-9]+\\.[0-9]+\\.[0-9]+)}", "https://svc.example.com/s/billing-api/releases/v1.24.3"},
		{benchMiss, "", ""},
	} {
		m, ok, reasons, err := c.Match(tt.segments)
		if err != nil || ok != (tt.pattern != "") || m.Route.Pattern != tt.pattern || m.Destination != tt.dest {
			t.Errorf("Match(%q) = %q -> %q (%v, %v); want %q -> %q", tt.segments, m.Route.Pattern, m.Destination, ok, err, tt.pattern, tt.dest)
		}
		if !ok && len(reasons) != MaxRoutes {
			t.Errorf("Match(%q) gave %d reasons, want %d", tt.segments, len(reasons), MaxRoutes)
		}
	}
}

var (
	benchHitLiteral = []string{"svc", "settings"}
	// Five segments, a regex and an int, two placeholders in a query.
	benchHitRegex = []string{"svc", "billing-api", "report", "10", "13"}
	// Three segments that four routes are tried for and two match.
	benchHitContested = []string{"svc", "billing-api", "v1.24.3"}
	// The right length for one route, whose regex refuses the month.
	benchMiss = []string{"svc", "billing-api", "report", "13", "01"}
)

// BenchmarkMatchHit measures PRD 5.5 steps 3 to 5 for one request against a
// link with 20 routes: finding the candidates, choosing the most specific
// and expanding the destination.
func BenchmarkMatchHit(b *testing.B) {
	c := benchLink(b)
	for _, bench := range []struct {
		name     string
		segments []string
	}{
		{"literal", benchHitLiteral},
		{"regex", benchHitRegex},
		{"contested", benchHitContested},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok, _, err := c.Match(bench.segments); !ok || err != nil {
					b.Fatal("the request did not match")
				}
			}
		})
	}
}

// BenchmarkMatchMissKnown measures a request that fits none of 20 routes,
// including the reason written for every one of them.
func BenchmarkMatchMissKnown(b *testing.B) {
	c := benchLink(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, reasons, _ := c.Match(benchMiss); ok || len(reasons) != MaxRoutes {
			b.Fatal("the request did not miss")
		}
	}
}

// BenchmarkCompile measures preparing one link for the index, which happens
// for every link at startup and for one link after each save.
func BenchmarkCompile(b *testing.B) {
	link := benchLink(b).Link
	b.ReportAllocs()
	for b.Loop() {
		if c := Compile(link); len(c.routes) != MaxRoutes {
			b.Fatal(fmt.Sprint("compiled ", len(c.routes), " routes"))
		}
	}
}
