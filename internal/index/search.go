package index

import (
	"slices"
	"strings"
	"unicode"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// This file is the search of the web UI (PRD SRCH-1, SRCH-2; plan L3.2). It
// runs on every keystroke, so it works on the snapshot alone: no database
// query, and no text is lowercased per search except the query itself. One
// search is one pass over the links in name order.

// Rank says why a link was found, and orders the results: a lower Rank comes
// first. The order is SRCH-2's: the name itself, a name that starts with the
// query, a misspelled name, then matches elsewhere.
type Rank int

// The ranks, best first.
const (
	// RankExact: the query is the link's name.
	RankExact Rank = iota
	// RankPrefix: the link's name starts with the query.
	RankPrefix
	// RankMisspelled: the link's name is within the edits that "Did you mean"
	// allows for a name as long as the query (linkmodel.AllowedEdits, the
	// same thresholds as suggestions on a miss).
	RankMisspelled
	// RankName: every word of the query occurs in the link's text, and the
	// best field one of them occurs in is the name ("status" finds
	// "platform-status").
	RankName
	// RankDescription: as RankName, the best field being the description.
	RankDescription
	// RankDestination: as RankName, the best field being a destination of
	// one of the link's routes.
	RankDestination
	// RankOwner: every word occurs in the owner's display name and nowhere
	// better.
	RankOwner

	rankNone // not a result; also the number of real ranks
)

// String returns the rank as the API spells it.
func (r Rank) String() string {
	switch r {
	case RankExact:
		return "exact"
	case RankPrefix:
		return "prefix"
	case RankMisspelled:
		return "misspelled"
	case RankName:
		return "name"
	case RankDescription:
		return "description"
	case RankDestination:
		return "destination"
	case RankOwner:
		return "owner"
	}
	return "none"
}

// MaxQueryWords is how many distinct words of a query are used; later ones
// are ignored. Each word costs a scan of the links that matched the ones
// before it, so this bounds what a pasted paragraph can cost. Nobody looks
// for a link with nine words.
const MaxQueryWords = 8

// Query is one search.
type Query struct {
	// Text is what the user typed. Case does not matter, and leading and
	// trailing space is ignored. Blank text finds nothing: listing every link
	// is not a search, and the caller does that itself.
	Text string
	// Limit is the most results returned. Zero or less returns none.
	Limit int
	// Suggest supplies the edit thresholds for RankMisspelled. The zero value
	// means the PRD's defaults, as for "Did you mean".
	Suggest linkmodel.SuggestOptions
	// Owners maps an owner's ID to the display name a query may match. An
	// owner missing from it simply cannot be found by name.
	Owners map[int64]string
}

// Hit is one search result.
type Hit struct {
	// Link is the link in the snapshot: read it, never modify it.
	Link *linkmodel.Link
	Rank Rank
	// Edits is, for RankMisspelled, how many edits separate the query from
	// the name; 0 for every other rank.
	Edits int
}

// Search finds the links that match q, best first, at most q.Limit of them.
//
// The query is lowercased and split into words at white space. A link is a
// result when either rule holds, and its rank is the better of the two:
//
//  1. The query is a single word and its name term equals the link's name
//     (RankExact), is a prefix of it (RankPrefix), or is within the allowed
//     edits of it (RankMisspelled). The name term is the word with a leading
//     "go/" removed and anything from the next "/" on cut off, so that
//     "go/reports/10/13", pasted from somewhere, finds go/reports.
//  2. Every word occurs, as a substring, in at least one of the link's name,
//     its description, the destinations of its routes, or its owner's name;
//     not necessarily all in the same field. The rank is that of the best
//     field any word occurs in, in this order: name, description,
//     destination, owner. So "daily report" finds a link described as "Daily
//     report for a given day", ranked by its description.
//
// Within a rank, results are ordered by name; misspelled names by fewest
// edits first, then by name. Usage arrives with INS-1 and will then break
// ties before the name (SRCH-2).
func (s *Snapshot) Search(q Query) []Hit {
	if s == nil || q.Limit <= 0 {
		return nil
	}
	words := queryWords(q.Text)
	if len(words) == 0 {
		return nil
	}

	var term string
	allowed := 0
	if len(words) == 1 {
		term = nameTerm(words[0])
		allowed = linkmodel.AllowedEdits(len(term), q.Suggest)
	}
	// owners[i] is the set of owners whose name contains words[i]; nil, which
	// finds nobody, when none does.
	owners := make([]map[int64]bool, len(words))
	for id, name := range q.Owners {
		lower := strings.ToLower(name)
		for i, w := range words {
			if strings.Contains(lower, w) {
				if owners[i] == nil {
					owners[i] = make(map[int64]bool)
				}
				owners[i][id] = true
			}
		}
	}

	// One bucket per rank, each filled in name order because the docs are
	// in name order. A bucket stops growing at the limit, since what comes
	// later in it would come later in the result too; except the misspelled
	// one, which is ordered by edits first and is sorted at the end. It stays
	// small: the edit thresholds admit few names.
	var buckets [rankNone][]Hit
	for i, d := range s.docs {
		rank, edits := nameRank(s.names[i], term, allowed)
		if rank == rankNone {
			rank = d.fieldRank(words, owners)
			if rank == rankNone {
				continue
			}
		}
		if rank != RankMisspelled && len(buckets[rank]) >= q.Limit {
			continue
		}
		buckets[rank] = append(buckets[rank], Hit{Link: &d.link.Link, Rank: rank, Edits: edits})
	}
	slices.SortStableFunc(buckets[RankMisspelled], func(a, b Hit) int { return a.Edits - b.Edits })

	var hits []Hit
	for _, bucket := range buckets {
		room := q.Limit - len(hits)
		if room <= 0 {
			break
		}
		hits = append(hits, bucket[:min(room, len(bucket))]...)
	}
	return hits
}

// queryWords lowercases text and splits it into its distinct words, at most
// MaxQueryWords of them. Control characters separate words like spaces do,
// which also guarantees that no word contains the separator of a doc's
// fields (docSep), so a word never matches across two fields.
func queryWords(text string) []string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToLower(text))
	var words []string
	for _, w := range strings.Fields(text) {
		if !slices.Contains(words, w) {
			words = append(words, w)
			if len(words) == MaxQueryWords {
				break
			}
		}
	}
	return words
}

