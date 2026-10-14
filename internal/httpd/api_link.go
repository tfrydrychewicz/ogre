package httpd

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// handleListLinks answers GET /links. Without a query, or with a blank one,
// it lists every link by name, as it did before search existed. With "q" it
// searches: the Service ranks, and this handler only reports the rank it
// gives each link.
//
// "limit" bounds the search results; the Service applies the default (20)
// and the maximum (100). A limit that is not a whole number of 1 or more is
// refused, not ignored, so a client's mistake shows. The plain list is not
// limited: it is what clients have always read in full.
func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	q, limit, rejected := listQuery(r.URL.RawQuery)
	if rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	if q == "" {
		s.writeLinkList(w, r)
		return
	}
	results, err := s.svc.Search(r.Context(), s.opts.Actor, q, limit)
	if err != nil {
		s.serviceError(w, r, "search links", app.Draft{}, err)
		return
	}
	out := struct {
		Links []searchResultJSON `json:"links"`
	}{Links: make([]searchResultJSON, 0, len(results))}
	for _, res := range results {
		link := s.linkJSON(r, res.Link)
		routes := make([]routeWithTokensJSON, 0, len(link.Routes))
		for i, rt := range link.Routes {
			pattern := res.Link.Routes[i].Pattern
			routes = append(routes, routeWithTokensJSON{routeJSON: rt, Tokens: tokensJSON(linkmodel.PatternTokens(pattern), pattern)})
		}
		out.Links = append(out.Links, searchResultJSON{linkJSON: link, Routes: routes, Rank: res.Rank})
	}
	writeJSON(w, r, http.StatusOK, out)
}

// writeLinkList answers with every link, ordered by name.
func (s *Server) writeLinkList(w http.ResponseWriter, r *http.Request) {
	links, err := s.svc.ListLinks(r.Context(), s.opts.Actor)
	if err != nil {
		s.serviceError(w, r, "list links", app.Draft{}, err)
		return
	}
	out := struct {
		Links []linkJSON `json:"links"`
	}{Links: make([]linkJSON, 0, len(links))}
	for _, l := range links {
		out.Links = append(out.Links, s.linkJSON(r, l))
	}
	writeJSON(w, r, http.StatusOK, out)
}

// searchResultJSON is a link as every endpoint returns it, plus why search
// found it: one of the app.Rank* values. Its routes also carry the tokens of
// their patterns (as on the link page), so the results that arrive as the
// user types can highlight variables without browser code reading a pattern
// (NFR-22). Routes shadows linkJSON.Routes in the JSON: the shallower field
// wins. The plain list, without q, keeps the shape it had in L2.
type searchResultJSON struct {
	linkJSON
	Routes []routeWithTokensJSON `json:"routes"`
	Rank   string                `json:"rank"`
}

// listQuery reads "q" and "limit" of GET /links. q comes back trimmed; ""
// means list. limit is 0 when it was not sent. A parameter sent twice uses
// the first value for q, and is refused for limit, as for a delete's version.
func listQuery(rawQuery string) (q string, limit int, rejected *rejection) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", 0, &rejection{http.StatusBadRequest, codeMalformed, "The query string is not valid."}
	}
	q = strings.TrimSpace(query.Get("q"))
	values, sent := query["limit"]
	if !sent {
		return q, 0, nil
	}
	if len(values) != 1 {
		return "", 0, &rejection{http.StatusBadRequest, codeMalformed, "\"limit\" must be given once."}
	}
	limit, err = strconv.Atoi(values[0])
	if err != nil || limit < 1 {
		return "", 0, &rejection{http.StatusBadRequest, codeMalformed, "\"limit\" must be a whole number, 1 or more."}
	}
	return q, limit, nil
}

