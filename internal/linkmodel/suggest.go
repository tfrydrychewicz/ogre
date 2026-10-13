package linkmodel

import "sort"

// This file depends on distance.go and on nothing else in the package.

// SuggestOptions tunes "Did you mean" (PRD 5.6). Both knobs are configuration
// so they can be adjusted once rescue-rate data exists; the zero value means
// the PRD's defaults.
type SuggestOptions struct {
	// Thresholds is the number of edits allowed between the requested name
	// and a suggested one, by the length of the requested name: 1-2
	// characters, 3-5, and 6 or more. The allowance grows with the length
	// because one edit in a two-letter name reaches most other two-letter
	// names, while two edits in a long name are still a plausible typo.
	//
	// All zeros means the default {0, 1, 2}. A negative entry counts as 0, so
	// {-1, -1, -1} is the way to switch suggestions off.
	Thresholds [3]int

	// Limit is the largest number of suggestions returned. Zero or less means
	// the default, 5.
	Limit int
}

// DefaultSuggestOptions returns the options SuggestOptions{} stands for,
// spelled out, for code that shows or documents the configuration.
func DefaultSuggestOptions() SuggestOptions {
	return SuggestOptions{Thresholds: [3]int{0, 1, 2}, Limit: 5}
}

// withDefaults replaces the fields left at their zero value.
func (o SuggestOptions) withDefaults() SuggestOptions {
	def := DefaultSuggestOptions()
	if o.Thresholds == ([3]int{}) {
		o.Thresholds = def.Thresholds
	}
	if o.Limit <= 0 {
		o.Limit = def.Limit
	}
	return o
}

// AllowedEdits returns how many edits a suggestion may be away from a
// requested name of requestedLen characters: by default 0 for 1-2 characters,
// 1 for 3-5, and 2 for 6 or more (PRD 5.6). It is never negative, and it is 0
// for an empty name, which nothing can be suggested for.
func AllowedEdits(requestedLen int, o SuggestOptions) int {
	o = o.withDefaults()
	var allowed int
	switch {
	case requestedLen <= 0:
		return 0
	case requestedLen <= 2:
		allowed = o.Thresholds[0]
	case requestedLen <= 5:
		allowed = o.Thresholds[1]
	default:
		allowed = o.Thresholds[2]
	}
	return max(allowed, 0)
}

// NameSuggestion is a link name close to a requested one, and how close.
type NameSuggestion struct {
	Name  string
	Edits int // 1 or more: the requested name itself is never a suggestion
}

// SuggestNames returns the names within the allowed distance of requested,
// fewest edits first, then alphabetically, at most o.Limit of them. It returns
// nil when there are none.
//
// requested and names must already be lowercase, since names ignore case and
// nothing else (use NormalizeName); names are expected to be unique. A name
// equal to requested is skipped: the caller only asks after a lookup failed,
// and "did you mean what you typed" helps nobody.
//
// Every name is looked at, which is what PRD 5.6 asks for and stays cheap at
// 100,000 links (see BenchmarkSuggest100k): a name whose length alone rules it
// out costs one comparison, and Distance gives up on the others as soon as
// they are too far. The order of names does not affect the result.
func SuggestNames(requested string, names []string, o SuggestOptions) []NameSuggestion {
	o = o.withDefaults()
	allowed := AllowedEdits(len(requested), o)
	if allowed == 0 {
		return nil // only an exact match would be near enough, and that is no suggestion
	}

	var (
		best  []NameSuggestion // sorted, never longer than o.Limit
		stack [distanceStackRows]int
		rows  = stack[:]
		// bound is the most edits a name may have and still enter best. It
		// starts at the allowance and tightens once best is full, so the scan
		// gets cheaper as it finds good suggestions.
		bound = allowed
	)
	for _, name := range names {
		if diff := len(name) - len(requested); diff > bound || -diff > bound {
			continue // the length difference alone costs more edits than allowed
		}
		edits := boundedOSA(requested, name, bound, &rows)
		if edits == 0 || edits > bound {
			continue
		}
		s := NameSuggestion{Name: name, Edits: edits}

		full := len(best) == o.Limit
		if full && !suggestionLess(s, best[len(best)-1]) {
			continue // as many edits as the last one kept, and after it by name
		}
		at := sort.Search(len(best), func(i int) bool { return suggestionLess(s, best[i]) })
		if best == nil {
			// Sized on the first hit: most scans find nothing, and a
			// configured limit may be far larger than what is ever found.
			best = make([]NameSuggestion, 0, min(o.Limit, 8))
		}
		if !full {
			best = append(best, NameSuggestion{})
		}
		copy(best[at+1:], best[at:]) // when full, this drops the last one
		best[at] = s

		if len(best) == o.Limit {
			bound = best[len(best)-1].Edits
		}
	}
	return best
}

// suggestionLess is the order of suggestions: fewest edits, then by name.
// Recent usage slots in between the two once usage counts exist (PRD 5.6,
// INS-1). The counts will then have to be passed in here rather than applied
// by the caller afterwards, because the limit cuts the list in this order.
func suggestionLess(a, b NameSuggestion) bool {
	if a.Edits != b.Edits {
		return a.Edits < b.Edits
	}
	return a.Name < b.Name
}
