package app

import "github.com/tfrydrychewicz/ogre/internal/linkmodel"

// The permission rules live here, in one place, so that sign-in only has to
// change these functions and never the use cases or the HTTP layer (plan
// section 3.10). In local mode the only actor is the built-in admin, so the
// rules never refuse a real request yet; they exist and are called all the
// same.

// CanEdit reports whether actor may change or delete l: its owner may, and so
// may an admin. The zero Actor may not, whatever the link says, because
// nobody is acting.
//
// The API reports this as a link's "canEdit" so that a client can hide what
// the server would refuse; the edit use cases of L2 apply the same function.
func (a *App) CanEdit(actor Actor, l linkmodel.Link) bool {
	return canEdit(actor, l)
}

func canEdit(actor Actor, l linkmodel.Link) bool {
	if actor.UserID == 0 {
		return false
	}
	return actor.Admin || actor.UserID == l.OwnerID
}

// canCreate is the rule for creating a link: any identified actor may. The
// zero Actor is refused because a link must have an owner.
func canCreate(actor Actor) bool {
	return actor.UserID != 0
}
