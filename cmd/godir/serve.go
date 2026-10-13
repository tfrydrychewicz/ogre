package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/tfrydrychewicz/godir/internal/app"
	"github.com/tfrydrychewicz/godir/internal/config"
	"github.com/tfrydrychewicz/godir/internal/httpd"
	"github.com/tfrydrychewicz/godir/internal/store"
	"github.com/tfrydrychewicz/godir/internal/web"
)

// usage is the help text of "godir help" and "godir --help".
func usage() string {
	return "godir runs a go-links Directory.\n\n" +
		"Usage:\n" +
		"  godir [serve] [flags]   run the Directory (the default command)\n" +
		"  godir version           print the version\n\n" +
		config.Usage()
}

// serve runs the Directory until ctx is cancelled or the process is told to
// stop (Ctrl-C or SIGTERM), then shuts down gracefully.
//
// The order matters:
//
//  1. The configuration is checked before anything is created, so a refused
//     address leaves no database behind.
//  2. The address is bound before the database is opened. A godir that
//     cannot have the port (most often because another godir is running)
//     must not create, write to or migrate a database it will never serve;
//     with a newer binary that would change the schema under the running
//     one.
//  3. The database is opened and every link loaded before requests are
//     answered, so the first request already resolves. Connections made in
//     between wait in the listen queue.
func serve(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) error {
	cfg, err := config.Load(args, getenv)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Once shutdown has begun, hand signals back to the system: a second
	// Ctrl-C then ends the process at once instead of being swallowed.
	go func() {
		<-ctx.Done()
		stop()
	}()

	listeners, err := httpd.Listen(ctx, cfg.Addr, log)
	if err != nil {
		return err
	}
	// Serve takes the listeners over; until then a failure must free them.
	serving := false
	defer func() {
		if !serving {
			_ = listeners.Close()
		}
	}()

	log.Info("godir is starting", "version", version, "url", "http://"+cfg.Addr, "db", cfg.DBPath)
	// Validate let a non-loopback host through only because the override
	// flag was given; say what that means every time it happens.
	if host, _, err := net.SplitHostPort(cfg.Addr); err == nil && !config.IsLoopbackHost(host) {
		log.Warn("listening beyond this computer without sign-in: anyone who can reach this address can read and change your links. "+
			"--allow-host only names the addresses browsers may use for this Directory; it does not keep other machines out, "+
			"which can send any Host they like", "addr", cfg.Addr)
	}
	if cfg.Dev {
		log.Warn("--dev has no effect yet: pages are served from the binary until the web UI arrives")
	}

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	// Every acknowledged save is already on disk (NFR-7); closing only
	// releases the file.
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing the database failed", "db", cfg.DBPath, "error", err)
		}
	}()

	directory, err := app.New(ctx, st)
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.DBPath, err)
	}
	actor, err := directory.LocalActor(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.DBPath, err)
	}

	srv := httpd.New(directory, httpd.Options{
		Addr:       cfg.Addr,
		AllowHosts: cfg.AllowHosts,
		Version:    version,
		Actor:      actor,
		Logger:     log,
		UI:         web.Handler(version),
	})
	serving = true
	if err := srv.Serve(ctx, listeners); err != nil {
		return err
	}
	log.Info("godir has stopped")
	return nil
}
