package httpd

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/tfrydrychewicz/ogre/internal/app"
)

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