// nameTerm is the part of a single-word query that is compared with names
// (rule 1 of Search).
func nameTerm(word string) string {
	word = strings.TrimPrefix(word, "go/")
	name, _, _ := strings.Cut(word, "/")
	return name
}

// nameRank compares a link's name with the query's name term. The term is
// "" when the query has more than one word, which no name matches.
func nameRank(name, term string, allowed int) (Rank, int) {
	switch {
	case term == "":
		return rankNone, 0
	case name == term:
		return RankExact, 0
	case strings.HasPrefix(name, term):
		return RankPrefix, 0
	case allowed > 0:
		// The length difference alone costs an edit per character; most
		// names are ruled out here without computing anything.
		if diff := len(name) - len(term); diff > allowed || -diff > allowed {
			return rankNone, 0
		}
		if edits := linkmodel.Distance(term, name, allowed); edits <= allowed {
			return RankMisspelled, edits
		}
	}
	return rankNone, 0
}

// docSep separates the fields of a doc's text. No query word contains it
// (queryWords), so a match always lies inside one field.
const docSep = '\x00'

// doc is a link as search sees it. It is built when the link enters a
// snapshot and never changes, like the link.
type doc struct {
	link *linkmodel.CompiledLink
	// text is the link's searchable fields, lowercased, in the order of
	// their ranks: the name, the description, then every route's
	// destination, separated by docSep. The first occurrence of a word is
	// therefore in the best field that has it, and one strings.Index per
	// word answers both whether and where it occurs.
	text string
	// descAt and destAt are where the description and the destinations
	// begin in text.
	descAt, destAt int
}

// newDoc builds the doc of c, stored under name (the normalized key).
//
// Every link has a doc for the life of the process, so the builder is sized
// up front: grown by appending, it would leave up to half of each text as
// unused capacity, megabytes at 100,000 links (docs/perf.md, NFR-5). The
// size is exact unless lowercasing changes a length, which only costs one
// more growth.
func newDoc(name string, c *linkmodel.CompiledLink) *doc {
	size := len(name) + 1 + len(c.Link.Description) + 1
	for _, r := range c.Link.Routes {
		size += len(r.Destination) + 1
	}
	var b strings.Builder
	b.Grow(size)
	b.WriteString(name)
	b.WriteByte(docSep)
	descAt := b.Len()
	b.WriteString(strings.ToLower(c.Link.Description))
	b.WriteByte(docSep)
	destAt := b.Len()
	for i, r := range c.Link.Routes {
		if i > 0 {
			b.WriteByte(docSep)
		}
		b.WriteString(strings.ToLower(r.Destination))
	}
	return &doc{link: c, text: b.String(), descAt: descAt, destAt: destAt}
}

// fieldRank applies rule 2 of Search: every word must occur in the doc's
// text or in its owner's name, and the rank is the best field found.
// owners[i] holds the owners whose name contains words[i].
func (d *doc) fieldRank(words []string, owners []map[int64]bool) Rank {
	best := rankNone
	for i, w := range words {
		at := strings.Index(d.text, w)
		var r Rank
		switch {
		case at < 0:
			if !owners[i][d.link.Link.OwnerID] {
				return rankNone
			}
			r = RankOwner
		case at < d.descAt:
			r = RankName
		case at < d.destAt:
			r = RankDescription
		default:
			r = RankDestination
		}
		best = min(best, r)
	}
	return best
}
