package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// UpdateLink replaces the description and routes of the link with the given
// ID by those of d, provided the link is still at version, the version the
// caller read. It returns the link as saved, one version higher.
//
// A draft has no name of its own: the name is the first segment its patterns
// share. Renaming a link is therefore nothing special, just a draft whose
// patterns begin with another name, and it is subject to the same rule as
// creating one: the new name must be free.
//
// A route of the draft that carries the ID of one of the link's routes keeps
// that route's identity; a route with ID 0 is new; a saved route the draft
// does not name is removed.
//
// Errors, in the order they are reported:
//
//   - ErrForbidden: nobody is acting; or, as a *ForbiddenError that names
//     the link and its owner, the actor may not edit this link (CanEdit).
//     Reported before anything about the draft, so that someone who may not
//     edit learns nothing about it from trying.
//   - ErrNotFound: no link has that ID.
//   - *VersionConflictError (errors.Is ErrVersionConflict): the link is at
//     another version. Reported before the draft's own problems: someone
//     whose copy is stale has to load the link again whatever they fix, and
//     should hear so first, not after correcting every field.
//   - *ValidationError: the draft breaks a rule.
//   - *ValidationError with CodeRouteNotInLink: a route claims the ID of a
//     saved route that is not one of this link's.
//   - *NameTakenError (errors.Is ErrNameTaken): the draft renames the link to
//     a name another link has.
//
// On success the change is durable and visible to Resolve before this
// returns; after a rename the old name no longer resolves.
func (a *App) UpdateLink(ctx context.Context, actor Actor, id int64, d Draft, version int) (linkmodel.Link, error) {
	if actor.UserID == 0 {
		return linkmodel.Link{}, ErrForbidden
	}
	// The rules need no stored data, so the draft is checked before the lock
	// is taken, as CreateLink does: a slow check must not hold up every other
	// write. What it found is reported only after the checks below.
	link, problems := linkmodel.ValidateDraft(d, a.rules)

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	current, err := a.editable(ctx, actor, id)
	if err != nil {
		return linkmodel.Link{}, err
	}
	if version != current.Version {
		return linkmodel.Link{}, &VersionConflictError{CurrentVersion: current.Version}
	}
	if problems.HasErrors() {
		return linkmodel.Link{}, &ValidationError{Problems: problems.Link, Routes: problems.Routes}
	}
	// After the version: a caller with a stale copy of the link sends stale
	// route IDs too, and should hear about the version, not about the IDs.
	if foreign := foreignRoutes(d, current); foreign != nil {
		for i, p := range foreign {
			problems.Routes[i] = append(problems.Routes[i], p...)
		}
		return linkmodel.Link{}, &ValidationError{Problems: problems.Link, Routes: problems.Routes}
	}
	link.ID = id

	saved, err := a.store.UpdateLink(ctx, link, version, actor.UserID)
	if err != nil {
		err = a.saveError(actor, link, problems, err)
		var conflict *VersionConflictError
		if errors.As(err, &conflict) {
			// Something other than this process changed the database
			// between the read above and the write.
			if now, getErr := a.store.GetLink(ctx, id); getErr == nil {
				conflict.CurrentVersion = now.Version
			}
		}
		return linkmodel.Link{}, err
	}

	snapshot := a.index.Load()
	if saved.Name != current.Name {
		snapshot = snapshot.Without(current.Name)
	}
	a.index.Store(snapshot.With(saved))
	return saved, nil
}

// DeleteLink removes the link with the given ID and its routes. version is
// the version the caller read, or 0 to delete the link whatever its version.
//
// It returns ErrForbidden (as a *ForbiddenError naming the link and its
// owner) when the actor may not edit the link, ErrNotFound when no link has
// that ID, and a *VersionConflictError (errors.Is ErrVersionConflict) when
// version is neither 0 nor the stored version. On
// success the name stops resolving before this returns, and is free for a
// new link.
func (a *App) DeleteLink(ctx context.Context, actor Actor, id int64, version int) error {
	if actor.UserID == 0 {
		return ErrForbidden
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	current, err := a.editable(ctx, actor, id)
	if err != nil {
		return err
	}
	// The store compares the version, in the transaction that deletes; a
	// second comparison here would decide nothing.
	switch err := a.store.DeleteLink(ctx, id, version, actor.UserID); {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrVersionConflict):
		return &VersionConflictError{CurrentVersion: current.Version}
	case err != nil:
		return fmt.Errorf("delete link %q: %w", current.Name, err)
	}
	a.index.Store(a.index.Load().Without(current.Name))
	return nil
}

// editable reads the link an edit is about and checks that the actor may
// edit it. The caller holds writeMu, so the link cannot change under it
// before the write.
//
// It reads the store and not the index: the version an edit is checked
// against must be the stored one.
func (a *App) editable(ctx context.Context, actor Actor, id int64) (linkmodel.Link, error) {
	current, err := a.store.GetLink(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return linkmodel.Link{}, ErrNotFound
	}
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("read link %d: %w", id, err)
	}
	if !canEdit(actor, current) {
		// Nothing here is a secret from the actor: the link and its owner are
		// what reading the link shows to everyone.
		return linkmodel.Link{}, &ForbiddenError{Link: current, Owner: a.Owner(ctx, current)}
	}
	return current, nil
}

// foreignRoutes checks the route IDs of a draft against the link it edits. It
// returns nil when every ID is 0 or one of the link's, and otherwise one
// entry per route of the draft with a problem for each route that claims an
// ID the link does not have.
//
// The store refuses such an update as well. It is checked here first so that
// saving and Validate report it the same way, as a problem of the route.
func foreignRoutes(d Draft, current linkmodel.Link) [][]linkmodel.Problem {
	own := make(map[int64]bool, len(current.Routes))
	for _, r := range current.Routes {
		own[r.ID] = true
	}
	var problems [][]linkmodel.Problem
	for i, r := range d.Routes {
		if r.ID == 0 || own[r.ID] {
			continue
		}
		if problems == nil {
			problems = make([][]linkmodel.Problem, len(d.Routes))
		}
		problems[i] = append(problems[i], linkmodel.Problem{
			Severity: linkmodel.SeverityError, Field: linkmodel.FieldRoute, Code: CodeRouteNotInLink,
			Message: "This route refers to a saved route that the link does not have. Reload the link and try again.",
		})
	}
	return problems
}
