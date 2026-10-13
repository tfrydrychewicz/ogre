package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// appDirName is the directory godir keeps under the platform's data location.
const appDirName = "godir"

// DefaultDataDir returns the per-user directory for the database, backups and
// logs: ~/Library/Application Support/godir on macOS, $XDG_DATA_HOME/godir
// (or ~/.local/share/godir) on Linux and other Unix systems, and
// %LocalAppData%\godir on Windows.
//
// The data lives there rather than next to the binary or in a source
// checkout, so updating or deleting the program never touches a user's links.
// DefaultDataDir only computes the path; it creates nothing.
func DefaultDataDir() (string, error) {
	return dataDir(runtime.GOOS, os.Getenv)
}

// dataDir is DefaultDataDir for a given operating system and environment.
func dataDir(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		if dir := getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, appDirName), nil
		}
		if home := getenv("USERPROFILE"); home != "" {
			return filepath.Join(home, "AppData", "Local", appDirName), nil
		}
		return "", errors.New("cannot find the data directory: neither %LocalAppData% nor %USERPROFILE% is set")
	case "darwin":
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("cannot find the data directory: $HOME is not set")
		}
		return filepath.Join(home, "Library", "Application Support", appDirName), nil
	default:
		// The XDG specification says a relative value is invalid and must be
		// ignored; it would otherwise put the database wherever godir was
		// started from.
		if dir := getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
			return filepath.Join(dir, appDirName), nil
		}
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("cannot find the data directory: neither $XDG_DATA_HOME nor $HOME is set")
		}
		return filepath.Join(home, ".local", "share", appDirName), nil
	}
}
