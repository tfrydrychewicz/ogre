// Command ogre runs the Ogre server, a go-links Directory: a small web
// server that turns "go/name" into the address it stands for.
//
//	ogre [serve] [flags]   run the Directory (the default command)
//	ogre version           print the version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// version is what "ogre version" and the meta endpoint report. A release
// build sets it with -ldflags "-X main.version=1.2.3".
var version = "0.2.0-dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main without the process: it takes everything main would reach for
// globally, so tests can drive whole commands, and returns the exit code.
// A failure is one line on stderr and exit code 1.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	err := dispatch(ctx, args, getenv, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage())
		return 0
	}
	fmt.Fprintf(stderr, "ogre: %v\n", err)
	return 1
}

// dispatch picks the command. No command, or a flag in its place, means
// serve, so that "ogre" alone starts the Directory (DEP-1).
func dispatch(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return serve(ctx, args, getenv, stderr)
	}
	switch command, rest := args[0], args[1:]; command {
	case "serve":
		return serve(ctx, rest, getenv, stderr)
	case "version":
		if len(rest) > 0 {
			return fmt.Errorf("version takes no arguments, got %q", rest[0])
		}
		fmt.Fprintf(stdout, "ogre %s\n", version)
		return nil
	case "help":
		return flag.ErrHelp
	default:
		return fmt.Errorf("unknown command %q; the commands are serve (the default) and version. Run \"ogre help\" for the flags", command)
	}
}
