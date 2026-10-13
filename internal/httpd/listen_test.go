package httpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
)

// freePort returns a port that was free a moment ago on both loopback
// addresses: "localhost" listens on both, and a port taken on [::1] alone is
// a refusal to start.
func freePort(t *testing.T) string {
	t.Helper()
	for range 50 {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		free := true
		if hasIPv6Loopback() {
			v6, err := net.Listen("tcp6", "[::1]:"+port)
			if err != nil {
				free = false
			} else {
				v6.Close()
			}
		}
		ln.Close()
		if free {
			return port
		}
	}
	t.Fatal("found no port free on both loopback addresses")
	return ""
}

// hasIPv6Loopback reports whether this machine can listen on [::1].
func hasIPv6Loopback() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// testClient talks to exactly the address in the URL: no proxy, no reused
// connections that would outlive a server, no followed redirects.
func testClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startServer runs ListenAndServe in the background and waits until it
// answers. The returned function stops it and returns ListenAndServe's
// result.
func startServer(t *testing.T, s *Server, port string) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()

	stopped := false
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("ListenAndServe did not return after its context was cancelled")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })

	client := testClient()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("ListenAndServe returned early: %v", err)
		default:
		}
		resp, err := client.Get("http://127.0.0.1:" + port + "/-/healthz")
		if err == nil {
			resp.Body.Close()
			return stop
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not come up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertPortFree(t *testing.T, port string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:"+port)
	if err != nil {
		t.Errorf("port %s is still in use after shutdown: %v", port, err)
		return
	}
	ln.Close()
}

func TestListenAndServe(t *testing.T) {
	port := freePort(t)
	addr := "localhost:" + port
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionHit, LinkName: "roadmap", Destination: "https://docs.example.com/roadmap"}
	}}
	logger, logs := captureLog()
	server := New(svc, Options{Addr: addr, Version: testVersion, Actor: testActor, Logger: logger})
	stop := startServer(t, server, port)
	client := testClient()

	t.Run("serves on both loopback addresses", func(t *testing.T) {
		hosts := []string{"127.0.0.1:" + port, "localhost:" + port}
		if hasIPv6Loopback() {
			hosts = append(hosts, "[::1]:"+port)
		} else {
			t.Log("no IPv6 loopback on this machine; [::1] not checked")
		}
		for _, host := range hosts {
			resp, err := client.Get("http://" + host + "/-/api/v1/meta")
			if err != nil {
				t.Errorf("%s: %v", host, err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := decodeJSON(t, `{"product":"ogre","version":"9.9.9-test","api":1}`)
			if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(decodeJSON(t, string(body)), want) {
				t.Errorf("%s: status %d, body %s", host, resp.StatusCode, body)
			}
		}
	})

	t.Run("a go link redirects", func(t *testing.T) {
		resp, err := client.Get("http://127.0.0.1:" + port + "/roadmap")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://docs.example.com/roadmap" {
			t.Errorf("status %d, Location %q", resp.StatusCode, resp.Header.Get("Location"))
		}
	})

	// net/http itself must not clean the path either. A Go client would
	// normalize the URL, so this speaks HTTP by hand.
	t.Run("the raw path survives a real connection", func(t *testing.T) {
		before := len(svc.resolvedPaths())
		conn, err := net.DialTimeout("tcp4", "127.0.0.1:"+port, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprintf(conn, "GET //a/../b%%2Fc/./d%%20e?x=/../y HTTP/1.1\r\nHost: localhost:%s\r\nConnection: close\r\n\r\n", port)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Errorf("status = %d, want the resolver's 302", resp.StatusCode)
		}
		got := svc.resolvedPaths()[before:]
		if want := []string{"//a/../b%2Fc/./d%20e"}; !reflect.DeepEqual(got, want) {
			t.Errorf("resolved %q, want %q", got, want)
		}
	})

	t.Run("a hostile Host is refused over TCP", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/-/api/v1/links", nil)
		req.Host = "evil.example:" + port
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("a second ogre on the port is told so", func(t *testing.T) {
		second := New(&fakeService{}, Options{Addr: addr, Version: "second"})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := second.ListenAndServe(ctx)
		if err == nil {
			t.Fatal("second server started on a port that is taken")
		}
		if !strings.Contains(err.Error(), "another ogre") || !strings.Contains(err.Error(), testVersion) {
			t.Errorf("error does not say another ogre (version %s) holds the port: %v", testVersion, err)
		}
		if !isAddrInUse(err) {
			t.Errorf("error does not wrap EADDRINUSE: %v", err)
		}
	})

	t.Run("cancelling the context shuts down cleanly", func(t *testing.T) {
		if err := stop(); err != nil {
			t.Errorf("ListenAndServe = %v, want nil", err)
		}
		assertPortFree(t, port)
		if !strings.Contains(logs.String(), `"msg":"listening"`) {
			t.Errorf("the listen address was not logged:\n%s", logs.String())
		}
	})
}

func TestListenAndServeWaitsForRequestsInFlight(t *testing.T) {
	port := freePort(t)
	entered, release := make(chan struct{}), make(chan struct{})
	svc := &fakeService{resolveFn: func(string) app.Decision {
		close(entered)
		<-release
		return app.Decision{Kind: app.DecisionHit, LinkName: "slow", Destination: "https://docs.example.com/slow"}
	}}
	server := New(svc, Options{Addr: "127.0.0.1:" + port})
	// startServer's readiness check goes to /-/healthz, not the resolver.
	stop := startServer(t, server, port)

	type result struct {
		status int
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := testClient().Get("http://127.0.0.1:" + port + "/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		resp.Body.Close()
		got <- result{status: resp.StatusCode}
	}()

	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()

	select {
	case err := <-stopped:
		t.Fatalf("shutdown did not wait for the request in flight (returned %v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)

	if r := <-got; r.err != nil || r.status != http.StatusFound {
		t.Errorf("request in flight: status %d, error %v; want 302", r.status, r.err)
	}
	if err := <-stopped; err != nil {
		t.Errorf("ListenAndServe = %v, want nil", err)
	}
	assertPortFree(t, port)
}

func TestListenOnPortHeldByAnotherProgram(t *testing.T) {
	respond := func(status int, contentType, body string) func(net.Conn) {
		return func(c net.Conn) {
			_, _ = http.ReadRequest(bufio.NewReader(c))
			fmt.Fprintf(c, "HTTP/1.1 %d X\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
				status, contentType, len(body), body)
		}
	}
	tests := []struct {
		name   string
		answer func(net.Conn)
	}{
		{"a web server without the endpoint", respond(404, "text/plain", "not found")},
		{"a web server answering HTML", respond(200, "text/html", "<html>hello</html>")},
		{"another product's meta", respond(200, "application/json", `{"product":"something-else","version":"1","api":1}`)},
		{"JSON without a product", respond(200, "application/json", `{}`)},
		{"ogre's name behind an error status", respond(500, "application/json", `{"product":"ogre","version":"1","api":1}`)},
		{"a redirect", respond(302, "text/plain", "")},
		{"not HTTP at all", func(c net.Conn) { _, _ = c.Write([]byte("SSH-2.0-OpenSSH\r\n")) }},
		{"closes at once", func(net.Conn) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					_ = c.SetDeadline(time.Now().Add(5 * time.Second))
					tt.answer(c)
					c.Close()
				}
			}()
			_, port, _ := net.SplitHostPort(ln.Addr().String())

			for _, addr := range []string{"localhost:" + port, "127.0.0.1:" + port} {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := New(&fakeService{}, Options{Addr: addr}).ListenAndServe(ctx)
				cancel()
				if err == nil {
					t.Fatalf("%s: started on a port that is taken", addr)
				}
				if !strings.Contains(err.Error(), "another program") || strings.Contains(err.Error(), "another ogre") {
					t.Errorf("%s: error does not say another program holds the port: %v", addr, err)
				}
				if !isAddrInUse(err) {
					t.Errorf("%s: error does not wrap EADDRINUSE: %v", addr, err)
				}
			}
		})
	}
}

// A holder that accepts connections and never answers must not hang startup.
func TestListenProbeTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(held)
				return
			}
			held <- c // keep it open, say nothing
		}
	}()
	defer func() {
		ln.Close()
		for c := range held {
			c.Close()
		}
	}()

	start := time.Now()
	refused, cancelRefused := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRefused()
	err = New(&fakeService{}, Options{Addr: ln.Addr().String()}).ListenAndServe(refused)
	if err == nil || !strings.Contains(err.Error(), "another program") {
		t.Errorf("error = %v, want one saying another program holds the port", err)
	}
	if elapsed := time.Since(start); elapsed > probeTimeout+3*time.Second {
		t.Errorf("gave up after %v, want about %v", elapsed, probeTimeout)
	}
}

