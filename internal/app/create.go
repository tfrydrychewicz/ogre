package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// Draft and DraftRoute are a link as submitted for saving. The types and
// every rule about them belong to the link model; they are named here too so
// that callers of the use cases need only this package.
type (
	Draft      = linkmodel.Draft
	DraftRoute = linkmodel.DraftRoute
)

// ValidationError reports everything that keeps a draft from being saved, so
// a form can show all of it at once instead of one problem per attempt.
//
// It holds warnings too, when the draft had any, beside the errors that
// caused it; a draft with warnings alone is saved and yields no
// ValidationError.
type ValidationError struct {
	// Problems are the ones that belong to no single route, such as a draft
	// without routes.
	Problems []linkmodel.Problem
	// Routes has one entry per route of the draft, in the draft's order,
	// holding that route's problems; an entry is empty when the route is
	// fine. Messages do not repeat which route they are about: the position
	// in this slice says so.
	Routes [][]linkmodel.Problem
}

// Error summarizes the problems: the first error's message and how many
// problems follow.
func (e *ValidationError) Error() string {
	all := linkmodel.DraftProblems{Link: e.Problems, Routes: e.Routes}
	first, ok := all.First()
	switch n := all.Count(); {
	case !ok:
		return "link is not valid"
	case n == 1:
		return "link is not valid: " + first.Message
	default:
		return fmt.Sprintf("link is not valid: %s (and %d more)", first.Message, n-1)
	}
}

// CreateLink validates d and saves it as a new link owned by the actor
// (DIR-1: the creator becomes the owner).
//
// It returns ErrForbidden when the actor may not create links, a
// *ValidationError listing every problem of the draft, or a *NameTakenError
// (errors.Is ErrNameTaken) when the name is in use. On success the link is
// durable and already visible to Resolve: the index is swapped after the store
// commits and before this returns, never earlier, so a redirect can't point at
// a link that failed to save.
//
// Route IDs in the draft mean nothing for a new link and are ignored.
func (a *App) CreateLink(ctx context.Context, actor Actor, d Draft) (linkmodel.Link, error) {
	if !canCreate(actor) {
		return linkmodel.Link{}, ErrForbidden
	}
	// The rules are the link model's; this use case only applies them.
	link, problems := linkmodel.ValidateDraft(d, a.rules)
	if problems.HasErrors() {
		return linkmodel.Link{}, &ValidationError{Problems: problems.Link, Routes: problems.Routes}
	}
	link.OwnerID = actor.UserID

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	// The store's unique constraint decides whether the name is free; the
	// index is not consulted for that, so there is one source of truth.
	saved, err := a.store.CreateLink(ctx, link, actor.UserID)
	if err != nil {
		return linkmodel.Link{}, a.saveError(actor, link, problems, err)
	}
	a.index.Store(a.index.Load().With(saved))
	return saved, nil
}

// saveError turns what the store said about saving link into the error the
// use case returns. The caller holds writeMu.
func (a *App) saveError(actor Actor, link linkmodel.Link, problems linkmodel.DraftProblems, err error) error {
	switch {
	case errors.Is(err, store.ErrNameTaken):
		return a.nameTaken(actor, link.Name)
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrVersionConflict):
		return &VersionConflictError{}
	case errors.Is(err, store.ErrRouteNotInLink), errors.Is(err, store.ErrVariableMismatch):
		// The checks before the store call rule both out; if one gets
		// through all the same, it is the draft that is wrong, not the
		// server.
		return &ValidationError{
			Problems: append(slices.Clone(problems.Link), linkmodel.Problem{
				Severity: linkmodel.SeverityError, Field: linkmodel.FieldLink, Code: CodeRouteNotInLink,
				Message: "The routes sent do not fit the saved link. Reload the link and try again.",
			}),
			Routes: problems.Routes,
		}
	}
	return fmt.Errorf("save link %q: %w", link.Name, err)
}

// nameTaken reports that name belongs to another link, saying which and
// whether the actor may edit it (DIR-4). The link is read from the index,
// which the caller's hold on writeMu keeps in step with the store.
func (a *App) nameTaken(actor Actor, name string) error {
	existing, ok := a.index.Load().Lookup(name)
	if !ok {
		// The store has a link the index lacks, which a second process
		// writing to the same database could cause. The name is all there is
		// to tell.
		return &NameTakenError{Existing: linkmodel.Link{Name: name}}
	}
	existing = cloneLink(existing)
	return &NameTakenError{Existing: existing, Editable: canEdit(actor, existing)}
}

// cloneLink returns l with routes of its own, for handing a link from the
// index to a caller who may keep or change it.
func cloneLink(l linkmodel.Link) linkmodel.Link {
	l.Routes = slices.Clone(l.Routes)
	for i := range l.Routes {
		l.Routes[i].Variables = slices.Clone(l.Routes[i].Variables)
	}
	return l
}
