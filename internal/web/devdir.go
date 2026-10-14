package web

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// modulePath identifies this project's go.mod when SourceDir looks for the
// checkout.
const modulePath = "github.com/tfrydrychewicz/ogre"

// SourceDir finds this package's directory in a source checkout, for --dev.
//
// It looks in two places, in order:
//
//  1. The working directory and its parents, for the go.mod of this module;
//     the package is internal/web below it. This is the usual case: "make
//     run ARGS=--dev" or "go run ./cmd/ogre --dev" from anywhere in the
//     checkout.
//  2. The directory this file was compiled from, which the binary records
//     unless it was built with -trimpath. This lets a bin/ogre built from a
//     checkout be started with --dev from any directory.
//
// A candidate counts only if it holds templates/layout.html and static/, so
// a stale path or another project's checkout is never served.
func SourceDir() (string, error) {
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if isModuleRoot(dir) {
				candidates = append(candidates, filepath.Join(dir, "internal", "web"))
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		candidates = append(candidates, filepath.Dir(file))
	}
	for _, dir := range candidates {
		if isSourceDir(dir) {
			return dir, nil
		}
	}
	return "", errors.New("--dev serves the pages from the source checkout, and none was found: " +
		"start ogre from inside a checkout of " + modulePath)
}

// isModuleRoot reports whether dir holds the go.mod of this module.
func isModuleRoot(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "module ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`) == modulePath
		}
	}
	return false
}

func isSourceDir(dir string) bool {
	layout, err := os.Stat(filepath.Join(dir, "templates", layoutFile))
	if err != nil || layout.IsDir() {
		return false
	}
	static, err := os.Stat(filepath.Join(dir, "static"))
	return err == nil && static.IsDir()
}
