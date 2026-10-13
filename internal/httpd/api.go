package httpd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/tfrydrychewicz/godir/internal/app"
	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

const (
	apiPrefix = internalPrefix + "api/v1/"

	// product identifies a godir to another godir: Listen asks the meta
	// endpoint who holds a busy port. It is on the rename list of plan
	// section 3.1.
	product    = "godir"
	apiVersion = 1

	// maxBodyBytes bounds a request body. A link with every route at its
	// longest is far below it.
	maxBodyBytes = 1 << 20
)

// internalMux routes everything under "/-/". ServeMux is safe here, unlike
// at the root: serveInternal has already refused paths it would rewrite.
func (s *Server) internalMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(internalPrefix+"healthz", byMethod{http.MethodGet: s.handleHealthz})
	mux.Handle(apiPrefix+"meta", byMethod{http.MethodGet: s.handleMeta})
	mux.Handle(apiPrefix+"links", byMethod{http.MethodGet: s.handleListLinks, http.MethodPost: s.handleCreateLink})
	// Unknown API paths answer in the API's error shape, not with a UI page.
	mux.HandleFunc(internalPrefix+"api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, codeNotFound, "There is no such API endpoint.", nil)
	})
	mux.Handle(internalPrefix, s.opts.UI)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeText(w, r, http.StatusOK, "ok")
}

// metaJSON is how a client, the extension, or another godir recognises this
// server and learns which API it speaks.
type metaJSON struct {
	Product string `json:"product"`
	Version string `json:"version"`
	API     int    `json:"api"`
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, metaJSON{Product: product, Version: s.opts.Version, API: apiVersion})
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.svc.ListLinks(r.Context(), s.opts.Actor)
	if err != nil {
		s.serviceError(w, r, "list links", err)
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

// linkRequest is the body of POST /links. The name is never sent: it is the
// first segment all routes share.
type linkRequest struct {
	Description string         `json:"description"`
	Routes      []routeRequest `json:"routes"`
}

type routeRequest struct {
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if rejected := decodeBody(w, r, &req); rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	draft := app.Draft{Description: req.Description, Routes: make([]app.DraftRoute, 0, len(req.Routes))}
	for _, rt := range req.Routes {
		draft.Routes = append(draft.Routes, app.DraftRoute{Pattern: rt.Pattern, Destination: rt.Destination})
	}

	link, err := s.svc.CreateLink(r.Context(), s.opts.Actor, draft)
	var invalid *app.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeInvalid(w, r, draft, invalid)
		return
	case err != nil:
		s.serviceError(w, r, "create link", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, s.linkJSON(r, link))
}

// decodeBody reads one JSON value into dst. It refuses unknown fields and
// anything after the value, so a typo in a field name is reported instead of
// silently saving a link without it.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) *rejection {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeRejection(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return &rejection{http.StatusBadRequest, codeMalformed, "The request body has extra data after the JSON value."}
		}
		return decodeRejection(err)
	}
	return nil
}

func decodeRejection(err error) *rejection {
	var tooLarge *http.MaxBytesError
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		return &rejection{http.StatusRequestEntityTooLarge, codeTooLarge, "The request body is larger than 1 MiB."}
	case errors.As(err, &wrongType):
		return &rejection{http.StatusBadRequest, codeMalformed, "The field \"" + wrongType.Field + "\" has the wrong type."}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no error type for this; the field name it quotes
		// is useful to whoever wrote the request.
		return &rejection{http.StatusBadRequest, codeMalformed,
			"The request body has an " + strings.TrimPrefix(err.Error(), "json: ") + "."}
	default:
		return &rejection{http.StatusBadRequest, codeMalformed, "The request body is not valid JSON."}
	}
}

// problemJSON is a linkmodel.Problem on the wire.
type problemJSON struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	// Start and End mark the problem in the text of its field as offsets in
	// UTF-16 code units: the index a browser's JavaScript uses for the same
	// string, so a form can highlight the span without converting.
	Start int `json:"start"`
	End   int `json:"end"`
}

// routeProblemsJSON holds the problems of one route of a draft.
type routeProblemsJSON struct {
	Problems []problemJSON `json:"problems"`
}

