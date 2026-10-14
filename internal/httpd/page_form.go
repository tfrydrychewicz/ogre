package httpd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// The create and edit form (plan L3.6; DIR-1 to DIR-4, DIR-7, VAR-2 to
// VAR-5).
//
// The server renders the frame of the form and hands the browser module
// (static/js/form.js) the draft to start from. Everything the form then shows
// about the draft (tokens, problems, name availability, the variables and
// the preview) comes from POST /-/api/v1/links/validate as the user types; the
// module judges nothing itself (NFR-22). Saving goes through the same API as
// any other client (API-1). Editing therefore needs JavaScript, and the page
// says so in a <noscript>.

// formPage is the data of templates/form.html.
type formPage struct {
	web.Base
	// Edit is true for the edit form, false for the create form.
	Edit bool
	// Name is the name of the link being edited; "" for a new link.
	Name string
	// LinkURL is the link page of the link being edited.
	LinkURL string
	// Initial is the draft the form starts from: formDraftJSON, as JSON,
	// base64-encoded. It is written into a data attribute and never placed
	// inside a script. The encoding keeps the draft's text (a pattern taken
	// from the address, a stored description) out of the markup altogether:
	// escaping alone would make it inert, but would still leave text such as
	// "javascript:" or "onerror=" for any check of the page to trip over.
	Initial string
	// Types are the variable types the type selector offers, in order.
	Types []formTypeOption
	// MaxRoutes is the most routes a link can have, for the form's "Add a
	// route" button (linkmodel.MaxRoutes).
	MaxRoutes int
	// ForeignHosts is set when a page of another site opened the create
	// form with routes filled in: the hosts their destinations lead to, so
	// the page can warn before anything is saved. It holds "" for a route
	// without a destination that can be read.
	ForeignHosts []string
	// ForeignPrefill is true when the create form was opened that way.
	ForeignPrefill bool
	// IgnoredPrefill is true when a page of another site opened the edit
	// form with routes to add, and they were left out.
	IgnoredPrefill bool
}

// foreignHandover is the value of the edit form's "prefill" parameter with
// which the create form's "Add this route to go/x" says that the routes it
// hands over were filled in by another site (form.js, addToExisting). The
// request itself then comes from the Directory's own page, so Fetch Metadata
// alone would take the routes for the user's. Another site cannot use the
// parameter to the opposite end: a request from it to the edit form loses
// its routes whatever the parameter says, and leaving the parameter out
// only removes a warning such a request never gets.
const foreignHandover = "foreign"

// foreignHosts lists the hosts the routes' destinations lead to, for the
// "filled in by another site" warning: "" for a destination that can't be
// read.
func foreignHosts(routes []formRouteJSON) []string {
	hosts := make([]string, 0, len(routes))
	for _, rt := range routes {
		hosts = append(hosts, destinationHost(rt.Destination))
	}
	return hosts
}

// formTypeOption is one choice of the variables table's type selector. Value is
// the type's name in the link model; Label is what the selector says.
type formTypeOption struct {
	Value, Label string
}

// formVariableTypes are the types the form offers (PRD 5.3, the P0 types),
// as the link model lists them, with the labels every page uses.
func formVariableTypes() []formTypeOption {
	out := make([]formTypeOption, 0, len(linkmodel.TypeChoices))
	for _, c := range linkmodel.TypeChoices {
		out = append(out, formTypeOption{Value: c.Name, Label: c.Label})
	}
	return out
}

