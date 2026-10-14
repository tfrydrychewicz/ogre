package perf

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseRequestRecord(t *testing.T) {
	for _, tc := range []struct {
		line string
		want requestRecord
		ok   bool
	}{
		{`time=2026-10-14T16:16:18.703+02:00 level=INFO msg=request method=GET status=302 duration=152.583µs area=resolver decision=hit link=jira`,
			requestRecord{status: 302, duration: 152583 * time.Nanosecond, area: "resolver", decision: "hit"}, true},
		{`time=2026-10-14T16:16:18.703+02:00 level=INFO msg=request method=GET status=200 duration=1.5ms area=internal route=/-/{$}`,
			requestRecord{status: 200, duration: 1500 * time.Microsecond, area: "internal", route: "/-/{$}"}, true},
		// A quoted value with a space and an escaped quote does not upset
		// the fields after it.
		{`time=x level=INFO msg=request method=GET status=404 link="a \"b\" c" duration=2s area=resolver decision=miss_unknown`,
			requestRecord{status: 404, duration: 2 * time.Second, area: "resolver", decision: "miss_unknown"}, true},
		{`time=x level=INFO msg=request status=200 duration=4100 area=internal`,
			requestRecord{status: 200, duration: 4100, area: "internal"}, true},
		{`time=x level=INFO msg="ogre is starting" version=0.1.0 url=http://127.0.0.1:1`, requestRecord{}, false},
		{`time=x level=INFO msg=request status=abc duration=1ms`, requestRecord{}, false},
		{`not a log line`, requestRecord{}, false},
		{`msg=request status=200 duration="unterminated`, requestRecord{}, false},
	} {
		got, ok := parseRequestRecord(tc.line)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseRequestRecord(%q) = %+v, %v; want %+v, %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSummarize(t *testing.T) {
	var d []time.Duration
	for i := 100; i >= 1; i-- { // unsorted on purpose
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	s := summarize(d)
	if s.n != 100 || s.p50 != 50*time.Millisecond || s.p95 != 95*time.Millisecond || s.p99 != 99*time.Millisecond || s.max != 100*time.Millisecond {
		t.Fatalf("summarize(1..100 ms) = %+v", s)
	}
	if d[0] != 100*time.Millisecond {
		t.Fatal("summarize sorted its argument")
	}
	// Nearest rank: of 3 values, the 95th percentile is the largest.
	if s := summarize([]time.Duration{1, 2, 3}); s.p95 != 3 || s.p50 != 2 {
		t.Fatalf("summarize(1,2,3) = %+v", s)
	}
	// Nearest rank, not rounding to the nearest rank: of 12 values, 95% is
	// 11.4 values, so the 95th percentile is the 12th, where rounding would
	// pick the 11th.
	var d12 []time.Duration
	for i := 1; i <= 12; i++ {
		d12 = append(d12, time.Duration(i))
	}
	if s := summarize(d12); s.p50 != 6 || s.p95 != 12 || s.p99 != 12 {
		t.Fatalf("summarize(1..12) = %+v", s)
	}
	var d1000 []time.Duration
	for i := 1000; i >= 1; i-- {
		d1000 = append(d1000, time.Duration(i))
	}
	if s := summarize(d1000); s.p50 != 500 || s.p95 != 950 || s.p99 != 990 || s.max != 1000 {
		t.Fatalf("summarize(1..1000) = %+v", s)
	}
	if s := summarize([]time.Duration{7}); s.n != 1 || s.p50 != 7 || s.p95 != 7 || s.p99 != 7 || s.max != 7 {
		t.Fatalf("summarize(7) = %+v", s)
	}
	if s := summarize(nil); s != (summary{}) {
		t.Fatalf("summarize(nil) = %+v", s)
	}
}

func TestParsePSTime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:01.23":    1230 * time.Millisecond,
		"12:03.50":   12*time.Minute + 3500*time.Millisecond,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"1-01:02:03": 25*time.Hour + 2*time.Minute + 3*time.Second,
	} {
		if got, err := parsePSTime(in); err != nil || got != want {
			t.Errorf("parsePSTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "12", "a:b", "1:2:3:4"} {
		if _, err := parsePSTime(bad); err == nil {
			t.Errorf("parsePSTime(%q) succeeded", bad)
		}
	}
}

// TestOwnProcessStats reads this test process's memory and CPU time the way
// the run reads the server's.
func TestOwnProcessStats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the run reads no process statistics on Windows")
	}
	rss, err := residentBytes(os.Getpid())
	if err != nil || rss < 1<<20 {
		t.Fatalf("residentBytes = %d, %v", rss, err)
	}
	if _, err := cpuTime(os.Getpid()); err != nil {
		t.Fatal(err)
	}
}

func TestServerEnvDropsOgreSettings(t *testing.T) {
	t.Setenv("OGRE_DB", "/somewhere/else.db")
	t.Setenv("OGRE_ADDR", "127.0.0.1:4655")
	t.Setenv("GOMAXPROCS", "8")
	env := strings.Join(serverEnv(1), "\n")
	if strings.Contains(env, "OGRE_") || strings.Contains(env, "GOMAXPROCS=8") || !strings.Contains(env, "GOMAXPROCS=1") {
		t.Fatalf("server environment:\n%s", env)
	}
	if strings.Contains(strings.Join(serverEnv(-1), "\n"), "GOMAXPROCS") {
		t.Fatal("GOMAXPROCS was set although -1 asks to leave it unset")
	}
}

func TestFreeAddrIsLoopbackAndNotTheDefaultPort(t *testing.T) {
	addr, err := freeAddr()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") || strings.HasSuffix(addr, ":4655") {
		t.Fatalf("freeAddr() = %q", addr)
	}
}

