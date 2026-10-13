package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server timeouts. A Directory serves small requests and tiny responses, so
// anything slower than this is a stuck or hostile client.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownGrace     = 5 * time.Second
	probeTimeout      = 2 * time.Second

	// maxHeaderBytes bounds the request line and headers together. No go
	// link and no API call comes near it, and it caps how much of a request
	// can ever be echoed or logged. (net/http adds a little slack of its
	// own.)
	maxHeaderBytes = 64 << 10
)

// Listeners are the sockets a Directory answers on, opened by Listen and
// served by Server.Serve.
//
// Opening them is a step of its own so that a caller can find out that the
// address is taken before it does anything it could regret: ogre binds
// first and opens its database second, so a second instance never touches,
// creates or migrates a database it will not serve.
type Listeners struct {
	list []net.Listener
}

// Addrs returns the address of each listener.
func (l *Listeners) Addrs() []net.Addr {
	addrs := make([]net.Addr, len(l.list))
	for i, ln := range l.list {
		addrs[i] = ln.Addr()
	}
	return addrs
}

// Close releases every listener. It is for the caller that got Listeners and
// then could not serve; Serve closes them itself.
func (l *Listeners) Close() error {
	var first error
	for _, ln := range l.list {
		if err := ln.Close(); err != nil && first == nil && !errors.Is(err, net.ErrClosed) {
			first = err
		}
	}
	return first
}

// Listen opens the listeners for addr, "host:port", without serving yet.
// Connections made before Serve is called wait in the system's queue.
//
// The host "localhost" means both loopback addresses, 127.0.0.1 and [::1]:
// browsers may resolve the name to either, and a server on only one of them
// looks unreachable half the time. On a machine without IPv6 the second is
// skipped with a warning. If [::1] exists but its port is taken, Listen
// fails: "localhost" resolves to ::1 first on many systems, so whatever
// holds that port would answer the go links meant for this Directory.
//
// When the port is taken, the error says whether another ogre holds it or
// some other program, because the remedies differ.
//
// log receives the warning about a missing IPv6 loopback; nil discards it.
func Listen(ctx context.Context, addr string, log *slog.Logger) (*Listeners, error) {
	var lc net.ListenConfig
	return listenWith(ctx, addr, log, lc.Listen)
}

// bindFunc opens one listener; net.ListenConfig.Listen is the real one.
// Tests substitute failures that cannot be arranged on a real machine.
type bindFunc func(ctx context.Context, network, address string) (net.Listener, error)

func listenWith(ctx context.Context, addr string, log *slog.Logger, bind bindFunc) (*Listeners, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("listen address %q: %w", addr, err)
	}

	if !strings.EqualFold(host, "localhost") {
		ln, err := bind(ctx, "tcp", addr)
		if err != nil {
			return nil, listenError(ctx, addr, err)
		}
		return &Listeners{list: []net.Listener{ln}}, nil
	}

	v4Addr := net.JoinHostPort("127.0.0.1", port)
	v4, err := bind(ctx, "tcp4", v4Addr)
	if err != nil {
		return nil, listenError(ctx, v4Addr, err)
	}
	// Reuse the port the first listener got, which matters for port 0.
	_, port, _ = net.SplitHostPort(v4.Addr().String())
	v6Addr := net.JoinHostPort("::1", port)
	v6, err := bind(ctx, "tcp6", v6Addr)
	switch {
	case err == nil:
		return &Listeners{list: []net.Listener{v4, v6}}, nil
	case isAddrInUse(err):
		// Someone else answers on [::1]:port. Starting anyway would report
		// a running Directory while a browser that resolves "localhost" to
		// ::1 talks to that other program.
		_ = v4.Close()
		return nil, listenError(ctx, v6Addr, err)
	default:
		// No IPv6 on this machine: nothing can answer on ::1, so serving on
		// 127.0.0.1 alone is safe.
		log.Warn("not listening on the IPv6 loopback address, which this computer does not seem to have; "+
			"use http://127.0.0.1 if localhost does not answer", "addr", v6Addr, "error", err)
		return &Listeners{list: []net.Listener{v4}}, nil
	}
}

