package httpd

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/web"
)

// The home page (plan L3.3, SRCH-1 to SRCH-4): a search box with focus, and
// below it the user's own links and the most recently added ones.
//
// The page works without JavaScript: the search form is a GET of this page
// with ?q=, and the server renders the ranked results. With JavaScript,
// search.js asks the API as the user types and renders the same markup.

const (
	// homeListLimit is the length of "Your links" and "Recently added".
	homeListLimit = 10
	// homeSearchLimit is the number of search results shown; the API's
	// default, so both ways of searching show the same list.
	homeSearchLimit = app.DefaultListLimit
	// homeRoutesShown is how many routes of a link a list shows; the rest
	// are counted. A link has at most 20 routes, which would swamp a list.
	homeRoutesShown = 3
)

// linkItem is a link in a list of the home page, and a search result.
type linkItem struct {
	Name        string
	Description string
	Owner       string
	// Rank is why search found the link (app.Rank*); "" outside search.
	Rank string
	// Href is where the link's entry leads: the go link itself for a link
	// without variables, which opens at once (SRCH-4), and otherwise the
	// link page with its launcher (#launch).
	Href   string
	Direct bool
	Routes []routeView
	// MoreRoutes counts the routes not listed.
	MoreRoutes int
}

// homeData is the data of templates/home.html.
type homeData struct {
	web.Base
	// Searched is true when the page was asked for a query (?q=).
	Searched bool
	Results  []linkItem
	// Capped is true when more links matched than are shown.
	Capped bool
	// Limit and RoutesShown are homeSearchLimit and homeRoutesShown, for
	// search.js, which renders the same list as the user types and must cut
	// it the same way.
	Limit, RoutesShown int
	Yours              []linkItem
	Recent             []linkItem
	// Empty is true when the Directory holds no link at all: the page then
	// shows how to start.
	Empty bool
}

// homePage is the Directory's home page, GET /-/.
func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := homeData{Base: s.base(""), Searched: q != "", Limit: homeSearchLimit, RoutesShown: homeRoutesShown}
	data.Query = q
	// The page's own, larger search box stands in for the header's.
	data.HideHeaderSearch = true
	owners := ownerNames{s: s, r: r}

	if q != "" {
		// One more than is shown, to tell "exactly the limit matched" from
		// "more matched than are shown".
		results, err := s.svc.Search(ctx, s.opts.Actor, q, homeSearchLimit+1)
		if err != nil {
			s.homeFailed(w, r, "search", err)
			return
		}
		if len(results) > homeSearchLimit {
			data.Capped, results = true, results[:homeSearchLimit]
		}
		for _, res := range results {
			item := newLinkItem(res.Link, owners.of(res.Link.OwnerID))
			item.Rank = res.Rank
			data.Results = append(data.Results, item)
		}
	}

	yours, err := s.svc.YourLinks(ctx, s.opts.Actor, homeListLimit)
	if err != nil {
		s.homeFailed(w, r, "your links", err)
		return
	}
	recent, err := s.svc.RecentLinks(ctx, homeListLimit)
	if err != nil {
		s.homeFailed(w, r, "recent links", err)
		return
	}
	// Every link is a recent one until there are more than the list holds,
	// so an empty list of recent links is an empty Directory.
	data.Empty = len(recent) == 0
	for _, l := range yours {
		data.Yours = append(data.Yours, newLinkItem(l, owners.of(l.OwnerID)))
	}
	for _, l := range recent {
		data.Recent = append(data.Recent, newLinkItem(l, owners.of(l.OwnerID)))
	}
	s.render(w, r, http.StatusOK, "home", data)
}

func (s *Server) homeFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	// The query is not logged: it is the user's text (NFR-17).
	s.log.Error("the home page failed", "reading", what, "error", err)
	s.errorPage(w, r, http.StatusInternalServerError, "Something went wrong",
		"The Directory could not list its links. Try again in a moment.")
}

// newLinkItem arranges a link for a list.
func newLinkItem(l linkmodel.Link, owner string) linkItem {
	item := linkItem{Name: l.Name, Description: l.Description, Owner: owner}
	routes := make([]routeView, 0, len(l.Routes))
	for _, rt := range l.Routes {
		routes = append(routes, newRouteView(rt))
	}
	item.Direct = !hasVariables(routes)
	if item.Direct {
		// Through the resolver, like a typed go link: the server decides
		// where it goes, and later counts the use.
		item.Href = "/" + url.PathEscape(l.Name)
	} else {
		item.Href = "/-/links/" + url.PathEscape(l.Name) + "#launch"
	}
	if len(routes) > homeRoutesShown {
		item.MoreRoutes = len(routes) - homeRoutesShown
		routes = routes[:homeRoutesShown]
	}
	item.Routes = routes
	return item
}

// ownerNames asks the Service for each owner's name once per page.
type ownerNames struct {
	s     *Server
	r     *http.Request
	names map[int64]string
}

func (o *ownerNames) of(id int64) string {
	if name, ok := o.names[id]; ok {
		return name
	}
	if o.names == nil {
		o.names = make(map[int64]string)
	}
	name := o.s.svc.OwnerName(o.r.Context(), id)
	o.names[id] = name
	return name
}
