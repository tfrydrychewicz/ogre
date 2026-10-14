package perf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRateFor is how long the fixed-rate phase lasts by default.
const DefaultRateFor = 30 * time.Second

// QualifyingLinks is the size of Directory the PRD's performance
// requirements are stated at. A run with fewer links proves the tool works;
// only a run at this size can say whether the product meets them.
const QualifyingLinks = 100_000

// Options configure a run. The zero value of a field means its default.
type Options struct {
	// Bin is the ogre binary to measure. It is started as is; build it
	// first (make build).
	Bin string
	// Links is how many links to generate (QualifyingLinks).
	Links int
	// Seed picks the generated Directory; the same seed gives the same
	// links and requests (1).
	Seed uint64
	// Requests is how many requests each class sends (2000), after a
	// warm-up of a tenth as many that is not counted.
	Requests int
	// Concurrency is how many requests of a class are in flight at once
	// (1: one person using the Directory, so server time is the time the
	// request itself takes, not time spent queued behind others).
	Concurrency int
	// GOMAXPROCS is given to the server (1: the one vCPU of NFR-5).
	// Negative leaves it unset, so the server uses every core.
	GOMAXPROCS int
	// Rate and RateFor are the fixed-rate phase of NFR-5: requests per
	// second (200) and for how long (30s).
	Rate    int
	RateFor time.Duration
	// StartupRuns is how many times the server is started to measure
	// startup (3). The last start serves the load.
	StartupRuns int
	// Dir holds the database and the server's log. Empty means a new
	// temporary directory, removed afterwards unless Keep is set.
	Dir  string
	Keep bool
}

func (o Options) withDefaults() Options {
	def := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	def(&o.Links, QualifyingLinks)
	def(&o.Requests, 2000)
	def(&o.Concurrency, 1)
	def(&o.GOMAXPROCS, 1)
	def(&o.Rate, 200)
	def(&o.StartupRuns, 3)
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.RateFor == 0 {
		o.RateFor = DefaultRateFor
	}
	if o.Bin == "" {
		o.Bin = filepath.Join("bin", "ogre")
	}
	return o
}

// Result is everything a run measured, for the report.
type Result struct {
	Options    Options
	Machine    Machine
	OgreVer    string
	GenerateIn time.Duration
	SeedIn     time.Duration
	DBBytes    int64
	Startups   []time.Duration
	Phases     []phase
	Capacity   capacity
	// Memory of the server process: right after its startup (all links
	// loaded, nothing served yet), the most seen while it served the load,
	// and after the load.
	RSSStart, RSSPeak, RSSEnd int64
	RSSError                  string
	Dir                       string
}

// Run generates the Directory, starts the server, measures it and writes the
// report to out. It returns whether every requirement it judged was met; err
// is for a run that could not be completed.
func Run(ctx context.Context, opts Options, out io.Writer) (passed bool, err error) {
	opts = opts.withDefaults()
	if opts.Concurrency < 1 || opts.Requests < 1 || opts.Rate < 1 || opts.StartupRuns < 1 || opts.Links < 1 {
		return false, errors.New("perf: links, requests, concurrency, rate and startup runs must be at least 1")
	}
	bin, err := filepath.Abs(opts.Bin)
	if err != nil {
		return false, err
	}
	if info, err := os.Stat(bin); err != nil || info.IsDir() {
		return false, fmt.Errorf("perf: no ogre binary at %s; build it first (make build)", opts.Bin)
	}
	res := &Result{Options: opts, Machine: describeMachine()}
	if v, err := exec.CommandContext(ctx, bin, "version").Output(); err == nil {
		res.OgreVer = strings.TrimSpace(string(v))
	}

	dir := opts.Dir
	if dir == "" {
		if dir, err = os.MkdirTemp("", "ogre-perf-"); err != nil {
			return false, err
		}
		if !opts.Keep {
			defer os.RemoveAll(dir)
		}
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	res.Dir = dir
	dbPath := filepath.Join(dir, "ogre.db")
	if _, err := os.Stat(dbPath); err == nil {
		return false, fmt.Errorf("perf: %s exists; give a new or empty directory", dbPath)
	}
	logPath := filepath.Join(dir, "server.log")

	progress := func(format string, args ...any) { fmt.Fprintf(out, "· "+format+"\n", args...) }

	progress("generating %d links (seed %d)", opts.Links, opts.Seed)
	owner, err := LocalOwner(ctx, dbPath)
	if err != nil {
		return false, err
	}
	t := time.Now()
	gen, err := Generate(opts.Links, opts.Seed, owner)
	if err != nil {
		return false, err
	}
	res.GenerateIn = time.Since(t)
	t = time.Now()
	if err := Seed(ctx, dbPath, gen.Links); err != nil {
		return false, err
	}
	res.SeedIn = time.Since(t)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil {
			res.DBBytes += info.Size()
		}
	}
	gen.Links = nil // the server holds them now; let the run's own memory go

	var srv *server
	defer func() {
		if srv != nil {
			_ = srv.stop(15 * time.Second)
		}
	}()
	for i := 0; i < opts.StartupRuns; i++ {
		addr, err := freeAddr()
		if err != nil {
			return false, err
		}
		progress("startup %d of %d: %s serve --addr %s", i+1, opts.StartupRuns, opts.Bin, addr)
		srv, err = startServer(ctx, bin, dbPath, addr, opts.GOMAXPROCS, logPath)
		if err != nil {
			return false, err
		}
		d, err := srv.waitHealthy(ctx, 2*time.Minute)
		if err != nil {
			return false, err
		}
		res.Startups = append(res.Startups, d)
		if i < opts.StartupRuns-1 {
			err := srv.stop(15 * time.Second)
			srv = nil
			if err != nil {
				return false, err
			}
		}
	}

	if b, err := residentBytes(srv.pid()); err == nil {
		res.RSSStart = b
	} else {
		res.RSSError = err.Error()
	}
	sampler := sampleRSS(srv.pid(), 250*time.Millisecond)

	client := newClient(opts.Concurrency)
	// The successful health check is the one request the server has
	// answered so far: the polls before it found no listener.
	l := &ledger{srv: srv, answered: 1}
	cls := classes(gen)
	warm := max(1, opts.Requests/10)
	for i := range cls {
		progress("%-24s warm-up %d, then %d requests, %d at a time", cls[i].label, warm, opts.Requests, opts.Concurrency)
		runClosed(ctx, client, l, &cls[i], warm, opts.Concurrency)
		res.Phases = append(res.Phases, runClosed(ctx, client, l, &cls[i], opts.Requests, opts.Concurrency))
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}
	progress("fixed rate: %d requests a second for %v (8 hits : 1 known-name miss : 1 unknown-name miss)", opts.Rate, opts.RateFor)
	res.Capacity = runRate(ctx, newClient(64), l, cls, opts.Rate, opts.RateFor)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	peak, perr := sampler.finish()
	if perr == nil {
		res.RSSPeak = peak
	}
	if b, err := residentBytes(srv.pid()); err == nil {
		res.RSSEnd = b
	}
	stopErr := srv.stop(15 * time.Second)
	srv = nil
	if stopErr != nil {
		return false, stopErr
	}

	passed = writeReport(out, res)
	if opts.Keep || opts.Dir != "" {
		fmt.Fprintf(out, "\nThe database and the server's log are kept in %s\n", dir)
	}
	return passed, nil
}
