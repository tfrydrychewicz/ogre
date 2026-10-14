package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// The link page (plan L3.4, DIR-6) and the delete confirmation (L3.7), and
// the views of a link that the home page shares with them.
//
// Nothing here reads a pattern. What a pattern is made of comes from the link
// model (linkmodel.PatternTokens, linkmodel.RouteVariables); this file only
// arranges it for the templates.

// patternPart is a piece of a pattern's text as a page shows it: a variable,
// highlighted, or the text around variables.
type patternPart struct {
	Text string
	Var  bool
}

// patternParts cuts a stored pattern into the parts a page highlights, by the
// tokens the link model reports. The text between tokens (the "/" between
// segments) is kept as it is, so the parts always add up to the pattern.
func patternParts(pattern string) []patternPart {
	var parts []patternPart
	plain := func(text string) {
		if text == "" {
			return
		}
		if n := len(parts); n > 0 && !parts[n-1].Var {
			parts[n-1].Text += text
			return
		}
		parts = append(parts, patternPart{Text: text})
	}
	at := 0
	for _, t := range linkmodel.PatternTokens(pattern) {
		if t.Start < at || t.End > len(pattern) || t.Start > t.End {
			continue // never for a stored pattern; the text is shown regardless
		}
		plain(pattern[at:t.Start])
		if t.Kind == linkmodel.TokenVariable {
			parts = append(parts, patternPart{Text: pattern[t.Start:t.End], Var: true})
		} else {
			plain(pattern[t.Start:t.End])
		}
		at = t.End
	}
	plain(pattern[at:])
	return parts
}

// routeView is one route of a link as the pages show it.
type routeView struct {
	ID          int64
	Pattern     string
	Parts       []patternPart
	Destination string
	// Host is the destination's host, for a short summary in a list; "" when
	// it cannot be read.
	Host      string
	Variables []variableView
}

// variableView is a variable of a route as the pages show it: the link
// model's description, with the type in the words the form uses for it.
type variableView struct {
	linkmodel.VariableInfo
	TypeLabel string
}

func newRouteView(rt linkmodel.Route) routeView {
	view := routeView{
		ID:          rt.ID,
		Pattern:     rt.Pattern,
		Parts:       patternParts(rt.Pattern),
		Destination: rt.Destination,
		Host:        destinationHost(rt.Destination),
	}
	for _, v := range linkmodel.RouteVariables(rt) {
		view.Variables = append(view.Variables, variableView{VariableInfo: v, TypeLabel: linkmodel.TypeLabel(v.Type)})
	}
	return view
}

// destinationHost returns the host of a destination template. Placeholders
// never stand in the host (PRD 5.4), so the template parses as a URL; a
// stored destination that does not is simply shown without a host.
func destinationHost(destination string) string {
	u, err := url.Parse(destination)
	if err != nil {
		return ""
	}
	return u.Host
}

// hasVariables reports whether any route of the link has a variable: such a
// link cannot be opened without values, so lists send the user to its page.
func hasVariables(routes []routeView) bool {
	for _, rt := range routes {
		if len(rt.Variables) > 0 {
			return true
		}
	}
	return false
}

// linkPageData is the data of templates/link.html.
type linkPageData struct {
	web.Base
	// NotFound is set when no link has the name; Missing is then the name,
	// when it could be one (and so is worth offering to create), or "".
	NotFound bool
	Missing  string

	Name        string
	Description string
	Owner       string
	Editable    bool
	Routes      []routeView
	// HasVariables is true when the launcher has fields to show.
	HasVariables bool
	// FullURL is the go link as an address of this Directory.
	FullURL   string
	CreatedAt time.Time
	UpdatedAt time.Time
	// LauncherRoutes is the routes as the launcher reads them (launcher.js):
	// the link JSON's routes plus each pattern's tokens. The template puts it
	// in an attribute, escaped there like any other text.
	LauncherRoutes string
}

// routeWithTokensJSON is a route as the API returns it, plus the tokens of its
// pattern in the validate endpoint's shape (UTF-16 offsets): the launcher puts
// values in place of the variables, and search results highlight them,
// without reading the pattern themselves.
type routeWithTokensJSON struct {
	routeJSON
	Tokens []tokenJSON `json:"tokens"`
}

