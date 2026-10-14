package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
)

// This file holds the reads the web pages need besides search: one link by
// its name, and the short lists of the home page (SRCH-3).

// LinkByName returns the link with the given name, ignoring case, and whether
// the actor may edit it (CanEdit). It returns ErrNotFound when no link has
// the name.
//
// The name is looked up in the index, which is what a go link resolves
// against, but the link itself is read from the store, like GetLink: the
// edit page takes its version from here, and that must be the stored one.
func (a *App) LinkByName(ctx context.Context, actor Actor, name string) (linkmodel.Link, bool, error) {
	c, ok := a.index.Load().Find(name)
	if !ok {
		return linkmodel.Link{}, false, ErrNotFound
	}
	l, err := a.store.GetLink(ctx, c.Link.ID)
	if errors.Is(err, store.ErrNotFound) {
		// Deleted by another process sharing the database, after this one
		// last saw it.
		return linkmodel.Link{}, false, ErrNotFound
	}
	if err != nil {
		return linkmodel.Link{}, false, fmt.Errorf("read link %q: %w", c.Link.Name, err)
	}
	return l, canEdit(actor, l), nil
}

// YourLinks returns the links the actor owns, the most recently changed
// first, at most limit of them (DefaultListLimit when limit is 0 or less,
// never more than MaxListLimit). Links changed in the same second are
// ordered newest ID first. The zero Actor owns nothing.
//
// It reads the index: a home page shown on every visit costs no query.
func (a *App) YourLinks(_ context.Context, actor Actor, limit int) ([]linkmodel.Link, error) {
	if actor.UserID == 0 {
		return []linkmodel.Link{}, nil
	}
	return a.newest(listLimit(limit),
		func(l *linkmodel.Link) bool { return l.OwnerID == actor.UserID },
		func(l *linkmodel.Link) time.Time { return l.UpdatedAt }), nil
}

// RecentLinks returns the most recently created links, newest first, at most
// limit of them (as for YourLinks). Links created in the same second are
// ordered newest ID first, which is creation order: IDs are never reused.
//
// It takes no actor because every link is visible to everyone; it reads the
// index, as YourLinks does.
func (a *App) RecentLinks(_ context.Context, limit int) ([]linkmodel.Link, error) {
	return a.newest(listLimit(limit),
		func(*linkmodel.Link) bool { return true },
		func(l *linkmodel.Link) time.Time { return l.CreatedAt }), nil
}

// OwnerName returns the display name of the user with the given ID, for
// showing who owns a link; "" when the user cannot be named, which never
// fails a page. Until accounts exist the only possible owner is the built-in
// user.
func (a *App) OwnerName(ctx context.Context, ownerID int64) string {
	u, err := a.localUser(ctx)
	if err != nil || u.ID != ownerID {
		return ""
	}
	return u.DisplayName
}

// newest returns copies of the at most limit links that keep accepts, by
// the time when gives, newest first, ties broken by the higher ID.
//
// It keeps only the best limit links while it scans, so a Directory of
// 100,000 links costs one pass and no sort of the whole list: once the list
// is full, a link older than the last one kept is dismissed with one
// comparison, and in a long-lived Directory nearly all of them are.
func (a *App) newest(limit int, keep func(*linkmodel.Link) bool, when func(*linkmodel.Link) time.Time) []linkmodel.Link {
	newer := func(x, y *linkmodel.Link) bool {
		if tx, ty := when(x), when(y); !tx.Equal(ty) {
			return tx.After(ty)
		}
		return x.ID > y.ID
	}
	best := make([]*linkmodel.Link, 0, limit)
	for c := range a.index.Load().All() {
		l := &c.Link
		if !keep(l) {
			continue
		}
		if len(best) == limit {
			if !newer(l, best[limit-1]) {
				continue
			}
			best = best[:limit-1]
		}
		at := len(best)
		for at > 0 && newer(l, best[at-1]) {
			at--
		}
		best = append(best, nil)
		copy(best[at+1:], best[at:])
		best[at] = l
	}
	out := make([]linkmodel.Link, len(best))
	for i, l := range best {
		out[i] = cloneLink(*l)
	}
	return out
}
