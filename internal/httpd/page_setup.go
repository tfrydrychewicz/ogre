package httpd

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tfrydrychewicz/ogre/internal/web"
)

// extensionDirectoryURL is where the extension sends go links until the user
// saves another address on its settings page: DEFAULT_DIRECTORY_URL in
// extension/lib/config.js, the address of config.DefaultAddr. The setup page
// warns when this Directory runs anywhere else, and says which address to
// save there, since the Directory cannot know what the extension has saved.
const extensionDirectoryURL = "http://localhost:" + extensionPort

// extensionPort is the port of extensionDirectoryURL.
const extensionPort = "4655"

// setupData is the data of templates/setup.html.
type setupData struct {
	web.Base
	// DirectoryURL is this Directory's address, from its configuration.
	DirectoryURL string
	// ShortcutURL is DirectoryURL with Chrome's placeholder for what is
	// typed after the keyword: the URL of the search shortcut.
	ShortcutURL string
	// ExtensionURL is where the extension sends go links.
	ExtensionURL string
	// ExtensionReaches is true when a go link sent to ExtensionURL arrives
	// at this Directory (extensionReaches); the page warns otherwise.
	ExtensionReaches bool
	// ExtensionDir is the extension's folder in this checkout, or "" when
	// the binary does not know where its source is.
	ExtensionDir string
}

// setupPage explains how to type go links in a browser, GET /-/setup (plan
// L3.9). Everything on it is fixed text except two addresses: this
// Directory's, which the search shortcut must point at, and the extension's
// folder, which "Load unpacked" asks for.
func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	base := s.base("Setup")
	base.Current = "setup"
	dir := s.configuredBaseURL()
	s.render(w, r, http.StatusOK, "setup", setupData{
		Base:             base,
		DirectoryURL:     dir,
		ShortcutURL:      dir + "/%s",
		ExtensionURL:     extensionDirectoryURL,
		ExtensionReaches: extensionReaches(s.opts.Addr),
		ExtensionDir:     extensionDir(),
	})
}

// extensionReaches reports whether the extension's go links, sent to
// http://localhost:4655, arrive at a Directory listening on addr: the port
// must be the extension's, and the host one that "localhost" reaches on this
// machine, which is localhost itself, 127.0.0.1, ::1 or every interface.
// The host check accepts "localhost" on the listen port whatever the listen
// host (loopbackHosts), so the request is then served. Another loopback
// address such as 127.0.0.2 is not reached through "localhost".
func extensionReaches(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port != extensionPort {
		return false
	}
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsUnspecified() || ip.Equal(net.IPv4(127, 0, 0, 1)) || ip.Equal(net.IPv6loopback))
}

// configuredBaseURL is the address this Directory serves go links at, built
// from the configured listen address (not from the request, unlike
// linkAddress). A listen address on every interface
// (":4655", "0.0.0.0:4655") names no host a browser can use; such a
// Directory is still reachable on this machine as localhost.
func (s *Server) configuredBaseURL() string {
	host, port, err := net.SplitHostPort(s.opts.Addr)
	if err != nil {
		return "http://" + s.opts.Addr
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

var (
	extensionDirOnce sync.Once
	extensionDirPath string
)

// extensionDir finds the extension's folder: "extension" at the root of the
// checkout this binary was built from or is run in (web.SourceDir finds
// internal/web there). It counts only when it holds a manifest.json, so a
// binary far from any checkout shows no path rather than a wrong one. The
// answer cannot change while the program runs, so it is looked up once.
func extensionDir() string {
	extensionDirOnce.Do(func() {
		src, err := web.SourceDir()
		if err != nil {
			return
		}
		dir := filepath.Join(src, "..", "..", "extension")
		if info, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil && !info.IsDir() {
			if abs, err := filepath.Abs(dir); err == nil {
				extensionDirPath = abs
			}
		}
	})
	return extensionDirPath
}