func TestListenAddressForms(t *testing.T) {
	t.Run("an IPv4 address listens on that address only", func(t *testing.T) {
		port := freePort(t)
		stop := startServer(t, New(&fakeService{}, Options{Addr: "127.0.0.1:" + port}), port)
		if hasIPv6Loopback() {
			if conn, err := net.DialTimeout("tcp6", "[::1]:"+port, time.Second); err == nil {
				conn.Close()
				t.Errorf("also listening on [::1]:%s", port)
			}
		}
		if err := stop(); err != nil {
			t.Errorf("ListenAndServe = %v, want nil", err)
		}
		assertPortFree(t, port)
	})

	for _, addr := range []string{"", "localhost", "no-port", "localhost:not-a-port", "localhost:99999"} {
		t.Run("invalid address "+addr, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := New(&fakeService{}, Options{Addr: addr}).ListenAndServe(ctx); err == nil {
				t.Errorf("ListenAndServe(%q) = nil, want an error", addr)
			}
		})
	}

	t.Run("a context cancelled before listening", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		port := freePort(t)
		// Either answer is acceptable: an error, or a clean immediate stop.
		// What matters is that it returns and holds no port.
		done := make(chan error, 1)
		go func() { done <- New(&fakeService{}, Options{Addr: "localhost:" + port}).ListenAndServe(ctx) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("ListenAndServe did not return")
		}
		assertPortFree(t, port)
	})
}

