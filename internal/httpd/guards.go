package httpd

import (
	"mime"
	"net/http"
	"strings"
)

// contentSecurityPolicy forbids inline script and style and anything loaded
// from another origin, so text that slipped past escaping still cannot run
// (NFR-11). It is sent on every response; it only matters on HTML.
const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// setSecurityHeaders sets the headers every response carries. No CORS header
// is ever set anywhere: other sites must not be able to read the Directory.
func setSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", contentSecurityPolicy)
}

// isSafeMethod reports whether a method is exempt from the write guards.
// OPTIONS is exempt so that a preflight gets a plain refusal from the router
// and never an approval.
func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// rejection is a refused request: what to answer and why.
type rejection struct {
	status  int
	code    string
	message string
}

// checkWrite applies the cross-site write guards of plan section 3.8 and
// returns nil when the request may proceed.
//
// They apply only under "/-/". The resolver must stay reachable from other
// sites: a go link clicked on any web page arrives with
// Sec-Fetch-Site: cross-site (L0 spike, check 4).
//
// Each header is judged as a whole. A browser sends each of them once, so a
// request that repeats one is refused rather than judged by whichever copy
// comes first: a proxy that merges or reorders headers must not be able to
// turn a refused request into an accepted one.
func checkWrite(r *http.Request) *rejection {
	crossSite := &rejection{http.StatusForbidden, codeCrossSite,
		"This request came from another site, so it was refused."}

	// Sent by every current browser and not settable by page script. Absent
	// from curl and from older browsers, which the checks below still cover.
	if site, present, single := oneHeader(r, "Sec-Fetch-Site"); present && (!single || !isOwnSite(site)) {
		return crossSite
	}
	// Present but empty is not "absent": only a missing header is let
	// through, and anything a browser sends names an origin (or "null").
	if origin, present, single := oneHeader(r, "Origin"); present && (!single || !strings.EqualFold(origin, ownOrigin(r))) {
		return crossSite
	}
	// A cross-site form can only send three content types, none of them
	// JSON; anything else needs a preflight, which is never approved.
	if contentType, _, single := oneHeader(r, "Content-Type"); !single || !isJSON(contentType) {
		return &rejection{http.StatusUnsupportedMediaType, codeUnsupportedMediaType,
			"Send this request with Content-Type: application/json, once."}
	}
	return nil
}

// isCrossSiteSubresource reports whether a request was made by a page of
// another site for something other than showing it to the user: an image, a
// frame, a script, a fetch.
//
// The resolver refuses these. A go link answers a hit with a redirect and a
// miss with a 404, and a page that embeds one as an image or a frame can
// tell the two apart (its own content security policy reports a redirect
// that leaves the Directory) and even confirm where the link leads, all
// without reading the response. Only a navigation of the whole tab, which
// the page cannot observe, has a reason to arrive from another site: that is
// a go link being clicked.
//
// The decision rests on the Fetch Metadata headers, which browsers set and
// page script cannot. A request without them (curl, an old browser) is not a
// page's doing and is served as before.
func isCrossSiteSubresource(r *http.Request) bool {
	site, present, single := oneHeader(r, "Sec-Fetch-Site")
	if !present {
		return false
	}
	if single && isOwnSite(site) {
		return false
	}
	mode, _, modeSingle := oneHeader(r, "Sec-Fetch-Mode")
	dest, _, destSingle := oneHeader(r, "Sec-Fetch-Dest")
	topLevelNavigation := modeSingle && destSingle && mode == "navigate" && dest == "document"
	return !topLevelNavigation
}

// isOwnSite reports whether a Sec-Fetch-Site value means the request came
// from the Directory's own pages ("same-origin") or from the user directly,
// by typing an address or opening a bookmark ("none"). "same-site" is not
// enough: every other port on localhost is the same site.
func isOwnSite(secFetchSite string) bool {
	return secFetchSite == "same-origin" || secFetchSite == "none"
}

// oneHeader returns a header that a well-formed request carries at most
// once. present says whether it was sent at all, even with an empty value;
// single is false when it was sent more than once.
func oneHeader(r *http.Request, name string) (value string, present, single bool) {
	values := r.Header.Values(name)
	switch len(values) {
	case 0:
		return "", false, false
	case 1:
		return values[0], true, true
	default:
		return "", true, false
	}
}

// ownOrigin is the origin a same-origin page would report for this request.
// The Host header has already passed the allow-list, so it can be trusted to
// name this Directory.
func ownOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// isJSON reports whether a Content-Type header is application/json, with or
// without parameters such as charset.
func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}
