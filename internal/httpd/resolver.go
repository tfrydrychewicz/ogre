package httpd

import (
	"net/http"
	"strings"
	"unicode"

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
	case app.DecisionMissUnknown:
		writeText(w, r, http.StatusNotFound, missUnknownPage(d))
	case app.DecisionMissKnown:
		writeText(w, r, http.StatusNotFound, missKnownPage(d))
	case app.DecisionBadRequest:
		writeText(w, r, http.StatusBadRequest, "400: this address is not valid text, so it cannot be a go link.\n")
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

// The miss pages are plain text until the web UI arrives (L3). They are built
// from the decision alone: the name the use case vetted, the suggestions it
// found and the reasons it worded. Nothing is read from the request here, so
// nothing an attacker put into an address is repeated beyond what the use
// case chose to pass on. Plain text with "nosniff" is never run as markup.

// missUnknownPage is the page for a name no link has (MISS-1): the links the
// user may have meant, each with its description.
//
// A suggestion that resolves is the whole corrected address, shown with where
// it leads. One that does not resolve names the link only, and following it
// would not explain anything: a link with a route for its bare name simply
// redirects there. So the page itself shows that link's usage help, its
// routes (PRD 5.6).
func missUnknownPage(d app.Decision) string {
	var b strings.Builder
	// The name is empty when what was asked for could not be a link name at
	// all; nothing of the request is repeated then.
	if d.LinkName == "" {
		b.WriteString("404: that is not a go link.\n")
	} else {
		b.WriteString("404: go/" + oneLine(d.LinkName) + " is not a go link yet.\n")
	}
	for _, sg := range d.Suggestions {
		b.WriteString("\nDid you mean go/" + oneLine(sg.Path) + "?")
		if sg.Description != "" {
			b.WriteString(" (" + oneLine(sg.Description) + ")")
		}
		b.WriteString("\n")
		switch {
		case sg.Resolves:
			b.WriteString("  It opens " + oneLine(sg.Destination) + "\n")
		case sg.Link != nil && len(sg.Link.Routes) > 0:
			b.WriteString("  This address does not fit it as written. go/" + oneLine(sg.Name) + " is used like this:\n")
			for _, rt := range sg.Link.Routes {
				b.WriteString("    go/" + oneLine(rt.Pattern) + "\n")
			}
		}
	}
	return b.String()
}

// missKnownPage is the page for a link that exists but has no route for this
// address (MISS-2): each route with the reason it did not fit, in the
// sentence the link model wrote.
func missKnownPage(d app.Decision) string {
	var b strings.Builder
	b.WriteString("404: go/" + oneLine(d.LinkName) + " exists, but nothing in it matches this address.\n")
	if d.Link != nil && d.Link.Description != "" {
		b.WriteString("\n" + oneLine(d.Link.Description) + "\n")
	}
	for _, reason := range d.Reasons {
		b.WriteString("\ngo/" + oneLine(reason.Pattern) + "\n")
		b.WriteString("  " + oneLine(reason.Message) + "\n")
	}
	return b.String()
}

// oneLine keeps a piece of text on its line of a plain-text page: control
// characters, line breaks among them, become spaces. A description is free
// text, and one with a line break in it could otherwise pass for a line of
// the page, such as a suggestion that was never made.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
}
