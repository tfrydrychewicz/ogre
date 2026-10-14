package httpd

import (
	"context"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// fakeQueries is the part of fakeService that answers the read queries of
// the web UI: search, a link by name, and the home page's lists. It is
// embedded in fakeService, so a test sets these fields on the fake itself.
type fakeQueries struct {
	// searchFn answers Search; nil finds nothing.
	searchFn func(q string, limit int) ([]app.SearchResult, error)
	// byNameFn answers LinkByName; nil finds nothing (app.ErrNotFound).
	byNameFn func(name string) (linkmodel.Link, bool, error)
	// yourLinks and recentLinks are what YourLinks and RecentLinks return,
	// cut to the limit asked for.
	yourLinks, recentLinks []linkmodel.Link

	searched []searchCall
}

type searchCall struct {
	q     string
	limit int
}

func (f *fakeService) Search(_ context.Context, actor app.Actor, q string, limit int) ([]app.SearchResult, error) {
	f.mu.Lock()
	f.searched = append(f.searched, searchCall{q, limit})
	f.actors = append(f.actors, actor)
	fn := f.searchFn
	f.mu.Unlock()
	if fn == nil {
		return []app.SearchResult{}, nil
	}
	return fn(q, limit)
}

func (f *fakeService) searches() []searchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]searchCall(nil), f.searched...)
}

func (f *fakeService) LinkByName(_ context.Context, actor app.Actor, name string) (linkmodel.Link, bool, error) {
	f.mu.Lock()
	f.actors = append(f.actors, actor)
	fn := f.byNameFn
	f.mu.Unlock()
	if fn == nil {
		return linkmodel.Link{}, false, app.ErrNotFound
	}
	return fn(name)
}

func (f *fakeService) YourLinks(_ context.Context, actor app.Actor, limit int) ([]linkmodel.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actors = append(f.actors, actor)
	return firstN(f.yourLinks, limit), nil
}

func (f *fakeService) RecentLinks(_ context.Context, limit int) ([]linkmodel.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return firstN(f.recentLinks, limit), nil
}

func (f *fakeService) OwnerName(context.Context, int64) string {
	if f.owner != "" {
		return f.owner
	}
	return "Local user"
}

// firstN returns at most n links of links, as a new slice; n of 0 or less
// means all of them.
func firstN(links []linkmodel.Link, n int) []linkmodel.Link {
	if n > 0 && n < len(links) {
		links = links[:n]
	}
	return append([]linkmodel.Link{}, links...)
}