// TestRun is the whole run, small: it builds the real binary, generates a
// small Directory, and measures it. It proves that the tool and the server
// agree on what is measured: every request is answered as its class
// expects and found in the request log under that class.
func TestRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the ogre binary")
	}
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ogre")
	if runtime.GOOS == "windows" {
		t.Skip("the run stops the server with an interrupt, which Windows does not deliver")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH to build the binary with")
	}
	build := exec.Command(goTool, "build", "-o", bin, "github.com/tfrydrychewicz/ogre/cmd/ogre")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out bytes.Buffer
	passed, err := Run(ctx, Options{
		Bin: bin, Links: 400, Requests: 40, Rate: 50, RateFor: time.Second, StartupRuns: 2,
		Dir: filepath.Join(dir, "run"),
	}, &out)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !passed {
		t.Fatalf("the run did not pass:\n%s", out.String())
	}
	report := out.String()
	for _, want := range []string{
		"DRY RUN: 400 links",
		"| Hit (302) | 40 |",
		"| Miss page, unknown name | 40 |",
		"| Search API, two words | 40 |",
		"| Home page | 40 |",
		"Fixed rate: 50/s for 1s: 50 sent",
		"| NFR-5 | PASS |",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	// The wait from the scheduled send is measured, and holds at least the
	// round trip, which over HTTP is never below a microsecond.
	m := regexp.MustCompile(`from the scheduled send to the answer, p95: hits ([0-9.]+) ms, misses ([0-9.]+) ms`).FindStringSubmatch(report)
	if m == nil {
		t.Errorf("the report has no waits from the scheduled send:\n%s", report)
	} else if hits, _ := strconv.ParseFloat(m[1], 64); hits < 0.001 {
		t.Errorf("hit wait p95 %s ms: not measured", m[1])
	} else if misses, _ := strconv.ParseFloat(m[2], 64); misses < 0.001 {
		t.Errorf("miss wait p95 %s ms: not measured", m[2])
	}
	// The server was stopped: its log ends with its own goodbye.
	log, err := os.ReadFile(filepath.Join(dir, "run", "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), `msg="ogre has stopped"`); got != 2 {
		t.Errorf("the log records %d clean stops, want 2", got)
	}
	// Each class is warmed up with a tenth of its requests, uncounted:
	// hits are 4 of warm-up, 40 measured, and 40 of the 50 at a fixed rate.
	if got := strings.Count(string(log), " decision=hit"); got != 4+40+40 {
		t.Errorf("the log records %d hits, want %d", got, 4+40+40)
	}
}
