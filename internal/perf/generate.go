// Package perf is the performance run of plan task L4.7: it generates a
// Directory of realistic links, starts the real ogre binary against it, and
// measures it over real HTTP against NFR-1, NFR-2, NFR-3 and NFR-5. The
// command is cmd/ogre-perf; docs/perf.md says how to run it and records the
// results.
//
// None of this is part of the shipped binary. It lives in internal/ only
// because Go code in this repository lives in cmd/ and internal/, and because
// it reuses the link model and the store, so that the links it measures are
// links the product itself would accept and load.
package perf

import (
	"fmt"
	"strings"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Generated is a Directory's worth of links together with the requests that
// exercise them. The generator writes both, because it is the one place that
// knows which request fits which route: reading the patterns back to invent
// requests would be a second, untested implementation of the link model.
type Generated struct {
	// Links are ready to save: each is the output of linkmodel.ValidateDraft
	// (canonical patterns, one VariableMeta per variable) with ID, owner,
	// version and timestamps set. Link IDs count 1, 2, 3, ... in slice
	// order, and route IDs likewise over all routes, route by route and link
	// by link: the IDs store.CreateLink hands out when it saves the links in
	// this order into an empty database.
	Links []linkmodel.Link
	// Hits are paths, with the leading slash, that a route answers.
	Hits []string
	// KnownMisses name an existing link with a request none of its routes
	// fits: a value of the wrong type, a month of 13, a segment too many.
	KnownMisses []string
	// UnknownMisses name no link. Half are one typo away from a real name,
	// so the miss page has suggestions to rank and values to carry over;
	// half are nothing like any name, so every name is looked at and
	// rejected.
	UnknownMisses []string
	// SearchOne and SearchTwo are queries of one and of two words, as a
	// person types them into the search box: whole words, prefixes of a
	// word, misspellings, and words of descriptions and destinations.
	SearchOne, SearchTwo []string
}

// maxSamplesPerKind bounds each request list. The lists are cycled through
// during a run; more variety than this does not change what is measured.
const maxSamplesPerKind = 5000

// The words names are made of. A real Directory's names are a team's
// vocabulary: systems, documents, teams, and the odd acronym.
var nameWords = []string{
	"api", "auth", "billing", "board", "budget", "build", "calendar", "careers", "chat", "ci",
	"cloud", "config", "cost", "crm", "dash", "data", "db", "deploy", "design", "dev",
	"docs", "eng", "events", "expenses", "faq", "finance", "flags", "forms", "gh", "git",
	"grafana", "guide", "handbook", "help", "hiring", "holidays", "hr", "incident", "infra", "interview",
	"issues", "jira", "k8s", "kb", "kibana", "launch", "legal", "logs", "mail", "metrics",
	"mobile", "notes", "okrs", "oncall", "onboarding", "ops", "org", "pager", "payroll", "people",
	"perf", "plan", "platform", "policy", "portal", "postmortem", "pr", "prod", "product", "q1",
	"q2", "q3", "q4", "qa", "release", "reports", "repo", "retro", "roadmap", "runbook",
	"sales", "search", "security", "sentry", "slides", "slo", "spec", "sprint", "sre", "staging",
	"standup", "status", "support", "team", "tickets", "tools", "town-hall", "travel", "vault", "vpn",
	"web", "wiki", "zoom", "android", "ios", "frontend", "backend", "ml", "analytics", "growth",
	"brand", "marketing", "partners", "contracts", "benefits", "office", "it", "laptop", "access", "sso",
}

// The words descriptions use besides the name words.
var descriptionWords = []string{
	"dashboard", "runbook", "tracker", "notes", "overview", "schedule", "checklist", "template",
	"process", "playbook", "folder", "sheet", "archive", "review", "proposal", "summary",
}

var descriptionForms = []string{
	"%s %s",
	"The %s %s for the team",
	"Where the %s %s lives",
	"%s %s, updated weekly",
	"Shared %s %s (ask in #help if you cannot open it)",
	"%s %s for new joiners",
	"Current %s %s; the old one is archived",
	"%s %s with links to everything else about it, kept up to date by the owners",
}

// The hosts destinations point at, with the path shape a real tool uses.
// Everything is under example.com (RFC 2606) so nothing generated points
// anywhere real, and never at the Directory itself.
var plainDestinations = []func(g *generator, name string) string{
	func(g *generator, name string) string {
		return "https://docs.example.com/document/d/" + g.token(32) + "/edit"
	},
	func(g *generator, name string) string {
		return "https://github.example.com/acme/" + name
	},
	func(g *generator, name string) string {
		return fmt.Sprintf("https://grafana.example.com/d/%s/%s?orgId=1&refresh=30s", g.token(9), name)
	},
	func(g *generator, name string) string {
		return "https://wiki.example.com/display/ENG/" + strings.ReplaceAll(name, "-", "+")
	},
	func(g *generator, name string) string {
		return "https://drive.example.com/drive/folders/" + g.token(28)
	},
	func(g *generator, name string) string {
		return "https://" + name + ".example.com/"
	},
	func(g *generator, name string) string {
		return fmt.Sprintf("https://sheets.example.com/spreadsheets/d/%s/edit#gid=%d", g.token(40), g.intn(100000))
	},
}

// The share of each kind of link, in tenths of a percent; they add up to
// 1000. Most go links are a single address; a fair number take a search
// term; few have several routes, and very few many.
const (
	sharePlain   = 700
	shareSearch  = 150
	shareTyped   = 90
	shareReports = 40
	shareWide    = 20
)

// generator is a fixed pseudo-random stream: the same seed gives the same
// Directory on every run and every machine, without math/rand's
// version-dependent streams (the benchmarks in internal/app do the same).
type generator struct {
	state uint64
}

func (g *generator) next() uint64 {
	// splitmix64: small, fast and good enough to pick words.
	g.state += 0x9e3779b97f4a7c15
	z := g.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (g *generator) intn(n int) int { return int(g.next() % uint64(n)) }

func (g *generator) pick(words []string) string { return words[g.intn(len(words))] }

const tokenChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// token is an opaque ID such as a document's, as long as n.
func (g *generator) token(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = tokenChars[g.intn(len(tokenChars))]
	}
	return string(b)
}

// baseTime is when the oldest generated link was created. Links are spread
// over the two years after it, so "Recently added" orders real timestamps.
var baseTime = time.Date(2024, 10, 1, 9, 0, 0, 0, time.UTC)

// Generate returns n links owned by ownerID, and the requests that exercise
// them. The same n and seed always give the same result. It fails only if the
// generator itself is wrong: every link it writes must pass the link model's
// save rules.
func Generate(n int, seed uint64, ownerID int64) (*Generated, error) {
	if n < 1 {
		return nil, fmt.Errorf("perf: cannot generate %d links", n)
	}
	g := &generator{state: seed}
	out := &Generated{Links: make([]linkmodel.Link, 0, n)}
	names := make(map[string]bool, n)
	// One in every `every` link contributes its requests, so the samples are
	// spread over the whole Directory, not taken from its first links.
	every := max(1, n/maxSamplesPerKind)
	var routeID int64

	for len(out.Links) < n {
		name := g.name()
		if names[name] || linkmodel.ReservedName(name) || !linkmodel.PossibleName(name) {
			continue
		}
		draft, hits, misses := g.link(name)
		link, problems := linkmodel.ValidateDraft(draft)
		if problems.HasErrors() {
			first, _ := problems.First()
			return nil, fmt.Errorf("perf: generated link %q is invalid: %s", name, first.Message)
		}
		names[name] = true
		at := baseTime.Add(time.Duration(len(out.Links)) * (2 * 365 * 24 * time.Hour / time.Duration(n))).Truncate(time.Second)
		link.ID, link.OwnerID, link.Version = int64(len(out.Links)+1), ownerID, 1
		link.CreatedAt, link.UpdatedAt = at, at
		for i := range link.Routes {
			routeID++
			link.Routes[i].ID, link.Routes[i].Position = routeID, i
		}
		out.Links = append(out.Links, link)

		if len(out.Links)%every == 0 {
			out.Hits = appendCapped(out.Hits, hits...)
			out.KnownMisses = appendCapped(out.KnownMisses, misses...)
		}
	}

	for len(out.UnknownMisses) < min(maxSamplesPerKind, max(10, n/10)) {
		var requested string
		if g.intn(2) == 0 {
			requested = g.typo(out.Links[g.intn(len(out.Links))].Name)
		} else {
			requested = g.gibberish()
		}
		// The typo may have made another real name, or something that
		// cannot be a name at all ("-ab"), which is not a miss but a bad
		// request.
		if names[linkmodel.NormalizeName(requested)] || !linkmodel.PossibleName(requested) {
			continue
		}
		path := "/" + requested
		if g.intn(3) == 0 {
			path += fmt.Sprintf("/%d", 1+g.intn(5000)) // a value to carry over
		}
		out.UnknownMisses = append(out.UnknownMisses, path)
	}

	for range 500 {
		out.SearchOne = append(out.SearchOne, g.searchWord(names))
		out.SearchTwo = append(out.SearchTwo, g.searchWord(names)+" "+g.searchWord(names))
	}
	return out, nil
}

func appendCapped(list []string, paths ...string) []string {
	for _, p := range paths {
		if len(list) < maxSamplesPerKind {
			list = append(list, p)
		}
	}
	return list
}

// name is a go link name as people make them: one to three words, mostly
// joined by "-", sometimes with a number ("q3-okrs-2026", "oncall2").
func (g *generator) name() string {
	var b strings.Builder
	words := []int{1, 2, 2, 2, 3, 3}[g.intn(6)]
	for w := 0; w < words; w++ {
		if w > 0 {
			b.WriteString([]string{"-", "-", "-", "-", "_", ""}[g.intn(6)])
		}
		b.WriteString(g.pick(nameWords))
	}
	switch g.intn(8) {
	case 0:
		fmt.Fprintf(&b, "-%d", 2019+g.intn(8))
	case 1:
		fmt.Fprintf(&b, "%d", 1+g.intn(20))
	}
	return b.String()
}

func (g *generator) description(name string) string {
	if g.intn(7) == 0 {
		return "" // many links are saved without one
	}
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	subject := strings.ToUpper(words[0][:1]) + words[0][1:]
	if len(words) > 1 {
		subject += " " + strings.Join(words[1:], " ")
	}
	form := g.pick(descriptionForms)
	if strings.HasPrefix(form, "%s") {
		return fmt.Sprintf(form, subject, g.pick(descriptionWords))
	}
	return fmt.Sprintf(form, strings.ToLower(subject), g.pick(descriptionWords))
}

// link writes the draft of one link named name, the paths it answers, and
// paths that name it but that none of its routes fits.
func (g *generator) link(name string) (d linkmodel.Draft, hits, misses []string) {
	d.Description = g.description(name)
	route := func(pattern, destination string, vars ...linkmodel.VariableMeta) {
		d.Routes = append(d.Routes, linkmodel.DraftRoute{Pattern: pattern, Destination: destination, Variables: vars})
	}
	// Half the variables carry the description and example a careful owner
	// writes, so the variables table is not empty either.
	described := g.intn(2) == 0
	meta := func(variable, description, example string) []linkmodel.VariableMeta {
		if !described {
			return nil
		}
		return []linkmodel.VariableMeta{{Name: variable, Description: description, Example: example}}
	}
	host := "https://" + name + ".example.com"

	switch k := g.intn(1000); {
	case k < sharePlain:
		route(name, plainDestinations[g.intn(len(plainDestinations))](g, name))
		hits = []string{"/" + name}
		misses = []string{"/" + name + "/" + g.pick(nameWords)}

	case k < sharePlain+shareSearch:
		// A search box: go/name/term. Half also open the tool itself.
		if g.intn(2) == 0 {
			route(name, host+"/")
			hits = append(hits, "/"+name)
		}
		route(name+"/{q}", host+"/search?q={q}&sort=relevance", meta("q", "What to search for", "deploy")...)
		hits = append(hits, "/"+name+"/"+g.pick(nameWords))
		misses = []string{fmt.Sprintf("/%s/%s/%s", name, g.pick(nameWords), g.pick(nameWords))}

	case k < sharePlain+shareSearch+shareTyped:
		// A tracker: go/name, go/name/new, go/name/1234, go/name/OPS-42/comments.
		route(name, host+"/")
		route(name+"/new", host+"/issues/new")
		route(name+"/{id:int}", host+"/issues/{id}", meta("id", "Issue number", "1234")...)
		route(name+"/{key:regex([A-Z]+-[0-9]+)}/{tab}", host+"/browse/{key}?tab={tab}",
			linkmodel.VariableMeta{Name: "key", Description: "Ticket key", Example: "OPS-42"})
		hits = []string{
			"/" + name,
			fmt.Sprintf("/%s/%d", name, 1+g.intn(99999)),
			fmt.Sprintf("/%s/OPS-%d/comments", name, 1+g.intn(9999)),
		}
		misses = []string{fmt.Sprintf("/%s/ops42/comments", name), fmt.Sprintf("/%s/%d/x/y", name, g.intn(100))}

	case k < sharePlain+shareSearch+shareTyped+shareReports:
		// Reports by date, with the month expression of PRD 5.7.
		route(name, host+"/reports")
		route(name+"/{year:int}/{month:regex(^(0?[1-9]|1[0-2])$)}", host+"/reports?year={year}&month={month}",
			meta("month", "Month, 1 to 12", "10")...)
		route(name+"/{year:int}/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", host+"/reports/daily?y={year}&m={month}&d={day}")
		hits = []string{
			fmt.Sprintf("/%s/%d/%d", name, 2020+g.intn(7), 1+g.intn(12)),
			fmt.Sprintf("/%s/%d/%02d/%d", name, 2020+g.intn(7), 1+g.intn(12), 1+g.intn(28)),
		}
		misses = []string{fmt.Sprintf("/%s/%d/13", name, 2020+g.intn(7)), fmt.Sprintf("/%s/last-year/10", name)}

	default:
		// A monorepo or a wiki space with many areas: 6 to the maximum of
		// routes, so the "why it didn't fit" list of a miss is long.
		route(name, host+"/")
		areas := 5 + g.intn(linkmodel.MaxRoutes-5)
		for i := 0; i < areas; i++ {
			switch i % 3 {
			case 0:
				route(fmt.Sprintf("%s/area%d/{id:int}", name, i), fmt.Sprintf("%s/area%d/items/{id}", host, i))
			case 1:
				route(fmt.Sprintf("%s/area%d/{slug:regex([a-z]+-[0-9]+)}", name, i), fmt.Sprintf("%s/area%d/{slug}", host, i))
			default:
				route(fmt.Sprintf("%s/area%d/{page}", name, i), fmt.Sprintf("%s/area%d/wiki/{page}", host, i))
			}
		}
		hits = []string{
			fmt.Sprintf("/%s/area0/%d", name, g.intn(5000)),
			fmt.Sprintf("/%s/area1/build-%d", name, g.intn(500)),
		}
		misses = []string{fmt.Sprintf("/%s/area%d/a/b", name, g.intn(areas)), fmt.Sprintf("/%s/nowhere", name)}
	}
	return d, hits, misses
}

// typo is name with one slip of the fingers: two letters swapped, one
// dropped, or one doubled. Sometimes typed with a capital, as people do.
func (g *generator) typo(name string) string {
	b := []byte(name)
	switch i := g.intn(len(b)); g.intn(3) {
	case 0:
		if i+1 < len(b) {
			b[i], b[i+1] = b[i+1], b[i]
		}
	case 1:
		if len(b) > 2 {
			b = append(b[:i:i], b[i+1:]...)
		}
	default:
		b = append(b[:i+1:i+1], b[i:]...)
	}
	s := string(b)
	if g.intn(5) == 0 {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

// gibberish is a name like nothing in the Directory, of a realistic length.
func (g *generator) gibberish() string {
	const letters = "bcdfghjklmnpqrstvwxz"
	b := make([]byte, 6+g.intn(14))
	for i := range b {
		b[i] = letters[g.intn(len(letters))]
	}
	if len(b) > 8 {
		b[len(b)/2] = '-'
	}
	return string(b)
}

// searchWord is one word of a query as it reaches the API while typing: a
// whole word, the first letters of one, a misspelled name, or a word that
// only descriptions or destinations hold.
func (g *generator) searchWord(names map[string]bool) string {
	switch g.intn(6) {
	case 0, 1:
		return g.pick(nameWords)
	case 2:
		w := g.pick(nameWords)
		return w[:min(len(w), 2+g.intn(3))]
	case 3:
		return g.typo(g.pick(nameWords))
	case 4:
		return g.pick(descriptionWords)
	default:
		return []string{"github", "grafana", "docs", "wiki", "sheets", "drive", "reports", "issues"}[g.intn(8)]
	}
}