// formDraftJSON is the draft the form starts from. Its routes are written the
// way the API's create and update bodies are, so the module sends them back
// unchanged when the user changes nothing.
type formDraftJSON struct {
	// LinkID and Version identify the link being edited; 0 for a new link.
	LinkID      int64           `json:"linkId"`
	Version     int             `json:"version"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Routes      []formRouteJSON `json:"routes"`
	// Appended is how many routes at the end came from the address (?pattern=)
	// rather than from the saved link: the edit form opened to add them.
	Appended int `json:"appended"`
}

type formRouteJSON struct {
	ID          int64                      `json:"id"`
	Pattern     string                     `json:"pattern"`
	Destination string                     `json:"destination"`
	Variables   map[string]variableRequest `json:"variables"`
}

// formPrefilledRoutes reads the routes a form is asked to start with: each
// "pattern" of the query with the "destination" at the same position (DIR-7).
// A miss page links here with the path the user typed; a name conflict on the
// create form links to the edit form of the existing link with the routes to
// add. The text is the user's or another page's, untrusted like any input: it
// is only ever shown in form fields and checked by the validate endpoint.
//
// A destination given without a pattern still prefills one route, for the
// extension's "create a go link for this page" (DIR-7).
func formPrefilledRoutes(query url.Values) []formRouteJSON {
	patterns, destinations := query["pattern"], query["destination"]
	n := max(len(patterns), len(destinations))
	// A link has at most MaxRoutes routes; more cannot be saved, and the
	// form would only have to show them.
	n = min(n, linkmodel.MaxRoutes)
	routes := make([]formRouteJSON, 0, n)
	for i := 0; i < n; i++ {
		var r formRouteJSON
		if i < len(patterns) {
			r.Pattern = patterns[i]
		}
		if i < len(destinations) {
			r.Destination = destinations[i]
		}
		routes = append(routes, r)
	}
	return routes
}

// openedFromAnotherSite reports whether a page of another site sent the
// browser here (Sec-Fetch-Site neither "same-origin" nor "none"; every other
// port of localhost is the "same-site", and counts as another site too).
// Without Fetch Metadata (an old browser, a tool) nothing can be told, and
// the request is taken as the user's own.
//
// It matters for the forms' prefill: routes in the address are text another
// site may have chosen. A route appended to an existing link takes over the
// requests it answers more specifically than the link's own routes (PRD
// 5.5), so a page that opened the edit form of go/reports with
// "reports/10/{day:int}" and its own destination, and got one click on Save,
// would quietly send part of that link's traffic to its own host.
//
// The extension does not make a typed go link look foreign: a go link
// opened from the address bar and redirected by its rule arrives with
// "none", and so does a tab the extension opens with chrome.tabs.create. A
// link clicked on one of the extension's own pages arrives as "cross-site",
// like any other site's (e2e/fetch-metadata.spec.mjs pins all three). So no
// prefill a user starts is lost, and the rule needs no exception for the
// extension.
func openedFromAnotherSite(r *http.Request) bool {
	site, present, single := oneHeader(r, "Sec-Fetch-Site")
	return present && (!single || !isOwnSite(site))
}

// newPage is the create form, GET /-/new, optionally prefilled from
// ?pattern= and ?destination= (plan L3.6, DIR-7).
//
// A prefill from another site is kept, since that is how a page or the
// extension offers to make a go link for an address (DIR-7), but the page
// warns, naming where its destinations lead, so nobody saves another site's
// routes unseen. A new link takes no traffic that any link answers today.
func (s *Server) newPage(w http.ResponseWriter, r *http.Request) {
	draft := formDraftJSON{Routes: formPrefilledRoutes(r.URL.Query())}
	base := s.base("New link")
	base.Current = "new"
	page := formPage{Base: base}
	if len(draft.Routes) > 0 && openedFromAnotherSite(r) {
		page.ForeignPrefill, page.ForeignHosts = true, foreignHosts(draft.Routes)
	}
	if len(draft.Routes) == 0 {
		draft.Routes = []formRouteJSON{{}}
	}
	s.renderForm(w, r, page, draft)
}

// editPage is the edit form, GET /-/links/{name}/edit (plan L3.6). Routes
// given in the query are appended to the link's own: that is how the create
// form offers to add a route to a link that already has the name (DIR-4).
//
// Whether the user may edit the link is the Service's answer, the same rule
// that guards the update itself; a user who may not gets a 403 page that
// says whose link it is, instead of a form that could never be saved.
func (s *Server) editPage(w http.ResponseWriter, r *http.Request) {
	link, editable, err := s.svc.LinkByName(r.Context(), s.opts.Actor, r.PathValue("name"))
	switch {
	case errors.Is(err, app.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "No such link",
			"There is no link with this name. It may have been deleted or renamed.")
		return
	case err != nil:
		s.log.Error("loading a link to edit failed", "error", err)
		s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong",
			"The link could not be loaded. Try again in a moment.")
		return
	case !editable:
		message := "Only its owner or an administrator can change go/" + link.Name + "."
		if owner := s.svc.OwnerName(r.Context(), link.OwnerID); owner != "" {
			message = "go/" + link.Name + " belongs to " + owner + ". Ask them to change it, or to hand it over."
		}
		s.errorPage(w, r, http.StatusForbidden, "You can't edit this link", message)
		return
	}

	draft := formDraftJSON{
		LinkID: link.ID, Version: link.Version, Name: link.Name, Description: link.Description,
		Routes: make([]formRouteJSON, 0, len(link.Routes)),
	}
	for _, rt := range link.Routes {
		route := formRouteJSON{ID: rt.ID, Pattern: rt.Pattern, Destination: rt.Destination}
		for _, v := range rt.Variables {
			if v.Description == "" && v.Example == "" {
				continue
			}
			if route.Variables == nil {
				route.Variables = map[string]variableRequest{}
			}
			route.Variables[v.Name] = variableRequest{Description: v.Description, Example: v.Example}
		}
		draft.Routes = append(draft.Routes, route)
	}
	page := formPage{
		Base:    s.base("Edit go/" + link.Name),
		Edit:    true,
		Name:    link.Name,
		LinkURL: formLinkURL(link.Name),
	}
	// Routes to add come from the create form of this Directory (DIR-4).
	// Another site has no business adding routes to a link: what it asked
	// for is left out, and the page says so (see openedFromAnotherSite).
	// Routes another site put into the create form, which the user then
	// handed over with "Add this route", arrive from the Directory's own
	// page; they are kept, under the create form's warning, which the
	// user's two clicks do not make any less needed.
	added := formPrefilledRoutes(r.URL.Query())
	if len(added) > 0 && openedFromAnotherSite(r) {
		added, page.IgnoredPrefill = nil, true
	}
	added = added[:min(len(added), linkmodel.MaxRoutes-min(len(draft.Routes), linkmodel.MaxRoutes))]
	if len(added) > 0 && r.URL.Query().Get("prefill") == foreignHandover {
		page.ForeignPrefill, page.ForeignHosts = true, foreignHosts(added)
	}
	draft.Routes = append(draft.Routes, added...)
	draft.Appended = len(added)

	s.renderForm(w, r, page, draft)
}

// renderForm completes the page with what both forms share and renders it.
func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, page formPage, draft formDraftJSON) {
	for i := range draft.Routes {
		if draft.Routes[i].Variables == nil {
			draft.Routes[i].Variables = map[string]variableRequest{}
		}
	}
	initial, err := json.Marshal(draft)
	if err != nil {
		// Not reachable: every field is a string, a number or a map of them.
		s.log.Error("encoding the form's draft failed", "error", err)
		s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong", "The form could not be prepared.")
		return
	}
	page.Initial = base64.StdEncoding.EncodeToString(initial)
	page.Types = formVariableTypes()
	page.MaxRoutes = linkmodel.MaxRoutes
	s.render(w, r, http.StatusOK, "form", page)
}

// formLinkURL is the address of a link's page. A saved name needs no
// escaping, but the escape keeps the address one path segment whatever it
// holds.
func formLinkURL(name string) string {
	return internalPrefix + "links/" + url.PathEscape(name)
}
