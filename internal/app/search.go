package app

import (
	"context"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Limits of the lists that search and the home page return. A list is for a
// person to look through, so it is short; the maximum keeps one request from
// asking for the whole Directory through a list meant for a screen.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// Ranks of a SearchResult, as the API spells them, best first. See
// index.Snapshot.Search for exactly what each one means.
const (
	RankExact       = "exact"
	RankPrefix      = "prefix"
	RankMisspelled  = "misspelled"
	RankName        = "name"
	RankDescription = "description"
	RankDestination = "destination"
	RankOwner       = "owner"
)

// SearchResult is one link found by Search.
type SearchResult struct {
	// Link is the link, the caller's own copy.
	Link linkmodel.Link
	// Rank says why it was found: one of the Rank constants.
	Rank string
	// Edits is, for RankMisspelled, how many edits separate the query from
	// the name; 0 otherwise.
	Edits int
}

// Search finds links as the user types (SRCH-1, SRCH-2), best first, at most
// limit of them: DefaultListLimit when limit is 0 or less, and never more
// than MaxListLimit.
//
// The ranking is computed by the index from its snapshot alone, so a search
// costs no database query (see index.Snapshot.Search for the rules): the
// exact name, then names that start with the query, then misspelled names
// within the thresholds of "Did you mean", then links in which every word of
// the query occurs, ranked by the best field one occurs in: name,
// description, destination, owner. Ties go by name.
//
// A blank query finds nothing; listing every link is ListLinks. The actor is
// unused for now, as in ListLinks, because every link is visible to
// everyone. The error is reserved for visibility rules that need the store;
// today Search does not fail.
func (a *App) Search(ctx context.Context, _ Actor, q string, limit int) ([]SearchResult, error) {
	hits := a.index.Load().Search(index.Query{
		Text:    q,
		Limit:   listLimit(limit),
		Suggest: a.suggest,
		Owners:  a.ownerNames(ctx),
	})
	if len(hits) == 0 {
		return []SearchResult{}, nil
	}
	out := make([]SearchResult, len(hits))
	for i, h := range hits {
		out[i] = SearchResult{Link: cloneLink(*h.Link), Rank: h.Rank.String(), Edits: h.Edits}
	}
	return out, nil
}

// listLimit applies the default and the maximum to a requested list length.
func listLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	return min(limit, MaxListLimit)
}

// ownerNames returns the display name of every user who can own a link, for
// searching by owner. Until accounts exist that is the built-in user alone.
// When the user cannot be read, links simply cannot be found by owner: a
// search is not failed for a cosmetic field.
func (a *App) ownerNames(ctx context.Context) map[int64]string {
	u, err := a.localUser(ctx)
	if err != nil {
		return nil
	}
	return map[int64]string{u.ID: u.DisplayName}
}
