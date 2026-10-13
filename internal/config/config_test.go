package config

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// env returns an environment lookup over a fixed set of variables, so no
// test depends on, or reads, the real environment.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// home is the only variable most tests need: it makes the default database
// path computable, and points it far from the real data directory.
var home = map[string]string{"HOME": "/home/tester", "LOCALAPPDATA": `C:\Users\tester\AppData\Local`}

func with(extra map[string]string) func(string) string {
	vars := map[string]string{}
	for k, v := range home {
		vars[k] = v
	}
	for k, v := range extra {
		vars[k] = v
	}
	return env(vars)
}

func TestLoadDefaults(t *testing.T) {
	got, err := load(nil, with(nil), "darwin")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := Config{
		Addr:     "localhost:4655",
		DBPath:   filepath.Join("/home/tester", "Library", "Application Support", "ogre", "ogre.db"),
		LogLevel: slog.LevelInfo,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the defaults do not validate: %v", err)
	}
}

func TestLoadDefaultDBPathPerOS(t *testing.T) {
	tests := []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"darwin", nil, filepath.Join("/home/tester", "Library", "Application Support", "ogre", "ogre.db")},
		{"linux", nil, filepath.Join("/home/tester", ".local", "share", "ogre", "ogre.db")},
		{"linux", map[string]string{"XDG_DATA_HOME": "/data/xdg"}, filepath.Join("/data/xdg", "ogre", "ogre.db")},
		{"windows", nil, filepath.Join(`C:\Users\tester\AppData\Local`, "ogre", "ogre.db")},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got, err := load(nil, with(tt.vars), tt.goos)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.DBPath != tt.want {
				t.Errorf("DBPath = %q, want %q", got.DBPath, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	allEnv := map[string]string{
		EnvAddr:       "127.0.0.1:9000",
		EnvDB:         "/env/links.db",
		EnvAllowHosts: "go, Go.Example.com:8080",
		EnvLogLevel:   "debug",
	}

	tests := []struct {
		name string
		args []string
		vars map[string]string
		want Config
	}{
		{
			name: "environment",
			vars: allEnv,
			want: Config{
				Addr: "127.0.0.1:9000", DBPath: "/env/links.db",
				AllowHosts: []string{"go", "go.example.com:8080"}, LogLevel: slog.LevelDebug,
			},
		},
		{
			name: "flags win over the environment",
			args: []string{"--addr", "[::1]:7000", "--db", "/flag/links.db", "--allow-host", "links", "--log-level", "error"},
			vars: allEnv,
			want: Config{
				Addr: "[::1]:7000", DBPath: "/flag/links.db",
				AllowHosts: []string{"links"}, LogLevel: slog.LevelError,
			},
		},
		{
			name: "each flag overrides only its own variable",
			args: []string{"--log-level=warn"},
			vars: allEnv,
			want: Config{
				Addr: "127.0.0.1:9000", DBPath: "/env/links.db",
				AllowHosts: []string{"go", "go.example.com:8080"}, LogLevel: slog.LevelWarn,
			},
		},
		{
			name: "a bad variable does not matter when its flag is given",
			args: []string{"--log-level", "info", "--allow-host", "go"},
			vars: map[string]string{EnvLogLevel: "shouting", EnvAllowHosts: "http://go/"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, AllowHosts: []string{"go"}, LogLevel: slog.LevelInfo},
		},
		{
			name: "single-dash flags",
			args: []string{"-addr", "127.0.0.1:1234", "-dev"},
			want: Config{Addr: "127.0.0.1:1234", DBPath: defaultDB, LogLevel: slog.LevelInfo, Dev: true},
		},
		{
			name: "switches",
			args: []string{"--dev", "--allow-unauthenticated-network", "--addr", "0.0.0.0:4655"},
			want: Config{
				Addr: "0.0.0.0:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo,
				Dev: true, AllowUnauthenticatedNetwork: true,
			},
		},
		{
			name: "the switches have no environment variable",
			vars: map[string]string{"OGRE_DEV": "1", "OGRE_ALLOW_UNAUTHENTICATED_NETWORK": "1"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "allow-host repeated",
			args: []string{"--allow-host", "go", "--allow-host", "links.example.com"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo, AllowHosts: []string{"go", "links.example.com"}},
		},
		{
			name: "allow-host comma list",
			args: []string{"--allow-host", "go,links.example.com:8080"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo, AllowHosts: []string{"go", "links.example.com:8080"}},
		},
		{
			name: "allow-host mixed, with spaces, case and repeats",
			args: []string{"--allow-host", " GO , links ,", "--allow-host=go,[::1]:9000"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo, AllowHosts: []string{"go", "links", "[::1]:9000"}},
		},
		{
			name: "allow-host flag replaces the variable, it does not add to it",
			args: []string{"--allow-host", "links"},
			vars: map[string]string{EnvAllowHosts: "go"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo, AllowHosts: []string{"links"}},
		},
		{
			name: "log level in any case",
			vars: map[string]string{EnvLogLevel: " WARN "},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelWarn},
		},
		{
			// The address is compared and printed as text later on, so the
			// port has one spelling: the server listens on 4655 either way.
			name: "a port with leading zeros is canonicalized",
			args: []string{"--addr", "localhost:04655"},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "a port with leading zeros on an IPv6 address",
			args: []string{"--addr", "[::1]:0080"},
			want: Config{Addr: "[::1]:80", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "a port that is not a number is left for Validate",
			args: []string{"--addr", "localhost:+4655"},
			want: Config{Addr: "localhost:+4655", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "localhost is canonicalized",
			args: []string{"--addr", " LocalHost:4655 "},
			want: Config{Addr: "localhost:4655", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "an address Load cannot parse is kept for Validate to explain",
			args: []string{"--addr", "nonsense"},
			want: Config{Addr: "nonsense", DBPath: defaultDB, LogLevel: slog.LevelInfo},
		},
		{
			name: "a relative database path is kept as given",
			args: []string{"--db", "links.db"},
			want: Config{Addr: "localhost:4655", DBPath: "links.db", LogLevel: slog.LevelInfo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := load(tt.args, with(tt.vars), "linux")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// defaultDB is the default database path of the "linux" cases in TestLoad.
var defaultDB = filepath.Join("/home/tester", ".local", "share", "ogre", "ogre.db")

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		vars map[string]string
		want []string // every one must appear in the message
	}{
		{
			name: "unknown flag",
			args: []string{"--bogus"},
			want: []string{"-bogus", "valid flags", "--addr", "--db", "--allow-host", "--log-level", "--dev", "--allow-unauthenticated-network"},
		},
		{
			name: "misspelled flag",
			args: []string{"--adr", "localhost:1"},
			want: []string{"-adr", "--addr"},
		},
		{
			name: "flag without its value",
			args: []string{"--addr"},
			want: []string{"-addr"},
		},
		{
			name: "stray argument",
			args: []string{"--dev", "roadmap"},
			want: []string{`"roadmap"`, "flags only"},
		},
		{
			name: "a value for a switch",
			args: []string{"--dev=sometimes"},
			want: []string{"sometimes", "-dev"},
		},
		{
			name: "bad log level flag",
			args: []string{"--log-level", "loud"},
			want: []string{"--log-level", `"loud"`, "debug, info, warn or error"},
		},
		{
			name: "bad log level variable",
			vars: map[string]string{EnvLogLevel: "loud"},
			want: []string{"OGRE_LOG_LEVEL", `"loud"`},
		},
		{
			name: "allow-host given a URL",
			args: []string{"--allow-host", "http://go/"},
			want: []string{"--allow-host", "http://go/", "URL"},
		},
		{
			name: "allow-host variable given a path",
			vars: map[string]string{EnvAllowHosts: "go,go/links"},
			want: []string{"OGRE_ALLOW_HOSTS", "go/links"},
		},
		{
			name: "allow-host wildcard",
			args: []string{"--allow-host", "*.example.com"},
			want: []string{"wildcards"},
		},
		{
			name: "allow-host with nothing in it",
			args: []string{"--allow-host", " , "},
			want: []string{"-allow-host", "host name"},
		},
		{
			name: "empty database path",
			args: []string{"--db", ""},
			want: []string{"--db"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(tt.args, with(tt.vars), "linux")
			if err == nil {
				t.Fatal("load succeeded")
			}
			if errors.Is(err, flag.ErrHelp) {
				t.Errorf("a mistake was reported as a request for help: %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestLoadWithoutAHomeDirectory(t *testing.T) {
	_, err := load(nil, env(nil), "linux")
	if err == nil {
		t.Fatal("load succeeded without any way to find the data directory")
	}
	for _, want := range []string{"--db", "OGRE_DB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not point to %s", err, want)
		}
	}

	// Naming the database makes the data directory irrelevant.
	for name, load := range map[string]func() (Config, error){
		"flag":     func() (Config, error) { return load([]string{"--db", "/x/links.db"}, env(nil), "linux") },
		"variable": func() (Config, error) { return load(nil, env(map[string]string{EnvDB: "/x/links.db"}), "linux") },
	} {
		cfg, err := load()
		if err != nil || cfg.DBPath != "/x/links.db" {
			t.Errorf("with the database named by %s: DBPath %q, err %v", name, cfg.DBPath, err)
		}
	}
}

func TestLoadHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		_, err := load([]string{arg}, with(nil), "linux")
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("load(%s) error = %v, want flag.ErrHelp", arg, err)
		}
	}
}

func TestUsage(t *testing.T) {
	usage := Usage()
	for _, want := range []string{
		"--addr address", "--db file", "--allow-host value", "--log-level string", "--dev\n", "--allow-unauthenticated-network\n",
		EnvAddr, EnvDB, EnvAllowHosts, EnvLogLevel, "localhost:4655",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage does not mention %s:\n%s", want, usage)
		}
	}
}

// TestLoadUsesTheGivenEnvironment guards the promise that Load reads only
// what it is handed: the process environment must not leak in.
func TestLoadUsesTheGivenEnvironment(t *testing.T) {
	t.Setenv(EnvAddr, "10.9.8.7:1")
	t.Setenv(EnvDB, "/from/the/process/environment.db")
	t.Setenv(EnvAllowHosts, "leaked")
	t.Setenv(EnvLogLevel, "error")

	got, err := Load(nil, env(map[string]string{EnvDB: "/given.db"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{Addr: "localhost:4655", DBPath: "/given.db", LogLevel: slog.LevelInfo}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDataDir(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		vars    map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "macOS", goos: "darwin",
			vars: map[string]string{"HOME": "/Users/ada", "XDG_DATA_HOME": "/ignored/on/macos"},
			want: filepath.Join("/Users/ada", "Library", "Application Support", "ogre"),
		},
		{name: "macOS without HOME", goos: "darwin", wantErr: true},
		{
			name: "Linux with XDG_DATA_HOME", goos: "linux",
			vars: map[string]string{"HOME": "/home/ada", "XDG_DATA_HOME": "/data/xdg"},
			want: filepath.Join("/data/xdg", "ogre"),
		},
		{
			name: "Linux without XDG_DATA_HOME", goos: "linux",
			vars: map[string]string{"HOME": "/home/ada"},
			want: filepath.Join("/home/ada", ".local", "share", "ogre"),
		},
		{
			name: "Linux ignores a relative XDG_DATA_HOME", goos: "linux",
			vars: map[string]string{"HOME": "/home/ada", "XDG_DATA_HOME": "relative/dir"},
			want: filepath.Join("/home/ada", ".local", "share", "ogre"),
		},
		{name: "Linux with neither", goos: "linux", wantErr: true},
		{
			name: "other Unix follows Linux", goos: "freebsd",
			vars: map[string]string{"HOME": "/home/ada"},
			want: filepath.Join("/home/ada", ".local", "share", "ogre"),
		},
		{
			name: "Windows", goos: "windows",
			vars: map[string]string{"LOCALAPPDATA": `C:\Users\ada\AppData\Local`, "USERPROFILE": `C:\Users\ada`},
			want: filepath.Join(`C:\Users\ada\AppData\Local`, "ogre"),
		},
		{
			name: "Windows without LocalAppData", goos: "windows",
			vars: map[string]string{"USERPROFILE": `C:\Users\ada`},
			want: filepath.Join(`C:\Users\ada`, "AppData", "Local", "ogre"),
		},
		{name: "Windows with neither", goos: "windows", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dataDir(tt.goos, env(tt.vars))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("dataDir = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultDataDir runs the real function with HOME pointed at a temporary
// directory, and checks that asking for the path creates nothing.
func TestDefaultDataDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "AppData", "Local"))

	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join(tmp, "Library", "Application Support", "ogre")
	case "windows":
		want = filepath.Join(tmp, "AppData", "Local", "ogre")
	default:
		want = filepath.Join(tmp, ".local", "share", "ogre")
	}
	got, err := DefaultDataDir()
	if err != nil {
		t.Fatalf("DefaultDataDir: %v", err)
	}
	if got != want {
		t.Errorf("DefaultDataDir = %q, want %q", got, want)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("DefaultDataDir created %d entries under the home directory; it must only compute a path", len(entries))
	}
}
