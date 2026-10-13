// Package app holds the Ogre server's use cases: resolving a request, and
// creating, checking, reading, changing and deleting links.
//
// It is the only package that writes through the store (plan section 3.2).
// Every use case that reads or changes data on someone's behalf takes an
// Actor, so the local single-user mode and a later signed-in mode run the
// same code. The HTTP layer above translates requests into these calls and
// contains no rules of its own.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// App is the set of use cases over one store. Create it with New. Its methods
// are safe for concurrent use.
type App struct {
	store store.Store
	index *index.Index
	// suggest tunes "Did you mean"; rules is what the save rules need to
	// know about this Directory. Both are fixed for the life of the App.
	suggest linkmodel.SuggestOptions
	rules   linkmodel.ValidateOptions

	// writeMu serializes writes. A write derives the next snapshot from the
	// current one, so two writes at once would each publish a snapshot that
	// lacks the other's link. Holding it across the store call also publishes
	// snapshots in commit order. Readers never take it.
	writeMu sync.Mutex

	// local caches the built-in user, which does not change while the
	// process runs, so that naming the owner of each listed link does not
	// cost a query per link.
	localMu sync.Mutex
	local   *store.User
}

// New returns an App over st with every stored link loaded into the index.
// Loading everything up front is what lets Resolve answer without the
// database for the life of the process: each link is compiled here, once,
// regular expressions included, and again only when it is saved.
//
// opts is optional; without it the defaults apply (Options). Only the first
// value is used.
func New(ctx context.Context, st store.Store, opts ...Options) (*App, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	links, err := st.ListLinks(ctx)
	if err != nil {
		return nil, fmt.Errorf("load links into the index: %w", err)
	}
	return &App{
		store:   st,
		index:   index.New(index.Build(links)),
		suggest: o.Suggest,
		rules:   linkmodel.ValidateOptions{OwnHosts: slices.Clone(o.OwnHosts)},
	}, nil
}

// Actor is who a use case acts for. Local mode supplies the built-in user
// (LocalActor); sign-in will supply the signed-in user without the use cases
// changing (plan section 3.10).
type Actor struct {
	UserID      int64
	DisplayName string
	// Admin actors may change any link, not only their own.
	Admin bool
}

// LocalActor returns the actor of local mode: the built-in user, who owns
// every link created without sign-in.
func (a *App) LocalActor(ctx context.Context) (Actor, error) {
	u, err := a.localUser(ctx)
	if err != nil {
		return Actor{}, fmt.Errorf("load the local user: %w", err)
	}
	return Actor{UserID: u.ID, DisplayName: u.DisplayName, Admin: u.Role == "admin"}, nil
}

// Owner returns the display name of l's owner, for showing next to the link.
// It returns "" when the owner cannot be named, which never fails a request:
// a missing name is a cosmetic gap, not a reason to hide the link.
//
// Until accounts exist the only possible owner is the built-in user.
func (a *App) Owner(ctx context.Context, l linkmodel.Link) string {
	u, err := a.localUser(ctx)
	if err != nil || u.ID != l.OwnerID {
		return ""
	}
	return u.DisplayName
}

// ListLinks returns every link, ordered by name, with its routes in order.
// The actor is unused for now because every link is visible to everyone; it
// is in the signature so that visibility rules can arrive without changing
// callers.
func (a *App) ListLinks(ctx context.Context, _ Actor) ([]linkmodel.Link, error) {
	links, err := a.store.ListLinks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	return links, nil
}

// GetLink returns the link with the given ID, with its routes in order, or
// ErrNotFound. It reads the store, not the index, so what it returns is what
// an edit will be checked against (the version above all).
//
// The actor is unused for the same reason as in ListLinks.
func (a *App) GetLink(ctx context.Context, _ Actor, id int64) (linkmodel.Link, error) {
	l, err := a.store.GetLink(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return linkmodel.Link{}, ErrNotFound
	}
	if err != nil {
		return linkmodel.Link{}, fmt.Errorf("read link %d: %w", id, err)
	}
	return l, nil
}

// localUser returns the built-in user, asking the store only until the first
// success. A failure is not cached, so a later call can succeed.
func (a *App) localUser(ctx context.Context) (store.User, error) {
	a.localMu.Lock()
	defer a.localMu.Unlock()
	if a.local != nil {
		return *a.local, nil
	}
	u, err := a.store.LocalUser(ctx)
	if err != nil {
		return store.User{}, err
	}
	a.local = &u
	return u, nil
}
