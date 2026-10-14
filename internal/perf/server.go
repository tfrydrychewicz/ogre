package perf

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultPort is the port of a real Directory (plan section 3.1). The run
// never uses it: a Directory the user has running may hold it, and a load
// test must not reach that one.
const defaultPort = 4655

// server is one ogre process started for the run, and what it has logged.
type server struct {
	cmd     *exec.Cmd
	baseURL string
	started time.Time

	mu      sync.Mutex
	records []requestRecord
	// notes are the log lines that are not request records, for the
	// report when something goes wrong.
	notes []string
	// changed is closed and replaced each time a record arrives.
	changed chan struct{}

	logDone chan struct{} // closed when the log has been read to its end
	exited  chan struct{} // closed when the process has exited
	waitErr error         // set before exited is closed
}

// freeAddr returns a loopback address with a port no one is listening on.
// Another program could take the port before the server binds it; the server
// then refuses to start and the run says so.
func freeAddr() (string, error) {
	for range 10 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		addr := l.Addr().String()
		_ = l.Close()
		if _, port, _ := net.SplitHostPort(addr); port != strconv.Itoa(defaultPort) {
			return addr, nil
		}
	}
	return "", errors.New("perf: found no free port")
}

// serverEnv is this process's environment with nothing that could point the
// server elsewhere: every OGRE_ variable is dropped, so only the flags the
// run passes decide the address and the database. GOMAXPROCS is set when
// gomaxprocs is above zero.
func serverEnv(gomaxprocs int) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "OGRE_") || strings.HasPrefix(kv, "GOMAXPROCS=") {
			continue
		}
		env = append(env, kv)
	}
	if gomaxprocs > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(gomaxprocs))
	}
	return env
}

// startServer runs bin against the database at dbPath on addr. Everything
// the server logs is appended to logPath and request records are parsed as
// they arrive. The process is ended by stop, or by ctx.
func startServer(ctx context.Context, bin, dbPath, addr string, gomaxprocs int, logPath string) (*server, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "serve", "--addr", addr, "--db", dbPath, "--log-level", "info")
	cmd.Env = serverEnv(gomaxprocs)
	// On cancellation (Ctrl-C of the run) ask for a graceful stop, as stop
	// does, and kill only if that does not end the process.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}

	s := &server{
		cmd:     cmd,
		baseURL: "http://" + addr,
		changed: make(chan struct{}),
		logDone: make(chan struct{}),
		exited:  make(chan struct{}),
	}
	s.started = time.Now()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("perf: start %s: %w", bin, err)
	}
	go func() {
		defer close(s.logDone)
		defer logFile.Close()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			_, _ = fmt.Fprintln(logFile, line)
			rec, ok := parseRequestRecord(line)
			s.mu.Lock()
			if ok {
				s.records = append(s.records, rec)
				close(s.changed)
				s.changed = make(chan struct{})
			} else {
				s.notes = append(s.notes, line)
			}
			s.mu.Unlock()
		}
	}()
	go func() {
		// The pipe must be read to its end before Wait closes it.
		<-s.logDone
		s.waitErr = cmd.Wait()
		close(s.exited)
	}()
	return s, nil
}

func (s *server) pid() int { return s.cmd.Process.Pid }

// waitHealthy polls the health check until it answers "ok" and returns the
// time from starting the process to that answer: the startup time the run
// reports. Each poll uses a new connection, as a browser arriving would.
func (s *server) waitHealthy(ctx context.Context, limit time.Duration) (time.Duration, error) {
	client := &http.Client{Timeout: limit, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(limit)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/-/healthz", nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "ok" {
				return time.Since(s.started), nil
			}
			return 0, fmt.Errorf("perf: the health check answered %d %q", resp.StatusCode, body)
		}
		select {
		case <-s.exited:
			return 0, fmt.Errorf("perf: the server exited before it answered (%v); its log:\n%s", s.waitErr, s.logTail(20))
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("perf: the server did not answer within %v; its log:\n%s", limit, s.logTail(20))
		}
	}
}

// recordCount is the number of request records read so far.
func (s *server) recordCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// waitRecords waits until n request records have been read, or until limit
// has passed. A request's record is written after its response, so the
// client can finish a phase before the last records arrive.
func (s *server) waitRecords(n int, limit time.Duration) bool {
	timeout := time.After(limit)
	for {
		s.mu.Lock()
		have, changed := len(s.records), s.changed
		s.mu.Unlock()
		if have >= n {
			return true
		}
		select {
		case <-changed:
		case <-s.logDone:
			return s.recordCount() >= n
		case <-timeout:
			return false
		}
	}
}

// recordsBetween returns a copy of records [from, to).
func (s *server) recordsBetween(from, to int) []requestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	to = min(to, len(s.records))
	if from >= to {
		return nil
	}
	return append([]requestRecord(nil), s.records[from:to]...)
}

// logTail returns the last n lines the server logged that are not request
// records.
func (s *server) logTail(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes := s.notes[max(0, len(s.notes)-n):]
	return "  " + strings.Join(notes, "\n  ")
}

// stop asks the server to shut down gracefully, as Ctrl-C does, and kills it
// if it has not exited after limit. It returns once the process is gone.
func (s *server) stop(limit time.Duration) error {
	select {
	case <-s.exited:
		return s.exitError()
	default:
	}
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.exited:
		return s.exitError()
	case <-time.After(limit):
		_ = s.cmd.Process.Kill()
		<-s.exited
		return fmt.Errorf("perf: the server did not stop within %v and was killed", limit)
	}
}

// exitError is the process's exit, once it has exited: nil for a clean stop.
func (s *server) exitError() error {
	if s.waitErr != nil {
		return fmt.Errorf("perf: the server exited with %v; its log:\n%s", s.waitErr, s.logTail(20))
	}
	return nil
}