// DEP-3 at the socket: "localhost" must open loopback listeners only, one per
// address family. Nothing is served here; the listeners are inspected and
// closed.
func TestLocalhostListensOnLoopbackOnly(t *testing.T) {
	port := freePort(t)
	listeners, err := Listen(context.Background(), "localhost:"+port, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listeners.Close()

	addrs := listeners.Addrs()
	for _, addr := range addrs {
		tcp, ok := addr.(*net.TCPAddr)
		if !ok || !tcp.IP.IsLoopback() {
			t.Errorf("listening on %s, which is not a loopback address", addr)
		}
		if ok && fmt.Sprint(tcp.Port) != port {
			t.Errorf("listening on %s, want port %s", addr, port)
		}
	}
	want := 1
	if hasIPv6Loopback() {
		want = 2
	}
	if len(addrs) != want {
		t.Errorf("%d listeners (%v), want %d", len(addrs), addrs, want)
	}

	if err := listeners.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	assertPortFree(t, port)
	if err := listeners.Close(); err != nil {
		t.Errorf("a second Close: %v, want nil", err)
	}
}

// Listening is a step of its own: the port is held from Listen on, so a
// caller can do slow or irreversible work (open the database) knowing no
// other ogre has the address, and nothing is answered until Serve.
func TestListenThenServe(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + port
	listeners, err := Listen(context.Background(), addr, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The port is taken already.
	if second, err := net.Listen("tcp4", addr); err == nil {
		second.Close()
		t.Fatal("the address is still free after Listen")
	} else if !isAddrInUse(err) {
		t.Errorf("a second listen failed with %v, want address in use", err)
	}

	// A request made now waits; it is answered once Serve runs.
	type result struct {
		status int
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := testClient().Get("http://" + addr + "/-/healthz")
		if err != nil {
			got <- result{err: err}
			return
		}
		resp.Body.Close()
		got <- result{status: resp.StatusCode}
	}()
	select {
	case r := <-got:
		t.Fatalf("a request was answered before Serve: %+v", r)
	case <-time.After(150 * time.Millisecond):
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(&fakeService{}, Options{Addr: addr}).Serve(ctx, listeners) }()
	if r := <-got; r.err != nil || r.status != http.StatusOK {
		t.Errorf("the waiting request: status %d, error %v; want 200", r.status, r.err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
	assertPortFree(t, port)
}

// When [::1]:port belongs to someone else, "localhost" would reach that
// program from every browser that resolves the name to ::1 first. Starting
// on 127.0.0.1 alone would report a working Directory that half the requests
// never see, so it is refused, with the same diagnosis as on IPv4.
func TestLocalhostRefusesWhenIPv6PortIsTaken(t *testing.T) {
	if !hasIPv6Loopback() {
		t.Skip("no IPv6 loopback on this machine")
	}
	// A port taken on [::1] and free on 127.0.0.1.
	hold := func(t *testing.T) (net.Listener, string) {
		t.Helper()
		for range 50 {
			ln, err := net.Listen("tcp6", "[::1]:0")
			if err != nil {
				t.Fatal(err)
			}
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			v4, err := net.Listen("tcp4", "127.0.0.1:"+port)
			if err != nil {
				ln.Close()
				continue
			}
			v4.Close()
			return ln, port
		}
		t.Fatal("found no port free on 127.0.0.1 and taken on [::1]")
		return nil, ""
	}

	t.Run("by another program", func(t *testing.T) {
		holder, port := hold(t)
		defer holder.Close()
		go func() { // hang up on the "are you an ogre?" question at once
			for {
				c, err := holder.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		logger, logs := captureLog()

		listeners, err := Listen(context.Background(), "localhost:"+port, logger)
		if err == nil {
			listeners.Close()
			t.Fatalf("Listen succeeded although [::1]:%s is taken", port)
		}
		if !isAddrInUse(err) || !strings.Contains(err.Error(), "[::1]:"+port) || !strings.Contains(err.Error(), "another program") {
			t.Errorf("error = %v, want one saying another program holds [::1]:%s", err, port)
		}
		// Nothing is left half open, and nothing claims to be listening.
		assertPortFree(t, port)
		if strings.Contains(logs.String(), "listening") {
			t.Errorf("the log claims a listener:\n%s", logs.String())
		}
	})

	t.Run("by another ogre", func(t *testing.T) {
		holder, port := hold(t)
		holder.Close()
		first := New(&fakeService{}, Options{Addr: "[::1]:" + port, Version: testVersion})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- first.ListenAndServe(ctx) }()
		defer func() {
			cancel()
			<-done
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			resp, err := testClient().Get("http://[::1]:" + port + "/-/healthz")
			if err == nil {
				resp.Body.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the first server did not come up: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
		}

		// The deadline is for the day this refusal breaks: the server would
		// then serve, return nil when the deadline passes, and fail the
		// check below instead of hanging the test.
		refused, cancelRefused := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelRefused()
		err := New(&fakeService{}, Options{Addr: "localhost:" + port}).ListenAndServe(refused)
		if err == nil || !strings.Contains(err.Error(), "another ogre") || !strings.Contains(err.Error(), testVersion) {
			t.Errorf("error = %v, want one saying another ogre (version %s) is running", err, testVersion)
		}
		assertPortFree(t, port)
	})
}

// A machine without IPv6 cannot have anything answering on ::1, so there the
// second listener is skipped with a warning and ogre serves on 127.0.0.1.
// The failure is injected, because it cannot be arranged on a machine that
// has IPv6.
func TestLocalhostServesWithoutIPv6(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EADDRNOTAVAIL, syscall.EAFNOSUPPORT} {
		t.Run(errno.Error(), func(t *testing.T) {
			port := freePort(t)
			var lc net.ListenConfig
			var asked []string
			noIPv6 := func(ctx context.Context, network, address string) (net.Listener, error) {
				asked = append(asked, network+" "+address)
				if network == "tcp6" {
					return nil, &net.OpError{Op: "listen", Net: network, Err: os.NewSyscallError("bind", errno)}
				}
				return lc.Listen(ctx, network, address)
			}
			logger, logs := captureLog()

			listeners, err := listenWith(context.Background(), "localhost:"+port, logger, noIPv6)
			if err != nil {
				t.Fatalf("listenWith = %v, want the IPv4 listener alone", err)
			}
			if want := []string{"tcp4 127.0.0.1:" + port, "tcp6 [::1]:" + port}; !reflect.DeepEqual(asked, want) {
				t.Errorf("asked to listen on %q, want %q", asked, want)
			}
			if addrs := listeners.Addrs(); len(addrs) != 1 || addrs[0].String() != "127.0.0.1:"+port {
				t.Errorf("listening on %v, want only 127.0.0.1:%s", addrs, port)
			}
			if !strings.Contains(logs.String(), "not listening on the IPv6 loopback address") || !strings.Contains(logs.String(), `"level":"WARN"`) {
				t.Errorf("no warning about the missing IPv6 listener:\n%s", logs.String())
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- New(&fakeService{}, Options{Addr: "localhost:" + port}).Serve(ctx, listeners) }()
			resp, err := testClient().Get("http://127.0.0.1:" + port + "/-/healthz")
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("127.0.0.1 does not answer: %v", err)
			}
			resp.Body.Close()
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Serve = %v, want nil", err)
			}
		})
	}
}

// What the net/http server is told: limits that keep a stuck or hostile
// client from holding a connection or sending megabytes of headers, and no
// answers of its own that would skip the Host check.
func TestHTTPServerSettings(t *testing.T) {
	srv := New(&fakeService{}, Options{Addr: testAddr}).httpServer()
	timeouts := map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	}
	for name, d := range timeouts {
		if d <= 0 || d > 5*time.Minute {
			t.Errorf("%s = %v, want a limit of at most a few minutes", name, d)
		}
	}
	if srv.ReadHeaderTimeout > srv.ReadTimeout {
		t.Errorf("ReadHeaderTimeout %v is longer than ReadTimeout %v", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
	if srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 64<<10 {
		t.Errorf("MaxHeaderBytes = %d, want at most 64 KiB", srv.MaxHeaderBytes)
	}
	if !srv.DisableGeneralOptionsHandler {
		t.Error("net/http would answer \"OPTIONS *\" itself, before the Host check")
	}
	if srv.Handler == nil || srv.ErrorLog == nil {
		t.Error("the server has no handler or no error log")
	}
}

// Requests that net/http could answer before the handler runs, sent by hand
// over a real connection.
func TestRequestsNetHTTPWouldAnswerItself(t *testing.T) {
	port := freePort(t)
	svc := &fakeService{}
	logger, logs := captureLog()
	stop := startServer(t, New(svc, Options{Addr: "127.0.0.1:" + port, Logger: logger}), port)
	defer func() { _ = stop() }()

	send := func(t *testing.T, raw string) *http.Response {
		t.Helper()
		conn, err := net.DialTimeout("tcp4", "127.0.0.1:"+port, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		// The server may answer and close before a large request is fully
		// written; the answer is what matters.
		_, _ = io.WriteString(conn, raw)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("no response: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	host := "Host: 127.0.0.1:" + port + "\r\nConnection: close\r\n\r\n"

	// "OPTIONS *" reaches the handler: it gets the Host check, the security
	// headers and a refusal, never net/http's own bare 200.
	t.Run("OPTIONS *", func(t *testing.T) {
		resp := send(t, "OPTIONS * HTTP/1.1\r\n"+host)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("the response lacks the security headers: %v", resp.Header)
		}
		assertNoCORS(t, resp.Header)
	})
	t.Run("OPTIONS * with a hostile Host", func(t *testing.T) {
		resp := send(t, "OPTIONS * HTTP/1.1\r\nHost: evil.example:"+port+"\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.StatusCode)
		}
	})

	// These two never reach the handler and cannot be made to: net/http
	// refuses them while parsing. They are fixed text with nothing of the
	// request in it. Their shape is pinned so that a Go release that changes
	// it is noticed here.
	t.Run("malformed percent-escape", func(t *testing.T) {
		resp := send(t, "GET /a%zz HTTP/1.1\r\n"+host)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusBadRequest || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
			t.Errorf("status = %d, Content-Type = %q; want net/http's plain 400", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if strings.Contains(string(body), "zz") || resp.Header.Get("Location") != "" {
			t.Errorf("the 400 repeats the request or redirects: %q", body)
		}
		assertNoCORS(t, resp.Header)
	})
	t.Run("headers over the limit", func(t *testing.T) {
		resp := send(t, "GET /"+strings.Repeat("a", maxHeaderBytes+8<<10)+" HTTP/1.1\r\n"+host)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("status = %d, want 431", resp.StatusCode)
		}
		if len(body) > 200 || strings.Contains(string(body), "aaaa") {
			t.Errorf("the 431 repeats the request: %.80q", body)
		}
		assertNoCORS(t, resp.Header)
	})
	// A long but permitted request is still served.
	t.Run("a long path under the limit", func(t *testing.T) {
		resp := send(t, "GET /"+strings.Repeat("a", 8<<10)+" HTTP/1.1\r\n"+host)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want the resolver's 404", resp.StatusCode)
		}
	})

	if got := svc.resolvedPaths(); len(got) != 1 {
		t.Errorf("%d requests reached the resolver, want only the long path", len(got))
	}
	if strings.Contains(logs.String(), "zz") {
		t.Errorf("the log repeats a refused request:\n%.400s", logs.String())
	}
}

// Browsers open connections ahead of need and often never use them. Such a
// connection has nothing in flight, so it must not hold up a shutdown: plain
// http.Server.Shutdown waits up to five seconds for each, which made Ctrl-C
// hang and, past the grace period, fail.
func TestShutdownDoesNotWaitForUnusedConnections(t *testing.T) {
	port := freePort(t)
	stop := startServer(t, New(&fakeService{}, Options{Addr: "127.0.0.1:" + port}), port)

	// Connections that never send a byte, like a browser's spare sockets.
	var idle []net.Conn
	for range 3 {
		conn, err := net.DialTimeout("tcp4", "127.0.0.1:"+port, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		idle = append(idle, conn)
	}
	// And one that did serve a request and is now kept alive.
	keepAlive, err := net.DialTimeout("tcp4", "127.0.0.1:"+port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer keepAlive.Close()
	fmt.Fprintf(keepAlive, "GET /-/healthz HTTP/1.1\r\nHost: 127.0.0.1:%s\r\n\r\n", port)
	resp, err := http.ReadResponse(bufio.NewReader(keepAlive), nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the kept-alive connection got no answer: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	// Give the server a moment to have accepted every connection.
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := stop(); err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("shutdown took %v with only unused connections open, want it at once", elapsed)
	}
	assertPortFree(t, port)

	// The unused connections were closed by the server.
	for i, conn := range idle {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("unused connection %d is still open after shutdown (read error: %v)", i, err)
		}
	}
}

func TestUnusedConnsTracksOnlyNewConnections(t *testing.T) {
	u := &unusedConns{conns: map[net.Conn]struct{}{}}
	a, aPeer := net.Pipe()
	b, bPeer := net.Pipe()
	defer aPeer.Close()
	defer bPeer.Close()

	u.track(a, http.StateNew)
	u.track(b, http.StateNew)
	u.track(b, http.StateActive) // b has started its first request
	u.closeAll()

	if _, err := a.Write([]byte("x")); err == nil {
		t.Error("the unused connection was not closed")
	}
	go func() { _, _ = io.Copy(io.Discard, bPeer) }()
	if _, err := b.Write([]byte("x")); err != nil {
		t.Errorf("a connection with a request in flight was closed: %v", err)
	}
	for _, state := range []http.ConnState{http.StateIdle, http.StateHijacked, http.StateClosed} {
		u.track(a, http.StateNew)
		u.track(a, state)
		if len(u.conns) != 0 {
			t.Errorf("a connection in state %v is still counted as unused", state)
		}
	}
	b.Close()
}
