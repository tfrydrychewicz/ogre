package httpd

import (
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// maxReasonValue bounds, in bytes, the request's own text repeated in a
// reason. A known-name miss repeats the refused segment once per route, and a
// segment can be as long as the request line allows; the message beside it
// already quotes a short form.
const maxReasonValue = 200

// The resolver's decision as JSON (plan section 3.5). Each kind has its own
// type so that a response carries exactly the fields of its kind.
type (
	resolveKindJSON struct {
		Kind string `json:"kind"`
	}

	resolveHitJSON struct {
		Kind        string            `json:"kind"`
		Link        resolveLinkJSON   `json:"link"`
		Route       resolveRouteJSON  `json:"route"`
		Values      map[string]string `json:"values"`
		Destination string            `json:"destination"`
	}

	resolveMissKnownJSON struct {
		Kind   string                 `json:"kind"`
		Link   resolveLinkJSON        `json:"link"`
		Routes []resolveRouteMissJSON `json:"routes"`
	}

	resolveMissUnknownJSON struct {
		Kind string `json:"kind"`
		// RequestedName is "" when what was asked for could not be a link
		// name at all.
		RequestedName string           `json:"requestedName"`
		Suggestions   []suggestionJSON `json:"suggestions"`
	}

	resolveLinkJSON struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	resolveRouteJSON struct {
		ID      int64  `json:"id"`
		Pattern string `json:"pattern"`
	}

	resolveRouteMissJSON struct {
		ID      int64      `json:"id"`
		Pattern string     `json:"pattern"`
		Reason  reasonJSON `json:"reason"`
	}

	// reasonJSON says why a route did not fit. Message is the sentence to
	// show; the other fields are for a client that wants to point at the
	// variable.
	reasonJSON struct {
		Code     string `json:"code"`
		Variable string `json:"variable"`
		Value    string `json:"value"`
		Accepts  string `json:"accepts"`
		Message  string `json:"message"`
	}

	suggestionJSON struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Edits       int    `json:"edits"`
		Resolves    bool   `json:"resolves"`
		Description string `json:"description"`
		// Destination is "" unless Resolves.
		Destination string `json:"destination"`
	}
)

// handleResolve answers GET /resolve?path=: what the resolver would do with a
// go link, without redirecting.
//
// The path parameter is the go-link path exactly as it would follow "go/" in
// the address bar, still percent-encoded, and then encoded once more as a
// query value, the way encodeURIComponent or URLSearchParams does it. The
// query string is decoded exactly once here and the result is handed to
// Service.Resolve unchanged; the link model then splits it on "/" and decodes
// each segment, as it does for a real request. So:
//
//	?path=reports/10/13      -> "reports/10/13": three segments
//	?path=reports%2F10%2F13  -> "reports/10/13": the same three segments
//	?path=docs%2Fa%252Fb     -> "docs/a%2Fb": two segments, the second "a/b"
//	?path=sale%2F100%2525    -> "sale/100%25": the value "100%"
//	?path=sale%2F100%25      -> "sale/100%": a malformed path, kind "bad_request"
//	?path=a+b                -> "a b": "+" is a space in a query string
//	?path=                   -> "": kind "home"
//
// A leading slash is allowed and changes nothing. Whatever follows a "?" or
// "#" in the decoded value is not part of the path, as in an address.
//
// The request itself is malformed, and answers 400, when the query string
// cannot be decoded or path is not given exactly once. Every decision the
// resolver can reach, "bad_request" included, is a successful answer and
// comes with 200; only a destination that failed to expand answers 500.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, codeMalformed, "The query string is not valid.", nil)
		return
	}
	paths := query["path"]
	if len(paths) != 1 {
		writeError(w, r, http.StatusBadRequest, codeMalformed,
			"Give the go-link path in the \"path\" parameter, once.", nil)
		return
	}

	d := s.svc.Resolve(paths[0])
	switch {
	case d.Kind == app.DecisionHome, d.Kind == app.DecisionBadRequest:
		writeJSON(w, r, http.StatusOK, resolveKindJSON{Kind: d.Kind.String()})
	case d.Kind == app.DecisionHit && d.Link != nil && d.Route != nil:
		values := d.Values
		if values == nil {
			values = map[string]string{}
		}
		writeJSON(w, r, http.StatusOK, resolveHitJSON{
			Kind:        d.Kind.String(),
			Link:        newResolveLinkJSON(d.Link),
			Route:       resolveRouteJSON{ID: d.Route.ID, Pattern: d.Route.Pattern},
			Values:      values,
			Destination: d.Destination,
		})
	case d.Kind == app.DecisionMissKnown && d.Link != nil:
		out := resolveMissKnownJSON{
			Kind:   d.Kind.String(),
			Link:   newResolveLinkJSON(d.Link),
			Routes: make([]resolveRouteMissJSON, 0, len(d.Reasons)),
		}
		for _, reason := range d.Reasons {
			out.Routes = append(out.Routes, resolveRouteMissJSON{
				ID:      reason.RouteID,
				Pattern: reason.Pattern,
				Reason:  newReasonJSON(reason),
			})
		}
		writeJSON(w, r, http.StatusOK, out)
	case d.Kind == app.DecisionMissUnknown:
		out := resolveMissUnknownJSON{
			Kind:          d.Kind.String(),
			RequestedName: d.RequestedName,
			Suggestions:   make([]suggestionJSON, 0, len(d.Suggestions)),
		}
		for _, sg := range d.Suggestions {
			out.Suggestions = append(out.Suggestions, suggestionJSON{
				Name: sg.Name, Path: sg.Path, Edits: sg.Edits, Resolves: sg.Resolves,
				Description: sg.Description, Destination: sg.Destination,
			})
		}
		writeJSON(w, r, http.StatusOK, out)
	default:
		// DecisionInternalError, a kind this server does not know, or a
		// decision without the link it claims to be about. Only the kind
		// and the link name are logged, never the path (NFR-17).
		attrs := []any{"decision", d.Kind.String(), "link", truncate(d.LinkName, maxLoggedName)}
		if d.Err != nil {
			attrs = append(attrs, "error", d.Err) // names link and route, never a value
		}
		s.log.Error("resolve produced no usable decision", attrs...)
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Something went wrong on the server.", nil)
	}
}

func newResolveLinkJSON(l *linkmodel.Link) resolveLinkJSON {
	return resolveLinkJSON{ID: l.ID, Name: l.Name, Description: l.Description}
}

func newReasonJSON(r linkmodel.Reason) reasonJSON {
	return reasonJSON{
		Code:     r.Code,
		Variable: r.Variable,
		Value:    cutText(r.Value, maxReasonValue),
		Accepts:  r.Accepts,
		Message:  r.Message,
	}
}

// cutText returns s cut to at most n bytes without splitting a character.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
