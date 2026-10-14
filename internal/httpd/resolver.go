package httpd

import (
	"net/http"

	"github.com/tfrydrychewicz/ogre/internal/app"
)

// decisionRefused is what the request log says, in place of a decision, for a
// go link requested as part of another site's page.
const decisionRefused = "refused_cross_site"

// serveResolver answers a go link. escapedPath is r.URL.EscapedPath() exactly
// as it arrived, leading slash included: nothing is cleaned, decoded or
// merged here, because "//", ".." and "%2F" may all be meaningful to the link
// model. The query string is not passed on.
func (s *Server) serveResolver(w http.ResponseWriter, r *http.Request, escapedPath string, entry *logEntry) {
	if !isRead(r.Method) {
		methodNotAllowedText(w, r)
		return
	}

	// RES-3: a browser must ask again every time, so that an edited or
	// deleted link takes effect at once. The same holds for a miss, which
	// stops being one the moment the link is created.
	w.Header().Set("Cache-Control", "no-store")

	// Refused before the link is even looked up, so the answer is the same
	// for a name that exists and one that does not (see
	// isCrossSiteSubresource).
	if isCrossSiteSubresource(r) {
		entry.decision = decisionRefused
		writeText(w, r, http.StatusForbidden,
			"403 Forbidden: go links can be opened, but not loaded into a page of another site.\n")
		return
	}

	d := s.svc.Resolve(escapedPath)
	entry.decision = d.Kind.String()
	entry.link = d.LinkName

	switch d.Kind {
	case app.DecisionHit:
		redirect(w, r, d.Destination)
	case app.DecisionHome:
		// A path of nothing but slashes, such as "//".
		redirect(w, r, internalPrefix)
	case app.DecisionMissUnknown, app.DecisionMissKnown:
		// RES-2: a miss is a 404 page at the address that missed, never a
		// redirect to a guess (page_miss.go).
		s.missPage(w, r, escapedPath, d)
	case app.DecisionBadRequest:
		s.badRequestPage(w, r)
	case app.DecisionInternalError:
		// A matched route whose destination did not expand to a URL on its
		// own host: a bug or damaged data. Never redirect; name the link so
		// it can be found, and nothing of the request (NFR-17).
		//
		// d.Err is the use case's own account of the cause. It names the
		// link, the route and at most a variable's name, never a value.
		attrs := []any{"link", truncate(d.LinkName, maxLoggedName)}
		if d.Err != nil {
			attrs = append(attrs, "error", d.Err)
		}
		s.log.Error("a matched route did not expand to a usable destination", attrs...)
		writeText(w, r, http.StatusInternalServerError, "500 Internal Server Error\n")
	default:
		s.log.Error("resolver returned an unknown decision", "kind", int(d.Kind))
		writeText(w, r, http.StatusInternalServerError, "500 Internal Server Error\n")
	}
}