// ListenAndServe is Listen on Options.Addr followed by Serve.
func (s *Server) ListenAndServe(ctx context.Context) error {
	listeners, err := Listen(ctx, s.opts.Addr, s.log)
	if err != nil {
		return err
	}
	return s.Serve(ctx, listeners)
}

// httpServer builds the net/http server around the handler.
func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// Without this net/http answers "OPTIONS *" by itself with a 200,
		// before the Host check and without the security headers.
		DisableGeneralOptionsHandler: true,
		ErrorLog:                     slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
}

// Serve answers requests on the listeners until ctx is cancelled, then shuts
// down gracefully and returns nil. It closes the listeners in every case.
func (s *Server) Serve(ctx context.Context, l *Listeners) error {
	listeners := l.list
	srv := s.httpServer()
	unused := &unusedConns{conns: map[net.Conn]struct{}{}}
	srv.ConnState = unused.track

	serveErr := make(chan error, len(listeners))
	for _, ln := range listeners {
		s.log.Info("listening", "addr", ln.Addr().String())
		go func() { serveErr <- srv.Serve(ln) }()
	}

	var result error
	running := len(listeners)
	select {
	case err := <-serveErr:
		// One listener failed on its own; a half-listening server would be
		// the "unreachable half the time" case, so stop the rest.
		running--
		result = fmt.Errorf("serve: %w", err)
		_ = srv.Close()
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if err := shutDown(shutdownCtx, srv, unused); err != nil {
			_ = srv.Close()
			result = fmt.Errorf("shut down: %w", err)
		}
	}
	// Serve closes its listener as it returns. Waiting for every Serve means
	// the port is free when this function returns, even if the context was
	// cancelled before a Serve goroutine got to run.
	for ; running > 0; running-- {
		<-serveErr
	}
	return result
}

// shutDown stops srv gracefully: no new connections, requests in flight are
// answered, and then it returns.
//
// http.Server.Shutdown alone also waits for connections on which no request
// was ever sent, for as long as five seconds each. Browsers open such
// connections ahead of need all the time, so a plain Shutdown made Ctrl-C
// hang, and fail when the wait outlasted the grace period. A connection that
// has not sent a request has nothing in flight, so those are closed at once.
func shutDown(ctx context.Context, srv *http.Server, unused *unusedConns) error {
	done := make(chan error, 1)
	go func() { done <- srv.Shutdown(ctx) }()

	// Repeated because Shutdown closes the listeners a moment after it is
	// called, and a connection accepted in that moment is unused too.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		unused.closeAll()
		select {
		case err := <-done:
			return err
		case <-ticker.C:
		}
	}
}

// unusedConns is the set of connections that are open and have not sent a
// request yet. It follows the server's own bookkeeping through
// http.Server.ConnState.
type unusedConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// track is the http.Server.ConnState hook. A connection is unused from the
// moment it is accepted until its first request starts to arrive.
func (u *unusedConns) track(c net.Conn, state http.ConnState) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if state == http.StateNew {
		u.conns[c] = struct{}{}
	} else {
		delete(u.conns, c)
	}
}

// closeAll closes every connection that is still unused. The server notices
// the closed connection and reports it through track, which forgets it.
func (u *unusedConns) closeAll() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for c := range u.conns {
		_ = c.Close()
	}
}

// listenError explains a failed listen. For a busy port it asks whoever holds
// it for the meta endpoint, to tell "ogre is already running" (fine, use it)
// from "something else has this port" (pick another).
func listenError(ctx context.Context, addr string, err error) error {
	if !isAddrInUse(err) {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	if version, ok := probeOgre(ctx, addr); ok {
		return fmt.Errorf("listen on %s: another ogre (version %q) is already running there; "+
			"stop it first, or choose another port with --addr: %w", addr, version, err)
	}
	return fmt.Errorf("listen on %s: the port is in use by another program; "+
		"choose another port with --addr: %w", addr, err)
}

// probeOgre asks the server at addr whether it is an Ogre server.
func probeOgre(ctx context.Context, addr string) (version string, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+apiPrefix+"meta", nil)
	if err != nil {
		return "", false
	}
	// No proxy, no redirects, no connection kept: the question is strictly
	// who answers on this address.
	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var meta metaJSON
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&meta); err != nil {
		return "", false
	}
	return meta.Version, meta.Product == product
}
