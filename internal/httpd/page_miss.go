package httpd

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// The miss page (plan L3.8, PRD 5.6 and 6.5): what a go link that did not
// resolve shows, rendered by the resolver at the address that missed, with
// status 404, and complete without JavaScript.
//
// Everything on it comes from the decision, except one thing: the path the
// user typed, which the page repeats so the user sees what was asked and can
// create it (MISS-3). That path is shown to the user and nowhere else. It is
// never logged (NFR-17), and the page is sent with Referrer-Policy:
// no-referrer, so following a suggestion or a destination from it does not
// carry the missed address along either.
//
// The page repeats the typed path only when its first segment could be a
// link name, the rule the use case already applies to the name
// (linkmodel.PossibleName). An address that cannot be a link at all is
// answered without repeating any of it.

// missTypedMaxRunes is how much of a typed path the page shows. A request
// line can be many kilobytes; past this the text is cut with an ellipsis.
const missTypedMaxRunes = 200

// missData is the data of templates/miss.html.
type missData struct {
	web.Base
	// Kind is "unknown" (no link has the name), "known" (the link exists,
	// none of its routes fits) or "nameless" (the address cannot be a link).
	Kind string
	// Typed is the path the user typed, after "go/", as it is shown: each
	// segment decoded when that only makes it easier to read (missShowPath).
	// Empty when Kind is "nameless".
	Typed string
	// Name is the requested name (unknown) or the link's name (known).
	Name string
	// CreatePath is what the create form opens with, and what its button
	// names: the typed path, or the name alone when the path is too long
	// to be a pattern. Empty when nothing can be offered.
	CreatePath string
	// CreateURL is the create form, prefilled with CreatePath.
	CreateURL string
	// Suggestions are the "Did you mean" entries of an unknown name, best
	// first.
	Suggestions []missSuggestion
	// Description, LinkURL and Routes describe the link of a known name.
	Description string
	LinkURL     string
	Routes      []missRoute
}

// missSuggestion is one "Did you mean" entry.
type missSuggestion struct {
	// Path is the suggested go link as shown, after "go/".
	Path string
	// Href is where following it goes: the corrected address itself when
	// it resolves, and the link's page (its usage help) when it does not.
	Href        string
	Resolves    bool
	Description string
	// Destination is where the corrected address leads; only when Resolves.
	Destination string
	// Patterns are the link's routes, shown when the user's values do not
	// fit it, so the page itself says how the link is used.
	Patterns [][]patternPart
}

// missRoute is one route of a known link with the reason the request did
// not fit it. The reason is the link model's sentence, which already names
// the variable, what it accepts, and its description and example where the
// link has them.
type missRoute struct {
	Pattern []patternPart
	Reason  string
}

// noReferrerWriter sets the headers that belong to a miss page alone, at the
// moment the status is written, after Render has set the page headers: it
// replaces the Directory's Referrer-Policy (same-origin) with no-referrer,
// and keeps the page out of search indexes. The miss page makes no write
// through the API, so the Origin: null that no-referrer causes on writes
// (see web.ReferrerPolicy) does not matter here.
type noReferrerWriter struct{ http.ResponseWriter }

func (w noReferrerWriter) WriteHeader(status int) {
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	w.ResponseWriter.WriteHeader(status)
}

func (w noReferrerWriter) Write(b []byte) (int, error) { return w.ResponseWriter.Write(b) }

// missPage renders the page of an unknown-name or known-name miss.
// escapedPath is the request's raw path, leading slash included.
func (s *Server) missPage(w http.ResponseWriter, r *http.Request, escapedPath string, d app.Decision) {
	data := missData{Base: s.base("")}
	// The page has a search box of its own, beside the other ways on.
	data.HideHeaderSearch = true
	switch {
	case d.Kind == app.DecisionMissKnown:
		data.Kind = "known"
		data.Name = d.LinkName
		data.Typed = missShowPath(escapedPath)
		data.Title = "go/" + d.LinkName + ": no route fits"
		data.LinkURL = internalPrefix + "links/" + url.PathEscape(d.LinkName)
		if d.Link != nil {
			data.Description = d.Link.Description
		}
		data.Routes = missRoutes(d)
		// MISS-3 holds for both kinds of miss: what was typed can become a
		// route. The create form then finds the name taken and offers to add
		// the route to this link, or says whose link it is (DIR-4).
		data.CreatePath, data.CreateURL = missCreate(data.Typed, d.LinkName)
	case d.LinkName == "":
		data.Kind = "nameless"
		data.Title = "Not a go link"
		data.CreateURL = internalPrefix + "new"
	default:
		data.Kind = "unknown"
		data.Name = d.LinkName
		data.Typed = missShowPath(escapedPath)
		data.Title = "No link go/" + d.LinkName
		data.CreatePath, data.CreateURL = missCreate(data.Typed, d.LinkName)
		for _, sg := range d.Suggestions {
			data.Suggestions = append(data.Suggestions, missSuggestionOf(sg))
		}
	}
	data.Typed = missCut(data.Typed)
	s.render(noReferrerWriter{w}, r, http.StatusNotFound, "miss", data)
}