// writeInvalid answers 422 for a draft that failed validation.
//
// The problems are grouped the way the validate endpoint groups them (plan
// section 3.5): details.routes has one entry per route of the request, in the
// same order, each with that route's problems; details.problems has those
// that belong to no single route, such as a request without routes.
func writeInvalid(w http.ResponseWriter, r *http.Request, draft app.Draft, invalid *app.ValidationError) {
	routes := make([]routeProblemsJSON, len(invalid.Routes))
	for i, problems := range invalid.Routes {
		var route app.DraftRoute
		if i < len(draft.Routes) {
			route = draft.Routes[i]
		}
		routes[i] = routeProblemsJSON{Problems: problemsJSON(problems, route)}
	}
	writeError(w, r, http.StatusUnprocessableEntity, codeLinkInvalid,
		"This link can't be saved until its problems are fixed.", map[string]any{
			"problems": problemsJSON(invalid.Problems, app.DraftRoute{}),
			"routes":   routes,
		})
}

// problemsJSON converts problems for the wire. route is the text they point
// into; its pattern and destination are needed to convert the offsets.
func problemsJSON(problems []linkmodel.Problem, route app.DraftRoute) []problemJSON {
	out := make([]problemJSON, 0, len(problems))
	for _, p := range problems {
		text := route.Pattern
		if p.Field == linkmodel.FieldDestination {
			text = route.Destination
		}
		out = append(out, problemJSON{
			Severity: p.Severity, Field: p.Field, Code: p.Code, Message: p.Message,
			Start: utf16Offset(text, p.Start), End: utf16Offset(text, p.End),
		})
	}
	return out
}

// utf16Offset converts a byte offset into text, which is how the link model
// counts, into the offset of the same position in UTF-16 code units, which is
// how the browser that draws the highlight counts. The two differ as soon as
// a non-ASCII character comes before the position.
//
// An offset past the end of text counts as the end. Text decoded from a JSON
// request is always valid UTF-8.
func utf16Offset(text string, byteOffset int) int {
	byteOffset = max(0, min(byteOffset, len(text)))
	units := 0
	for _, r := range text[:byteOffset] {
		units += utf16.RuneLen(r)
	}
	return units
}

// serviceError turns an error of the use cases into a response. Only errors
// the caller can act on are described; anything else is logged and answered
// with a bare 500, so internals never leak into a response.
func (s *Server) serviceError(w http.ResponseWriter, r *http.Request, action string, err error) {
	switch {
	case errors.Is(err, app.ErrNameTaken):
		writeError(w, r, http.StatusConflict, codeNameTaken, "A link with this name already exists.", nil)
	case errors.Is(err, app.ErrForbidden):
		writeError(w, r, http.StatusForbidden, codeForbidden, "You are not allowed to do this.", nil)
	default:
		s.log.Error(action+" failed", "error", err)
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Something went wrong on the server.", nil)
	}
}

// linkJSON is a link as every endpoint returns it (plan section 3.5).
type linkJSON struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Owner       ownerJSON   `json:"owner"`
	Version     int         `json:"version"`
	CanEdit     bool        `json:"canEdit"`
	Routes      []routeJSON `json:"routes"`
	Usage       usageJSON   `json:"usage"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
}

type ownerJSON struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
}

type routeJSON struct {
	ID          int64  `json:"id"`
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
	// Variables is always empty in L1; the field is present so clients can
	// be written against the final shape.
	Variables []any `json:"variables"`
}

// usageJSON is all zeros until usage counting arrives in L5.
type usageJSON struct {
	Total      int64   `json:"total"`
	Last30Days int64   `json:"last30Days"`
	LastUsedAt *string `json:"lastUsedAt"`
}

func (s *Server) linkJSON(r *http.Request, l linkmodel.Link) linkJSON {
	out := linkJSON{
		ID:          l.ID,
		Name:        l.Name,
		Description: l.Description,
		Owner:       ownerJSON{ID: l.OwnerID, DisplayName: s.svc.Owner(r.Context(), l)},
		Version:     l.Version,
		// The rule is the Service's; the same one guards the edit itself.
		CanEdit:   s.svc.CanEdit(s.opts.Actor, l),
		Routes:    make([]routeJSON, 0, len(l.Routes)),
		CreatedAt: formatTime(l.CreatedAt),
		UpdatedAt: formatTime(l.UpdatedAt),
	}
	for _, rt := range l.Routes {
		out.Routes = append(out.Routes, routeJSON{ID: rt.ID, Pattern: rt.Pattern, Destination: rt.Destination, Variables: []any{}})
	}
	return out
}

// formatTime renders a time as the API does everywhere: UTC, RFC 3339.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
