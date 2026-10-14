package web

import "net/http"

// ContentSecurityPolicy is sent with every response of the Directory (NFR-11).
//
// Everything is denied unless named: scripts, styles and images come from
// the Directory itself, and pages talk only to their own API. There is no
// 'unsafe-inline' anywhere, so text that slipped past escaping still cannot
// run or restyle a page, and no inline script, style element, style
// attribute or on* handler may appear in a template. "data:" images are
// allowed for small icons drawn by the stylesheets. form-action keeps forms
// on the Directory, base-uri stops an injected <base> from re-pointing the
// relative URLs of a page, and frame-ancestors stops other sites from
// framing it.
//
// The last two directives make the rule of plan section 3.6 (untrusted text
// enters the DOM only as text) hold at run time too, in browsers that know
// Trusted Types (Chromium): with no policy allowed ('none'), every sink that
// turns a string into markup or script (innerHTML, outerHTML,
// insertAdjacentHTML, document.write, srcdoc, DOMParser,
// Range.createContextualFragment and the like) throws instead. "make
// dom-guard" bans the same sinks in the source; this catches whatever a
// pattern there misses, and the end-to-end tests fail on the violation.
const ContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; " +
	"require-trusted-types-for 'script'; trusted-types 'none'"

// ReferrerPolicy is "same-origin", chosen in L1 and kept on purpose.
//
// A go link's address carries values (a ticket number, a customer name), so
// it must not reach the destination as a Referer. same-origin already
// guarantees that: a redirect from the Directory to another site is a
// cross-origin request, which gets no Referer at all. "no-referrer" would
// leak nothing more, but it would break the Directory's own writes: for a
// same-origin fetch that is not a GET or HEAD, a browser serializes the
// Origin header as "null" under no-referrer, and the write guard rightly
// refuses an Origin that is not the Directory's.
const ReferrerPolicy = "same-origin"

// SetSecurityHeaders sets the headers every response of the Directory
// carries, pages or not: the content security policy, no MIME sniffing, the
// referrer policy and a refusal to be framed. The HTTP layer calls it before
// anything else; Render calls it again so a page is safe even when served by
// some other path. Set is idempotent, so calling it twice is harmless.
//
// No CORS header is set here or anywhere: other sites must not read the
// Directory.
func SetSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", ContentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", ReferrerPolicy)
	// frame-ancestors says the same to current browsers; this header is for
	// those that predate it.
	h.Set("X-Frame-Options", "DENY")
}

// SetPageHeaders sets the headers of an HTML page: the security headers and
// no caching, since a page shows links that change (an edited or deleted
// link must show at once, and a miss page stops being one the moment the
// link is created).
func SetPageHeaders(h http.Header) {
	SetSecurityHeaders(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
}