// missCreate is what a miss page offers to create (MISS-3): the create form
// gets the whole typed path when it could be a pattern's length, and the form
// then says what else to change. A longer one could not be saved anyway, so
// the name alone is offered.
func missCreate(typed, name string) (createPath, createURL string) {
	createPath = typed
	if utf8.RuneCountInString(createPath) > linkmodel.MaxPatternLength {
		createPath = name
	}
	return createPath, internalPrefix + "new?pattern=" + url.QueryEscape(createPath)
}

// badRequestPage answers an address that is not valid text (a malformed
// escape, or bytes that are not UTF-8), so there is nothing to look up. It
// repeats nothing of it.
func (s *Server) badRequestPage(w http.ResponseWriter, r *http.Request) {
	s.errorPage(noReferrerWriter{w}, r, http.StatusBadRequest, "Not a go link",
		"This address is not valid text, so it cannot be a go link. Check how it was copied, or search the Directory for the link you want.")
}

// missSuggestionOf turns one suggestion of the use case into what the page
// shows.
func missSuggestionOf(sg app.Suggestion) missSuggestion {
	out := missSuggestion{
		Path:        missCut(missShowPath(sg.Path)),
		Resolves:    sg.Resolves,
		Description: sg.Description,
	}
	if sg.Resolves && !strings.HasPrefix(sg.Path, "/") {
		// Following the corrected address resolves it, values and all
		// (PRD scenario 4). sg.Path is percent-encoded like a request path
		// and starts with the name, never with "/", so "/"+Path stays on
		// this Directory; the check is there in case that ever changes.
		out.Href = "/" + sg.Path
		out.Destination = sg.Destination
		return out
	}
	// PRD 5.6: a suggestion that does not resolve opens the link's usage
	// help. Its own address would not: a link with a route for its bare
	// name would simply redirect there.
	out.Path = missCut(sg.Name)
	out.Href = internalPrefix + "links/" + url.PathEscape(sg.Name)
	if sg.Link != nil {
		for _, rt := range sg.Link.Routes {
			out.Patterns = append(out.Patterns, patternParts(rt.Pattern))
		}
	}
	return out
}

// missRoutes pairs each route of a known link with the reason it gave, in
// route order. The reason's sentence is the link model's own (MISS-2); the
// page adds nothing to it.
func missRoutes(d app.Decision) []missRoute {
	routes := make([]missRoute, 0, len(d.Reasons))
	for _, reason := range d.Reasons {
		routes = append(routes, missRoute{Pattern: patternParts(reason.Pattern), Reason: reason.Message})
	}
	return routes
}

// missShowPath turns a raw request path into the text the page shows after
// "go/": the segments the link model splits it into, joined by "/" (so a
// trailing slash or a doubled one is not shown).
//
// A segment is shown decoded when every character of it reads the same in a
// URL: letters, digits and marks of any script, and the ASCII punctuation a
// path may hold as it is. "reports/10/13" and "wiki/Zürich" show as typed.
// Anything else — an encoded "/" or "%", a space, a line break, an invisible
// or direction-changing character — keeps its percent-encoding, so that what
// is shown is exactly one address, cannot rearrange the text around it, and
// can be typed again. The page escapes the result like any other text.
func missShowPath(escapedPath string) string {
	segments, err := linkmodel.SplitPath(escapedPath)
	if err != nil {
		// The resolver answers such a path as a bad request, without this
		// page; shown as it came, it is still ASCII.
		return strings.Trim(escapedPath, "/")
	}
	for i, seg := range segments {
		if !missReadable(seg) {
			segments[i] = url.PathEscape(seg)
		}
	}
	return strings.Join(segments, "/")
}

// missReadable reports whether a decoded segment can be shown as it is.
func missReadable(seg string) bool {
	for _, r := range seg {
		switch {
		case r < utf8.RuneSelf:
			if !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' ||
				strings.ContainsRune("-._~!$&'()*+,;=:@", r)) {
				return false
			}
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r):
		default:
			return false
		}
	}
	return true
}

// missCut shortens text for showing to missTypedMaxRunes characters and an
// ellipsis.
func missCut(s string) string {
	n := 0
	for i := range s {
		if n == missTypedMaxRunes {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