// linkPage is a link's page, GET /-/links/{name} (plan L3.4). The name is
// found ignoring case. An unknown name is a 404 page that offers to create
// the link.
func (s *Server) linkPage(w http.ResponseWriter, r *http.Request) {
	link, editable, ok := s.pageLink(w, r, "link")
	if !ok {
		return
	}
	routes := make([]routeView, 0, len(link.Routes))
	for _, rt := range link.Routes {
		routes = append(routes, newRouteView(rt))
	}
	data := linkPageData{
		Base:         s.base("go/" + link.Name),
		Name:         link.Name,
		Description:  link.Description,
		Owner:        s.svc.OwnerName(r.Context(), link.OwnerID),
		Editable:     editable,
		Routes:       routes,
		HasVariables: hasVariables(routes),
		FullURL:      linkAddress(r, link.Name),
		CreatedAt:    link.CreatedAt,
		UpdatedAt:    link.UpdatedAt,
	}
	launcher := make([]routeWithTokensJSON, 0, len(link.Routes))
	for i, rt := range s.linkJSON(r, link).Routes {
		launcher = append(launcher, routeWithTokensJSON{
			routeJSON: rt,
			Tokens:    tokensJSON(linkmodel.PatternTokens(link.Routes[i].Pattern), link.Routes[i].Pattern),
		})
	}
	encoded, err := json.Marshal(launcher)
	if err != nil {
		s.log.Error("encoding the launcher's routes failed", "link", link.Name, "error", err)
		s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong", "The link could not be shown. Try again.")
		return
	}
	data.LauncherRoutes = string(encoded)
	s.render(w, r, http.StatusOK, "link", data)
}

// deletePageData is the data of templates/delete.html.
type deletePageData struct {
	web.Base
	ID int64
	// LinkVersion is the link's version, sent with the DELETE. It is not
	// named Version, which would hide Base.Version from the layout.
	LinkVersion int
	Name        string
	Description string
	Owner       string
	Editable    bool
	Routes      []routeView
}

// linkDeletePage confirms the deletion of a link, GET
// /-/links/{name}/delete (plan L3.7). The deletion itself is a DELETE of the
// API, sent from the page's script with the version shown here, so a link
// changed in the meantime is not deleted unseen.
func (s *Server) linkDeletePage(w http.ResponseWriter, r *http.Request) {
	link, editable, ok := s.pageLink(w, r, "delete")
	if !ok {
		return
	}
	routes := make([]routeView, 0, len(link.Routes))
	for _, rt := range link.Routes {
		routes = append(routes, newRouteView(rt))
	}
	// Someone who may not delete the link gets the page with the reason and
	// no button, as a 403, like the edit form's refusal.
	status := http.StatusOK
	if !editable {
		status = http.StatusForbidden
	}
	s.render(w, r, status, "delete", deletePageData{
		Base:        s.base("Delete go/" + link.Name),
		ID:          link.ID,
		LinkVersion: link.Version,
		Name:        link.Name,
		Description: link.Description,
		Owner:       s.svc.OwnerName(r.Context(), link.OwnerID),
		Editable:    editable,
		Routes:      routes,
	})
}

// pageLink finds the link a page is about, by the {name} of its path. When
// there is none it has already answered, with the link page's 404 (for the
// link page itself and the pages below it alike), and ok is false.
func (s *Server) pageLink(w http.ResponseWriter, r *http.Request, page string) (link linkmodel.Link, editable bool, ok bool) {
	name := r.PathValue("name")
	link, editable, err := s.svc.LinkByName(r.Context(), s.opts.Actor, name)
	switch {
	case errors.Is(err, app.ErrNotFound):
		s.linkNotFoundPage(w, r, name)
		return linkmodel.Link{}, false, false
	case err != nil:
		// The name is not logged: it is the request's text, and may be
		// anything (NFR-17).
		s.log.Error("reading a link for a page failed", "page", page, "error", err)
		s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong",
			"The link could not be read. Try again in a moment.")
		return linkmodel.Link{}, false, false
	}
	return link, editable, true
}

// linkNotFoundPage answers a link page for a name no link has, with 404. The
// name is repeated, and offered for a new link, only when it could be a
// name at all: anything else from the address is not worth showing and is
// not echoed (linkmodel.PossibleName).
func (s *Server) linkNotFoundPage(w http.ResponseWriter, r *http.Request, name string) {
	data := linkPageData{Base: s.base("Link not found"), NotFound: true}
	if linkmodel.PossibleName(name) {
		data.Missing = linkmodel.NormalizeName(name)
	}
	s.render(w, r, http.StatusNotFound, "link", data)
}

// linkAddress is the address of a go link on this Directory, as the user
// reached it: the request's own scheme and host (which the host check has
// already accepted) and the link's name.
func linkAddress(r *http.Request, name string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: r.Host, Path: "/" + name}
	return u.String()
}
