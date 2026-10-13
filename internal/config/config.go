// Package config turns command-line flags and environment variables into the
// settings of "ogre serve", and holds the rule that keeps a Directory without
// sign-in off the network.
//
// It reads nothing itself: Load is given the arguments and an environment
// lookup, so the rules can be tested without touching the real process
// environment or the user's data directory.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DefaultAddr is where ogre listens unless told otherwise. "localhost" means
// both loopback addresses, 127.0.0.1 and ::1. 4655 spells GOLK on a phone
// keypad. The extension's default Directory URL is this address.
const DefaultAddr = "localhost:4655"

// DBFileName is the database file's name inside the data directory.
const DBFileName = "ogre.db"

// Environment variables. A flag, when given, wins over its variable.
const (
	EnvAddr       = "OGRE_ADDR"
	EnvDB         = "OGRE_DB"
	EnvAllowHosts = "OGRE_ALLOW_HOSTS"
	EnvLogLevel   = "OGRE_LOG_LEVEL"
)

// Config is everything "ogre serve" needs to start.
type Config struct {
	// Addr is the listen address, "host:port".
	Addr string
	// DBPath is the database file. Load fills in the per-user default.
	DBPath string
	// AllowHosts are extra Host header values to serve besides the loopback
	// names, lowercase, for example "go" or "go.example.com:8080".
	AllowHosts []string
	// LogLevel is the least severe level that is logged.
	LogLevel slog.Level
	// Dev will serve templates and static files from disk; it has no effect
	// until the web UI arrives.
	Dev bool
	// AllowUnauthenticatedNetwork lifts the refusal to listen beyond
	// loopback. It has no environment variable on purpose: exposing a
	// Directory that has no sign-in should be typed out each time, not
	// inherited from a shell profile.
	AllowUnauthenticatedNetwork bool
}

// Load builds a Config from args (the arguments after the command name) and
// the environment. Precedence is flag, then environment variable, then
// default. A value that loses to a higher-precedence one is not examined, so
// a bad variable does not block a run that overrides it.
//
// Load does not check that the address is safe to listen on; call Validate.
// When args ask for help, the error wraps flag.ErrHelp and Usage has the text
// to print.
func Load(args []string, getenv func(string) string) (Config, error) {
	return load(args, getenv, runtime.GOOS)
}

// load is Load for a given operating system, so the default database path of
// every platform can be tested on one.
func load(args []string, getenv func(string) string, goos string) (Config, error) {
	var (
		addr, db, logLevel string
		hosts              hostList
		cfg                Config
	)
	fs := newFlagSet(&addr, &db, &logLevel, &hosts, &cfg.Dev, &cfg.AllowUnauthenticatedNetwork)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("%w (valid flags: %s)", err, flagNames(fs))
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q: serve takes flags only (valid flags: %s)", fs.Arg(0), flagNames(fs))
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	// pick applies the precedence and reports where the value came from, so
	// an error can name the thing the user has to fix.
	pick := func(flagName, flagValue, envName, fallback string) (value, source string) {
		if given[flagName] {
			return flagValue, "--" + flagName
		}
		if v := getenv(envName); v != "" {
			return v, envName
		}
		return fallback, ""
	}

	cfg.Addr, _ = pick("addr", addr, EnvAddr, DefaultAddr)
	cfg.Addr = canonicalAddr(cfg.Addr)

	cfg.DBPath, _ = pick("db", db, EnvDB, "")
	if cfg.DBPath == "" {
		if given["db"] {
			return Config{}, errors.New("--db needs a file path")
		}
		dir, err := dataDir(goos, getenv)
		if err != nil {
			return Config{}, fmt.Errorf("%w; say where to keep the database with --db or %s", err, EnvDB)
		}
		cfg.DBPath = filepath.Join(dir, DBFileName)
	}

	level, source := pick("log-level", logLevel, EnvLogLevel, "info")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.TrimSpace(level))); err != nil {
		return Config{}, fmt.Errorf("%s: unknown log level %q; use debug, info, warn or error", source, level)
	}

	rawHosts, source := []string(hosts), "--allow-host"
	if !given["allow-host"] {
		rawHosts, source = splitList(getenv(EnvAllowHosts)), EnvAllowHosts
	}
	var err error
	if cfg.AllowHosts, err = cleanHosts(rawHosts); err != nil {
		return Config{}, fmt.Errorf("%s: %w", source, err)
	}
	return cfg, nil
}

// canonicalAddr tidies a listen address without judging it: surrounding space
// goes, "LocalHost:4655" becomes "localhost:4655", and a port written as
// "04655" becomes "4655". Everything later compares or prints the address as
// text (the Host check, the startup line), so there must be one spelling of
// it. Anything it cannot parse is returned as it is, for Validate to explain.
func canonicalAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if strings.EqualFold(host, "localhost") {
		host = "localhost"
	}
	// Only digits: Atoi alone would also take "+4655".
	if port != "" && strings.Trim(port, "0123456789") == "" {
		if n, err := strconv.Atoi(port); err == nil {
			port = strconv.Itoa(n)
		}
	}
	return net.JoinHostPort(host, port)
}

// Usage returns the help text for the flags Load accepts.
func Usage() string {
	var b strings.Builder
	var s string
	var h hostList
	var t bool
	fs := newFlagSet(&s, &s, &s, &h, &t, &t)
	b.WriteString("Flags of ogre serve:\n")
	fs.VisitAll(func(f *flag.Flag) {
		// Written out rather than left to PrintDefaults, to show flags with
		// two dashes, the way the error messages and the documentation do.
		value, text := flag.UnquoteUsage(f)
		if value != "" {
			value = " " + value
		}
		fmt.Fprintf(&b, "  --%s%s\n      %s\n", f.Name, value, text)
	})
	return b.String()
}

// newFlagSet declares the flags. It prints nothing by itself: the caller
// decides what to show, so a mistake produces one message, not a usage dump
// followed by the error.
func newFlagSet(addr, db, logLevel *string, hosts *hostList, dev, allowNetwork *bool) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.StringVar(addr, "addr", "", "listen `address` (default \""+DefaultAddr+"\", both loopback addresses; env "+EnvAddr+")")
	fs.StringVar(db, "db", "", "database `file` (default "+DBFileName+" in the per-user data directory; env "+EnvDB+")")
	fs.Var(hosts, "allow-host", "extra Host header `value` to serve, such as \"go\"; repeat the flag or separate values with commas (env "+EnvAllowHosts+")")
	fs.StringVar(logLevel, "log-level", "", "debug, info, warn or error (default \"info\"; env "+EnvLogLevel+")")
	fs.BoolVar(dev, "dev", false, "serve templates and static files from disk (no effect yet: it arrives with the web UI)")
	fs.BoolVar(allowNetwork, "allow-unauthenticated-network", false, "allow listening beyond this computer although ogre has no sign-in")
	return fs
}

// flagNames lists the flags for an error message.
func flagNames(fs *flag.FlagSet) string {
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, "--"+f.Name) })
	return strings.Join(names, ", ")
}
