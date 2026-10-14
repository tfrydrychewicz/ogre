// Command ogre-perf is the performance run of plan task L4.7. It generates a
// Directory of realistic links in a temporary database, starts the ogre
// binary against it on a free loopback port, and measures it over HTTP:
// server time at p50, p95 and p99 for hits, miss pages and search, the home
// page, memory, startup time, and a fixed rate of resolutions (NFR-1, NFR-2,
// NFR-3, NFR-5). docs/perf.md describes the method and records the results.
//
// It is a development tool, not part of the shipped binary:
//
//	make perf                        # 100,000 links, the qualifying run
//	make perf PERF_ARGS="-links 10000 -rate-for 10s"   # a quick dry run
//
// It never uses port 4655 or the user's data directory: the database lives
// in a new temporary directory, removed afterwards unless -keep is given.
// The exit status is 0 when every requirement it judged passed, 1 when one
// did not, and 2 when the run itself failed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/tfrydrychewicz/ogre/internal/perf"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var o perf.Options
	fs := flag.NewFlagSet("ogre-perf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.Bin, "bin", "bin/ogre", "the ogre binary to measure (build it with make build)")
	fs.IntVar(&o.Links, "links", perf.QualifyingLinks, "how many links to generate; the requirements are stated at 100000")
	fs.Uint64Var(&o.Seed, "seed", 1, "picks the generated links and requests; the same seed gives the same ones")
	fs.IntVar(&o.Requests, "requests", 2000, "requests per class, after a warm-up of a tenth as many")
	fs.IntVar(&o.Concurrency, "concurrency", 1, "requests of a class in flight at once")
	fs.IntVar(&o.GOMAXPROCS, "gomaxprocs", 1, "GOMAXPROCS of the server: 1 is NFR-5's one vCPU; -1 leaves it unset")
	fs.IntVar(&o.Rate, "rate", 200, "requests per second of the fixed-rate phase (NFR-5)")
	fs.DurationVar(&o.RateFor, "rate-for", perf.DefaultRateFor, "how long the fixed-rate phase lasts")
	fs.IntVar(&o.StartupRuns, "startup-runs", 3, "how many times the server is started to measure startup")
	fs.StringVar(&o.Dir, "dir", "", "keep the database and the server's log in this new directory instead of a temporary one")
	fs.BoolVar(&o.Keep, "keep", false, "keep the temporary directory with the database and the server's log")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "ogre-perf: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if o.GOMAXPROCS == 0 {
		fmt.Fprintln(stderr, "ogre-perf: -gomaxprocs 0 is ambiguous; give 1 or more, or -1 to leave it unset")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	passed, err := perf.Run(ctx, o, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "ogre-perf: %v\n", err)
		return 2
	}
	if !passed {
		fmt.Fprintln(stderr, "ogre-perf: at least one requirement was not met, or a request was not answered as expected")
		return 1
	}
	return 0
}
