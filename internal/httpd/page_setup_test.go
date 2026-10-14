package httpd

import (
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The setup page (L3.9): every part of the plan's list, in order, readable
// without JavaScript, pointing the search shortcut at this Directory.
func TestSetupPage(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, nil).Handler(), "/-/setup")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	assertObeysPolicy(t, body)
	assertSafeHrefs(t, body)

	text := missText(body)
	at := 0
	for _, want := range []string{
		"Type go links in your browser", "This Directory answers go links at http://localhost:4655.",
		// How to load the extension.
		"1. Load the extension", "chrome://extensions", "Developer mode", "Load unpacked",
		"Choose this folder: " + filepath.Join(setupRepoRoot(t), "extension"),
		// How to check that it works.
		"2. Check that it works", "Type go/ in the address bar, with the slash, and press Enter.",
		// When Chrome searches instead.
		"3. If Chrome searches instead of opening the link", "go/name/", "http://go/name",
		// Incognito: what L0 saw, how to allow it, and that it is untested.
		"4. Incognito windows", "ends on Chrome's error page", "Details", "Allow in Incognito",
		"Not tested yet", "has not been tried",
		// The extension-free alternative.
		"5. Without the extension: a search shortcut", "chrome://settings/searchEngines",
		"Shortcut go", "URL with %s in place of query http://localhost:4655/%s",
	} {
		i := strings.Index(text[at:], want)
		if i < 0 {
			t.Errorf("the page lacks %q after position %d:\n%s", want, at, text)
			continue
		}
		at += i + len(want)
	}
	if strings.Contains(text, "won't reach this Directory") {
		t.Error("the page warns, but this Directory runs where the extension expects it")
	}
	for _, want := range []string{
		`<a href="/-/setup" aria-current="page">Setup</a>`,
		`data-copy="chrome://extensions" aria-label="Copy chrome://extensions" hidden>Copy</button>`,
		`data-copy="http://localhost:4655/%s"`,
		`<script type="module" src="` + assetURL(t, "js/setup.js") + `"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the markup lacks %s", want)
		}
	}
	// Without JavaScript, nothing that needs it shows: every copy button
	// starts hidden.
	for _, button := range regexp.MustCompile(`<button[^>]*data-copy[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(button, " hidden") {
			t.Errorf("a copy button is visible without JavaScript: %s", button)
		}
	}
}

// The shortcut points at the configured address, and the page warns when
// the extension, which knows only the default address, would miss it.
func TestSetupPageAddress(t *testing.T) {
	tests := []struct {
		addr     string
		wantURL  string
		wantWarn bool
	}{
		{"localhost:4655", "http://localhost:4655", false},
		{":4655", "http://localhost:4655", false},
		{"0.0.0.0:4655", "http://localhost:4655", false},
		{"[::]:4655", "http://localhost:4655", false},
		// The extension's http://localhost:4655 reaches these as well.
		{"127.0.0.1:4655", "http://127.0.0.1:4655", false},
		{"[::1]:4655", "http://[::1]:4655", false},
		{"LocalHost:4655", "http://LocalHost:4655", false},
		// localhost is not 127.0.0.2, and another port is another server.
		{"127.0.0.2:4655", "http://127.0.0.2:4655", true},
		{"localhost:8080", "http://localhost:8080", true},
		{"127.0.0.1:8080", "http://127.0.0.1:8080", true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			s := newTestServer(&fakeService{}, func(o *Options) { o.Addr = tt.addr })
			if got := s.configuredBaseURL(); got != tt.wantURL {
				t.Errorf("configuredBaseURL() = %q, want %q", got, tt.wantURL)
			}
			_, port, err := net.SplitHostPort(tt.addr)
			if err != nil {
				t.Fatal(err)
			}
			rec := request{target: "/-/setup", host: "localhost:" + port}.send(s.Handler())
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			text := missText(rec.Body.String())
			if !strings.Contains(text, tt.wantURL+"/%s") {
				t.Errorf("the shortcut is not %s/%%s:\n%s", tt.wantURL, text)
			}
			if got := strings.Contains(text, "Warning: the extension won't reach this Directory"); got != tt.wantWarn {
				t.Errorf("warning shown = %v, want %v", got, tt.wantWarn)
			}
		})
	}
}

// setupRepoRoot is the checkout the tests run in: two levels above this package.
func setupRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
