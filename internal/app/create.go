package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/tfrydrychewicz/godir/internal/linkmodel"
	"github.com/tfrydrychewicz/godir/internal/store"
)

// Errors of the use cases that a caller is expected to handle. Test for them
// with errors.Is.
var (
	// ErrNameTaken means a link with the draft's name already exists.
	ErrNameTaken = errors.New("a link with this name already exists")
	// ErrForbidden means the actor is not allowed to do what was asked.
	ErrForbidden = errors.New("not allowed")
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

// Error summarizes the problems: the first message and how many follow.
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
// *ValidationError listing every problem of the draft, or ErrNameTaken when
// the name is in use. On success the link is durable and already visible to
// Resolve: the index is swapped after the store commits and before this
// returns, never earlier, so a redirect can't point at a link that failed to
// save.
func (a *App) CreateLink(ctx context.Context, actor Actor, d Draft) (linkmodel.Link, error) {
	if !canCreate(actor) {
		return linkmodel.Link{}, ErrForbidden
	}
	// The rules are the link model's; this use case only applies them.
	link, problems := linkmodel.ValidateDraft(d)
	if problems.Any() {
		return linkmodel.Link{}, &ValidationError{Problems: problems.Link, Routes: problems.Routes}
	}
	link.OwnerID = actor.UserID

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	// The store's unique constraint decides whether the name is free; the
	// index is not consulted, so there is one source of truth.
	saved, err := a.store.CreateLink(ctx, link)
	if errors.Is(err, store.ErrNameTaken) {
		return linkmodel.Link{}, ErrNameTaken
	}
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("save link %q: %w", link.Name, err)
	}
	a.index.Store(a.index.Load().With(saved))
	return saved, nil
}