// linkID reads the {id} of /links/{id}.
//
// An ID is written as decimal digits and nothing else: no sign, no spaces.
// Anything that is not such a number, 0, and a number too large for an ID all
// mean the same thing to the caller, that no link lives at this address, so
// they get the same 404 a missing link gets and the Service is not asked.
func linkID(r *http.Request) (id int64, ok bool) {
	text := r.PathValue("id")
	if text == "" {
		return 0, false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(text, 10, 64) // fails on overflow
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request) {
	id, ok := linkID(r)
	if !ok {
		writeLinkNotFound(w, r)
		return
	}
	link, err := s.svc.GetLink(r.Context(), s.opts.Actor, id)
	if err != nil {
		s.serviceError(w, r, "get link", app.Draft{}, err)
		return
	}
	writeJSON(w, r, http.StatusOK, s.linkJSON(r, link))
}

// updateRequest is the body of PATCH /links/{id}.
//
// Despite the method, the body is the whole editable link: description and
// routes replace what is stored (plan section 3.5), and a route that is left
// out is deleted. All three fields are therefore required, so that a request
// that forgot one cannot erase the description or overwrite someone else's
// change unnoticed. The pointers tell "not sent" from an empty value.
type updateRequest struct {
	Description *string        `json:"description"`
	Routes      []routeRequest `json:"routes"`
	// Version is the link's version as the caller last read it.
	Version *int `json:"version"`
}

func (s *Server) handleUpdateLink(w http.ResponseWriter, r *http.Request) {
	id, ok := linkID(r)
	if !ok {
		writeLinkNotFound(w, r)
		return
	}
	var req updateRequest
	if rejected := decodeBody(w, r, &req); rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	// These are faults of the request, not of the link it describes, so
	// they answer 400 and not the 422 of a draft that fails validation.
	switch {
	case req.Version == nil:
		writeError(w, r, http.StatusBadRequest, codeMalformed,
			"Send \"version\": the version of the link as you last read it.", nil)
		return
	case *req.Version < 1:
		writeError(w, r, http.StatusBadRequest, codeMalformed, "\"version\" must be 1 or more.", nil)
		return
	case req.Description == nil:
		writeError(w, r, http.StatusBadRequest, codeMalformed,
			"Send \"description\", even when it is empty: an update replaces it.", nil)
		return
	case req.Routes == nil:
		writeError(w, r, http.StatusBadRequest, codeMalformed,
			"Send \"routes\": an update replaces all routes of the link.", nil)
		return
	}
	draft := newDraft(*req.Description, req.Routes)

	link, err := s.svc.UpdateLink(r.Context(), s.opts.Actor, id, draft, *req.Version)
	if err != nil {
		s.serviceError(w, r, "update link", draft, err)
		return
	}
	writeJSON(w, r, http.StatusOK, s.linkJSON(r, link))
}

// handleDeleteLink answers 204 without a body. The request has no body
// either; one that is sent is not read.
//
// "?version=N" makes the delete conditional on the link still being at that
// version; without it the link is deleted as it is now.
func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	id, ok := linkID(r)
	if !ok {
		writeLinkNotFound(w, r)
		return
	}
	version, rejected := deleteVersion(r.URL.RawQuery)
	if rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	if err := s.svc.DeleteLink(r.Context(), s.opts.Actor, id, version); err != nil {
		s.serviceError(w, r, "delete link", app.Draft{}, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// deleteVersion reads the optional version parameter. 0 means "not sent",
// which the Service takes as "do not check".
//
// A version that is sent but unusable is refused instead of ignored: the
// caller asked for a conditional delete, and falling back to an
// unconditional one would delete what they meant to protect.
func deleteVersion(rawQuery string) (int, *rejection) {
	bad := &rejection{http.StatusBadRequest, codeMalformed,
		"\"version\" must be a whole number, 1 or more, given once."}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, &rejection{http.StatusBadRequest, codeMalformed, "The query string is not valid."}
	}
	values, sent := query["version"]
	if !sent {
		return 0, nil
	}
	if len(values) != 1 {
		return 0, bad
	}
	version, err := strconv.Atoi(values[0])
	if err != nil || version < 1 {
		return 0, bad
	}
	return version, nil
}
