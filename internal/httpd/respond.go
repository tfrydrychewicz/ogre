package httpd

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Error codes of the API. Clients key on these; messages may be reworded.
const (
	codeMalformed            = "request.malformed"
	codeTooLarge             = "request.too_large"
	codeUnsupportedMediaType = "request.unsupported_media_type"
	codeCrossSite            = "request.cross_site"
	codeMethodNotAllowed     = "request.method_not_allowed"
	codeNotFound             = "not_found" // no such endpoint or page
	codeInternal             = "internal"
	codeLinkInvalid          = "link.invalid"
	codeLinkNotFound         = "link.not_found"
	codeNameTaken            = "link.name_taken"
	codeVersionConflict      = "link.version_conflict"
	codeForbidden            = "link.forbidden"
)

// writeText writes a plain-text response. HEAD gets the same headers,
// Content-Length included, and no body.
func writeText(w http.ResponseWriter, r *http.Request, status int, body string) {
	writeBody(w, r, status, "text/plain; charset=utf-8", []byte(body))
}

// writeJSON writes v as JSON. API responses are never cached: a list fetched
// after a save must show the save.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// Only reachable through a programming error in the types below.
		status, body = http.StatusInternalServerError, []byte(`{"error":{"code":"internal","message":"The server could not build its answer.","details":{}}}`)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeBody(w, r, status, "application/json", append(body, '\n'))
}

func writeBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// redirect answers 302. It does not use http.Redirect, which would add an
// HTML body and resolve the target against the request path; the target here
// is either "/-/" or a destination the link model already validated as an
// absolute URL.
//
// The destination is sent as it was saved, except that bytes outside ASCII
// are percent-encoded: a header value has to be ASCII to mean the same thing
// to every browser, proxy and script (RES-6), and a saved destination may
// spell a path as "/żółć". Encoding the UTF-8 bytes is what a browser would
// do with that address itself, so the page reached is the same.
func redirect(w http.ResponseWriter, r *http.Request, location string) {
	location = asciiURL(location)
	w.Header().Set("Location", location)
	writeText(w, r, http.StatusFound, "Redirecting to "+location+"\n")
}

// asciiURL percent-encodes every byte of u that is not printable ASCII and
// leaves the rest untouched, existing escapes included.
func asciiURL(u string) string {
	clean := true
	for i := 0; i < len(u); i++ {
		if c := u[i]; c <= ' ' || c >= 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return u // nearly every destination: nothing to encode, nothing allocated
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(u) + 16)
	for i := 0; i < len(u); i++ {
		if c := u[i]; c <= ' ' || c >= 0x7f {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// methodNotAllowedText is the 405 of the resolver and the home redirect,
// which accept GET and HEAD only. It is how a cross-site form posting to a
// go link is refused (plan section 3.8).
func methodNotAllowedText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeText(w, r, http.StatusMethodNotAllowed, "405 Method Not Allowed\n")
}

// errorBody is the one error shape of the API (plan section 3.5).
type errorBody struct {
	Error errorJSON `json:"error"`
}

type errorJSON struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// writeError writes an API error. details may be nil; the field is always an
// object so clients need no null check.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, r, status, errorBody{Error: errorJSON{Code: code, Message: message, Details: details}})
}

// byMethod routes one path by method and answers 405 with an Allow header for
// the rest. ServeMux's own method patterns are not used because a catch-all
// for unknown API paths would swallow the 405.
type byMethod map[string]http.HandlerFunc

func (m byMethod) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet // handlers write through writeBody, which drops the body
	}
	if h, ok := m[method]; ok {
		h(w, r)
		return
	}
	w.Header().Set("Allow", m.allow())
	writeError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed,
		"This address does not accept "+r.Method+" requests.", nil)
}

func (m byMethod) allow() string {
	methods := make([]string, 0, len(m)+1)
	for method := range m {
		methods = append(methods, method)
		if method == http.MethodGet {
			methods = append(methods, http.MethodHead)
		}
	}
	sort.Strings(methods)
	return strings.Join(methods, ", ")
}
