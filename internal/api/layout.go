package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// Pages are made of rows: built-in shelves (Continue Watching, a TMDB
// chart), hub categories (one row of hub tiles), single hubs and
// collections. Each page has a layout: the user's own, else the admin's
// default, else the built-in one below.

// layoutRow is one resolved row of a page.
type layoutRow struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "landscape", "items", "titles", "hubs" or "collection"
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	// Items are library items, Titles TMDB titles linked to the library.
	Items      []*db.Item     `json:"items,omitempty"`
	Titles     []titleView    `json:"titles,omitempty"`
	Hubs       []hubCard      `json:"hubs,omitempty"`
	Style      string         `json:"style,omitempty"` // a hub row's "tiles" or "logos"
	Collection *db.Collection `json:"collection,omitempty"`
	Missing    []titleView    `json:"missing,omitempty"`
	Ranked     bool           `json:"ranked,omitempty"`
	// HubID is set on a single hub's row, which opens the hub.
	HubID string `json:"hubId,omitempty"`
}

// hubCard is a hub as a tile in a row.
type hubCard struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Tagline      string `json:"tagline,omitempty"`
	BackdropPath string `json:"backdropPath,omitempty"`
	LogoPath     string `json:"logoPath,omitempty"`
	Color        string `json:"color,omitempty"`
}

// pageScope is the page a layout belongs to and the libraries it draws on.
type pageScope struct {
	key  string        // "home", "movies", "shows" or "library:<id>"
	kind string        // item kind: "movie", "show" or "movie,show"
	lib  *db.Library   // a library page's library
	libs []*db.Library // visible libraries of the page's kind
	ids  []string
}

func (sc *pageScope) home() bool { return sc.key == "home" }

// tmdbKinds are the TMDB media types the page shows.
func (sc *pageScope) tmdbKinds() []string {
	switch sc.kind {
	case "movie":
		return []string{"movie"}
	case "show":
		return []string{"tv"}
	}
	return []string{"movie", "tv"}
}

func (s *Server) scopeFor(r *http.Request, key string) (*pageScope, error) {
	libs, _, err := s.visibleLibraries(r)
	if err != nil {
		return nil, err
	}
	sc := &pageScope{key: key, kind: "movie,show"}
	switch {
	case key == "home":
	case key == "movies":
		sc.kind = "movie"
	case key == "shows":
		sc.kind = "show"
	case strings.HasPrefix(key, "library:"):
		id := strings.TrimPrefix(key, "library:")
		for _, l := range libs {
			if l.ID == id {
				sc.lib = l
			}
		}
		if sc.lib == nil {
			return nil, errNotFound
		}
		sc.kind = libKind(sc.lib)
		libs = []*db.Library{sc.lib}
	default:
		return nil, errNotFound
	}
	for _, l := range libs {
		if sc.kind == "movie,show" || libKind(l) == sc.kind {
			sc.libs = append(sc.libs, l)
			sc.ids = append(sc.ids, l.ID)
		}
	}
	return sc, nil
}

var errNotFound = fmt.Errorf("page not found")

func libKind(l *db.Library) string {
	if l.Type == "shows" {
		return "show"
	}
	return "movie"
}

func tmdbType(mediaType string) string {
	if mediaType == "show" || mediaType == "tv" {
		return "tv"
	}
	return "movie"
}

// ---------- built-in shelves ----------

type shelfInfo struct {
	ref, title string
	home       bool // only on Home
	tmdb       bool // needs TMDB
	media      string
}

// builtinShelves are the rows any layout can use, in the editor's order.
var builtinShelves = []shelfInfo{
	{ref: "continue", title: "Continue Watching"},
	{ref: "watchlist", title: "My Watchlist", home: true},
	{ref: "recentlyWatched", title: "Recently Watched", home: true},
	{ref: "because", title: "Because You Watched", home: true, tmdb: true},
	{ref: "recentlyAdded", title: "Recently Added"},
	{ref: "topRated", title: "Top Rated in Your Library"},
	{ref: "unwatched", title: "Unwatched"},
	{ref: "trending", title: "Trending Now", home: true, tmdb: true},
	{ref: "chart:trending:movie", title: "Trending Movies", tmdb: true, media: "movie"},
	{ref: "chart:trending:tv", title: "Trending Series", tmdb: true, media: "tv"},
	{ref: "chart:popular:movie", title: "Popular Movies", tmdb: true, media: "movie"},
	{ref: "chart:popular:tv", title: "Popular Series", tmdb: true, media: "tv"},
	{ref: "chart:now_playing:movie", title: "New Releases", tmdb: true, media: "movie"},
	{ref: "chart:upcoming:movie", title: "Coming Soon", tmdb: true, media: "movie"},
	{ref: "chart:airing:tv", title: "New Episodes", tmdb: true, media: "tv"},
	{ref: "chart:top_rated:movie", title: "Top Rated Movies", tmdb: true, media: "movie"},
	{ref: "chart:top_rated:tv", title: "Top Rated Series", tmdb: true, media: "tv"},
}

func shelfByRef(ref string) (shelfInfo, bool) {
	for _, sh := range builtinShelves {
		if sh.ref == ref {
			return sh, true
		}
	}
	return shelfInfo{}, false
}

// shelfFits reports whether a built-in shelf belongs on the page.
func (s *Server) shelfFits(sc *pageScope, sh shelfInfo) bool {
	if sh.home && !sc.home() {
		return false
	}
	if sh.tmdb && !s.TMDB.Enabled() {
		return false
	}
	return sh.media == "" || slices.Contains(sc.tmdbKinds(), sh.media)
}

// genresRef is the page's own genre category: one hub per genre its
// titles have.
func genresRef(sc *pageScope) string {
	kind := sc.kind
	if kind == "movie,show" {
		kind = "all"
	}
	lib := ""
	if sc.lib != nil {
		lib = sc.lib.ID
	}
	return "genres:" + kind + ":" + lib
}

// builtinLayout is a page's rows before anyone has arranged them.
func (s *Server) builtinLayout(r *http.Request, sc *pageScope) []db.LayoutEntry {
	shelf := func(refs ...string) []db.LayoutEntry {
		var out []db.LayoutEntry
		for _, ref := range refs {
			if sh, ok := shelfByRef(ref); ok && s.shelfFits(sc, sh) {
				out = append(out, db.LayoutEntry{Type: "shelf", Ref: ref})
			}
		}
		return out
	}
	category := func(ref string) []db.LayoutEntry { return []db.LayoutEntry{{Type: "category", Ref: ref}} }
	cols := s.scopeCollections(r.Context(), r, sc)
	var out []db.LayoutEntry
	add := func(e ...[]db.LayoutEntry) {
		for _, x := range e {
			out = append(out, x...)
		}
	}
	streaming := s.TMDB.Enabled() && s.Streaming.Active() && s.canStream(r)
	switch {
	case sc.home() && streaming:
		add(shelf("continue", "watchlist", "trending"), cols, shelf("recentlyWatched", "because"), category(db.ServicesCategoryID),
			shelf("chart:popular:movie", "chart:popular:tv"), category(db.GenresCategoryID),
			shelf("chart:now_playing:movie", "chart:airing:tv", "chart:top_rated:movie", "chart:top_rated:tv"))
	case sc.home():
		add(shelf("continue", "watchlist", "trending"), cols, shelf("recentlyAdded", "topRated"), category(genresRef(sc)))
	case sc.lib == nil && sc.kind == "movie":
		add(shelf("continue"), category(genresRef(sc)), shelf("recentlyAdded"), cols,
			shelf("chart:trending:movie", "chart:popular:movie", "chart:now_playing:movie", "chart:upcoming:movie", "chart:top_rated:movie"))
	case sc.lib == nil:
		add(shelf("continue"), category(genresRef(sc)), shelf("recentlyAdded"), cols,
			shelf("chart:trending:tv", "chart:popular:tv", "chart:airing:tv", "chart:top_rated:tv"))
	default:
		add(shelf("continue"), category(genresRef(sc)), shelf("recentlyAdded"), cols, shelf("topRated", "unwatched"))
	}
	return out
}

// scopeCollections are the collections an admin put on the page (Home, or
// a library's own rows), in their order.
func (s *Server) scopeCollections(ctx context.Context, r *http.Request, sc *pageScope) []db.LayoutEntry {
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		return nil
	}
	if !sc.home() {
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].LibraryOrder < cs[j].LibraryOrder })
	}
	now := time.Now()
	var out []db.LayoutEntry
	for _, c := range cs {
		on := c.ShowOnHome
		if !sc.home() {
			on = c.ShowInLibrary && slices.Contains(sc.ids, c.LibraryID)
		}
		if on && c.ActiveOn(now) && visibleCollection(r, c) {
			out = append(out, db.LayoutEntry{Type: "collection", Ref: c.ID})
		}
	}
	return out
}

// effectiveLayout is the page's layout for the caller, and whether it's
// their own. Collections put on the page since it was saved are added at
// the end, so promoting one still works.
func (s *Server) effectiveLayout(ctx context.Context, r *http.Request, sc *pageScope) (entries []db.LayoutEntry, custom bool) {
	uid := principal(r).User.ID
	entries, custom, _ = s.DB.Layout(ctx, sc.key, uid)
	if !custom {
		var ok bool
		if entries, ok, _ = s.DB.Layout(ctx, sc.key, ""); !ok {
			return s.builtinLayout(r, sc), false
		}
	}
	return withNewCollections(entries, s.scopeCollections(ctx, r, sc)), custom
}

func withNewCollections(entries, cols []db.LayoutEntry) []db.LayoutEntry {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Type == "collection" {
			seen[e.Ref] = true
		}
	}
	for _, c := range cols {
		if !seen[c.Ref] {
			entries = append(entries, c)
		}
	}
	return entries
}

// ---------- resolving rows ----------

// resolveLayout turns a page's entries into rows, fetching them in parallel.
func (s *Server) resolveLayout(ctx context.Context, r *http.Request, sc *pageScope, entries []db.LayoutEntry) []layoutRow {
	parts := make([][]layoutRow, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, e := range entries {
		if e.Hidden {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			parts[i] = s.resolveEntry(ctx, r, sc, e)
		}()
	}
	wg.Wait()
	out := []layoutRow{}
	seen := map[string]bool{}
	for _, p := range parts {
		for _, row := range p {
			if !seen[row.ID] {
				seen[row.ID] = true
				out = append(out, row)
			}
		}
	}
	return out
}

func (s *Server) resolveEntry(ctx context.Context, r *http.Request, sc *pageScope, e db.LayoutEntry) []layoutRow {
	switch e.Type {
	case "shelf":
		return s.builtinRows(ctx, r, sc, e.Ref)
	case "category":
		if row := s.categoryRow(ctx, r, e.Ref); row != nil {
			return []layoutRow{*row}
		}
	case "hub":
		h, err := s.hubFor(ctx, r, e.Ref)
		if err != nil {
			return nil
		}
		for _, sh := range h.Shelves {
			if row := s.resolveShelf(ctx, r, sh, 4); row != nil {
				row.ID, row.Title, row.HubID = "hub:"+h.ID, h.Name, h.ID
				return []layoutRow{*row}
			}
		}
	case "collection":
		c, err := s.DB.GetCollection(ctx, e.Ref)
		if err != nil || !c.ActiveOn(time.Now()) || !visibleCollection(r, c) {
			return nil
		}
		if row := s.collectionRow(ctx, r, c); row != nil {
			return []layoutRow{*row}
		}
	}
	return nil
}

func (s *Server) collectionRow(ctx context.Context, r *http.Request, c *db.Collection) *layoutRow {
	items, missing, err := s.collectionItems(ctx, r, c, 30)
	if err != nil || len(items)+len(missing) == 0 {
		return nil
	}
	c.ItemCount, c.Active = len(items), true
	if len(missing) > 30 {
		missing = missing[:30]
	}
	return &layoutRow{ID: "collection:" + c.ID, Kind: "collection", Title: c.Name, Collection: c, Items: items, Missing: missing}
}

func (s *Server) builtinRows(ctx context.Context, r *http.Request, sc *pageScope, ref string) []layoutRow {
	sh, ok := shelfByRef(ref)
	if !ok || !s.shelfFits(sc, sh) {
		return nil
	}
	uid := principal(r).User.ID
	items := func(id, title string, list []*db.Item) []layoutRow {
		if len(list) == 0 {
			return nil
		}
		return []layoutRow{{ID: id, Kind: "items", Title: title, Items: list}}
	}
	query := func(q db.ItemQuery) []*db.Item {
		if len(sc.ids) == 0 {
			return nil
		}
		q.LibraryIDs, q.Kind, q.UserID = sc.ids, sc.kind, uid
		if q.Limit == 0 {
			q.Limit = 20
		}
		list, _, _ := s.DB.QueryItems(ctx, q)
		return list
	}
	switch {
	case ref == "continue":
		var list []*db.Item
		for _, it := range s.continueWatching(ctx, r, sc.ids) {
			if sc.kind == "movie,show" || (sc.kind == "movie") == (it.Kind == "movie") {
				list = append(list, it)
			}
		}
		if len(list) == 0 {
			return nil
		}
		return []layoutRow{{ID: "continue", Kind: "landscape", Title: sh.title, Items: list}}
	case ref == "watchlist":
		return items("watchlist", sh.title, s.watchlistRow(ctx, uid, sc.ids))
	case ref == "recentlyWatched":
		return items("recentlyWatched", sh.title, s.recentlyWatched(ctx, uid, sc.ids, 20))
	case ref == "recentlyAdded":
		var out []layoutRow
		for _, l := range sc.libs {
			list, _, _ := s.DB.QueryItems(ctx, db.ItemQuery{LibraryID: l.ID, Kind: libKind(l), Sort: "added", Limit: 20, UserID: uid})
			title := "Recently Added in " + l.Name
			if sc.lib != nil {
				title = sh.title
			}
			out = append(out, items("recentlyAdded:"+l.ID, title, list)...)
		}
		return out
	case ref == "topRated":
		if list := query(db.ItemQuery{Sort: "rating", MinRating: 7}); len(list) >= 4 {
			return items("topRated", sh.title, list)
		}
	case ref == "unwatched":
		return items("unwatched", sh.title, query(db.ItemQuery{Sort: "added", Unwatched: true}))
	case ref == "trending":
		ts, err := s.TMDB.Trending(ctx)
		if err != nil {
			return nil
		}
		views := s.titleRow(ctx, r, ts, true)
		if len(views) < 4 {
			return nil
		}
		row := layoutRow{ID: "trending", Kind: "titles", Title: sh.title, Titles: views, Ranked: true}
		if !s.canStream(r) {
			n := 0
			for _, v := range views {
				if v.Item != nil {
					n++
				}
			}
			row.Subtitle = fmt.Sprintf("%d in your library", n)
		}
		return []layoutRow{row}
	case ref == "because":
		var out []layoutRow
		for _, it := range s.recentTitles(ctx, uid, sc.ids, 3) {
			ts, err := s.becauseYouWatched(it)(ctx)
			if err != nil {
				continue
			}
			if views := s.titleRow(ctx, r, ts, false); len(views) >= 4 {
				out = append(out, layoutRow{ID: "because-" + it.ID, Kind: "titles", Title: "Because you watched " + it.Title, Titles: views})
			}
		}
		return out
	case strings.HasPrefix(ref, "chart:"):
		parts := strings.Split(ref, ":")
		ts, err := s.TMDB.Chart(ctx, parts[1], parts[2], 1)
		if err != nil {
			slog.Debug("layout chart", "ref", ref, "err", err)
			return nil
		}
		if views := s.titleRow(ctx, r, ts, false); len(views) >= 4 {
			return []layoutRow{{ID: ref, Kind: "titles", Title: sh.title, Titles: views, Ranked: parts[1] == "trending"}}
		}
	}
	return nil
}

// becauseYouWatched fetches TMDB's recommendations for a title.
func (s *Server) becauseYouWatched(it *db.Item) func(context.Context) ([]tmdb.Title, error) {
	return func(ctx context.Context) ([]tmdb.Title, error) {
		var ex *tmdb.Extras
		var err error
		if it.Kind == "show" {
			ex, err = s.TMDB.ShowExtras(ctx, it.TMDBID)
		} else {
			ex, err = s.TMDB.MovieExtras(ctx, it.TMDBID)
		}
		if err != nil {
			return nil, err
		}
		mt := map[bool]string{true: "tv", false: "movie"}[it.Kind == "show"]
		list := append(ex.Recommendations.Results, ex.Similar.Results...)
		for i := range list {
			list[i].MediaType = mt
		}
		return list, nil
	}
}

// titleRow is up to 20 TMDB titles with posters, linked to the library.
// Callers who can't stream only see what the server has, unless keepAll.
func (s *Server) titleRow(ctx context.Context, r *http.Request, ts []tmdb.Title, keepAll bool) []titleView {
	seen := map[string]bool{}
	views := []titleView{}
	for _, t := range ts {
		v := viewOf(t, "")
		key := fmt.Sprint(v.MediaType, v.TMDBID)
		if t.PosterPath == "" || seen[key] || (v.MediaType != "movie" && v.MediaType != "show") {
			continue
		}
		seen[key] = true
		views = append(views, v)
		if len(views) == 20 {
			break
		}
	}
	views = s.linkLibrary(ctx, r, views, false)
	if keepAll || s.canStream(r) {
		return views
	}
	kept := []titleView{}
	for _, v := range views {
		if v.Item != nil {
			kept = append(kept, v)
		}
	}
	return kept
}

// resolveShelf fetches one hub shelf. Rows with fewer than min titles are dropped.
func (s *Server) resolveShelf(ctx context.Context, r *http.Request, sh db.ShelfDef, min int) *layoutRow {
	row := &layoutRow{ID: sh.ID, Title: sh.Title, Ranked: sh.Ranked}
	switch sh.Source {
	case "discover", "chart":
		if !s.TMDB.Enabled() {
			return nil
		}
		var ts []tmdb.Title
		var err error
		if sh.Source == "chart" {
			ts, err = s.TMDB.Chart(ctx, sh.Chart, tmdbType(sh.MediaType), 1)
		} else {
			ts, _, err = s.TMDB.DiscoverWith(ctx, tmdbType(sh.MediaType), tmdb.DiscoverQuery{Sort: sh.Sort, Genres: sh.GenreIDs, Providers: sh.Providers})
		}
		if err != nil {
			slog.Debug("hub shelf", "shelf", sh.ID, "err", err)
			return nil
		}
		row.Kind, row.Titles = "titles", s.titleRow(ctx, r, ts, false)
		if len(row.Titles) < min {
			return nil
		}
	case "library":
		rules := sh.Rules
		if sh.MediaType != "" {
			rules.Kind = sh.MediaType
		}
		c := &db.Collection{Kind: "smart", Rules: rules, Sort: sh.Sort, LibraryID: sh.CollectionID}
		items, _, err := s.collectionItems(ctx, r, c, 30)
		if err != nil || len(items) < max(1, min/2) {
			return nil
		}
		row.Kind, row.Items = "items", items
	case "collection":
		c, err := s.DB.GetCollection(ctx, sh.CollectionID)
		if err != nil || !visibleCollection(r, c) {
			return nil
		}
		cr := s.collectionRow(ctx, r, c)
		if cr == nil {
			return nil
		}
		cr.ID = sh.ID
		if sh.Title != "" {
			cr.Title = sh.Title
		}
		return cr
	default:
		return nil
	}
	return row
}

// ---------- hubs ----------

// hubFor is a saved hub or a page's own genre hub ("genre:<kind>:<library>:<name>").
func (s *Server) hubFor(ctx context.Context, r *http.Request, id string) (*db.Hub, error) {
	if strings.HasPrefix(id, "genre:") {
		parts := strings.SplitN(id, ":", 4)
		if len(parts) != 4 || parts[3] == "" {
			return nil, db.ErrNotFound
		}
		if parts[2] != "" && !principal(r).CanAccess(parts[2]) {
			return nil, db.ErrNotFound
		}
		return s.genreHub(ctx, parts[1], parts[2], parts[3]), nil
	}
	return s.DB.GetHub(ctx, id)
}

// genreHub builds the hub for one genre of a page: the library's titles in
// it, then TMDB's when the server has TMDB.
func (s *Server) genreHub(ctx context.Context, kind, lib, name string) *db.Hub {
	h := &db.Hub{ID: "genre:" + kind + ":" + lib + ":" + name, Name: name, Kind: "genre"}
	mediaTypes := []string{kind}
	if kind == "all" {
		mediaTypes = []string{"movie", "show"}
	}
	rules := db.CollectionRules{Genres: []string{name}}
	if kind != "all" {
		rules.Kind = kind
	}
	noun := map[string]string{"movie": "Movies", "show": "Series", "all": "Titles"}[kind]
	library := func(id, title, sort string, unwatched bool) db.ShelfDef {
		ru := rules
		ru.Unwatched = unwatched
		// CollectionID on a library shelf scopes it to that library.
		return db.ShelfDef{ID: id, Title: title, Source: "library", Sort: sort, Rules: ru, CollectionID: lib}
	}
	h.Shelves = append(h.Shelves, library("recent", "Recently Added", "added", false))
	if s.TMDB.Enabled() {
		for _, mt := range mediaTypes {
			if g := s.tmdbGenreID(ctx, tmdbType(mt), name); g > 0 {
				n := map[string]string{"movie": "Movies", "show": "Series"}[mt]
				h.Shelves = append(h.Shelves,
					db.ShelfDef{ID: "popular-" + mt, Title: "Popular " + name + " " + n, Source: "discover", MediaType: mt, Sort: "popular", GenreIDs: []int{g}},
					db.ShelfDef{ID: "top-" + mt, Title: "Top Rated " + name + " " + n, Source: "discover", MediaType: mt, Sort: "top_rated", GenreIDs: []int{g}, Ranked: true},
					db.ShelfDef{ID: "new-" + mt, Title: "New " + name + " " + n, Source: "discover", MediaType: mt, Sort: "new", GenreIDs: []int{g}},
				)
			}
		}
	}
	h.Shelves = append(h.Shelves,
		library("rated", "Top Rated in Your Library", "rating", false),
		library("unwatched", "Unwatched "+noun, "added", true),
		library("all", "All "+name+" "+noun, "title", false),
	)
	return h
}

// genreAliases pairs the movie and series names TMDB gives the same genre.
var genreAliases = map[string]string{
	"science fiction":    "sci-fi & fantasy",
	"sci-fi & fantasy":   "science fiction",
	"action":             "action & adventure",
	"action & adventure": "action",
	"war":                "war & politics",
	"war & politics":     "war",
}

// tmdbGenreID finds a genre by name in TMDB's list for "movie" or "tv".
func (s *Server) tmdbGenreID(ctx context.Context, mediaType, name string) int {
	gs, err := s.TMDB.Genres(ctx, mediaType)
	if err != nil {
		return 0
	}
	n := strings.ToLower(name)
	for _, want := range []string{n, genreAliases[n]} {
		for _, g := range gs {
			if want != "" && strings.ToLower(g.Name) == want {
				return g.ID
			}
		}
	}
	return 0
}

// preferredGenres come first in a genre row; the rest follow alphabetically.
var preferredGenres = []string{"Action", "Action & Adventure", "Comedy", "Drama", "Science Fiction", "Sci-Fi & Fantasy", "Horror", "Thriller",
	"Crime", "Animation", "Documentary", "Romance", "Family", "Mystery", "Fantasy", "Adventure"}

// genreCards are the genre hubs of a page: the genres its libraries have,
// or with no libraries of the kind, TMDB's.
func (s *Server) genreCards(ctx context.Context, r *http.Request, kind, lib string) []hubCard {
	var ids []string
	if lib != "" {
		if !principal(r).CanAccess(lib) {
			return nil
		}
		ids = []string{lib}
	} else {
		libs, _, _ := s.visibleLibraries(r)
		for _, l := range libs {
			if kind == "all" || libKind(l) == kind {
				ids = append(ids, l.ID)
			}
		}
	}
	type genre struct {
		name, backdrop string
		count          int
	}
	var gs []genre
	counts, _ := s.DB.GenreCounts(ctx, ids)
	for _, g := range counts {
		n := g.Count
		if kind == "movie" {
			n = g.Movies
		} else if kind == "show" {
			n = g.Shows
		}
		if n >= 3 {
			gs = append(gs, genre{g.Genre, g.BackdropPath, n})
		}
	}
	// A streaming page browses all of TMDB, so it has every genre.
	if lib == "" && kind != "all" && s.TMDB.Enabled() && s.canStream(r) {
		have := map[string]bool{}
		for _, g := range gs {
			have[g.name] = true
		}
		if list, err := s.TMDB.Genres(ctx, tmdbType(kind)); err == nil {
			for _, g := range list {
				if !have[g.Name] {
					gs = append(gs, genre{name: g.Name})
				}
			}
		}
	}
	rank := func(name string) int {
		if i := slices.Index(preferredGenres, name); i >= 0 {
			return i
		}
		return len(preferredGenres)
	}
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := rank(gs[i].name), rank(gs[j].name)
		if a != b {
			return a < b
		}
		return gs[i].name < gs[j].name
	})
	cards := make([]hubCard, len(gs))
	var wg sync.WaitGroup
	for i, g := range gs {
		cards[i] = hubCard{ID: "genre:" + kind + ":" + lib + ":" + g.name, Name: g.name, Kind: "genre", BackdropPath: g.backdrop}
		if g.backdrop == "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cards[i].BackdropPath = s.hubArt(ctx, r, s.genreHub(ctx, kind, lib, g.name))
			}()
		}
	}
	wg.Wait()
	return cards
}

// categoryRow is a row of hub tiles: a saved category or a page's genres
// ("genres:<kind>:<library>").
func (s *Server) categoryRow(ctx context.Context, r *http.Request, ref string) *layoutRow {
	if strings.HasPrefix(ref, "genres:") {
		parts := strings.SplitN(ref, ":", 3)
		if len(parts) != 3 {
			return nil
		}
		cards := s.genreCards(ctx, r, parts[1], parts[2])
		if len(cards) == 0 {
			return nil
		}
		return &layoutRow{ID: "category:" + ref, Kind: "hubs", Title: "Genres", Style: "tiles", Hubs: cards}
	}
	c, err := s.DB.GetHubCategory(ctx, ref)
	if err != nil {
		return nil
	}
	cards := make([]*hubCard, len(c.HubIDs))
	var wg sync.WaitGroup
	for i, id := range c.HubIDs {
		h, err := s.DB.GetHub(ctx, id)
		if err != nil || !s.hubAvailable(h) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cards[i] = s.cardOf(ctx, r, h)
		}()
	}
	wg.Wait()
	row := &layoutRow{ID: "category:" + c.ID, Kind: "hubs", Title: c.Name, Style: c.Style}
	for _, card := range cards {
		if card != nil {
			row.Hubs = append(row.Hubs, *card)
		}
	}
	if len(row.Hubs) == 0 {
		return nil
	}
	return row
}

// hubAvailable reports whether a hub can show anything: TMDB shelves need TMDB.
func (s *Server) hubAvailable(h *db.Hub) bool {
	for _, sh := range h.Shelves {
		if (sh.Source != "discover" && sh.Source != "chart") || s.TMDB.Enabled() {
			return true
		}
	}
	return false
}

func (s *Server) cardOf(ctx context.Context, r *http.Request, h *db.Hub) *hubCard {
	c := &hubCard{ID: h.ID, Name: h.Name, Kind: h.Kind, Tagline: h.Tagline, BackdropPath: h.BackdropPath, LogoPath: h.LogoPath, Color: h.Color}
	if c.BackdropPath == "" {
		c.BackdropPath = s.hubArt(ctx, r, h)
	}
	if c.LogoPath == "" && h.Kind == "service" {
		c.LogoPath = s.providerLogo(ctx, h)
	}
	return c
}

// hubArt is the backdrop of a hub's first title, remembered for a while
// since a row of tiles would otherwise fetch every hub's first shelf.
func (s *Server) hubArt(ctx context.Context, r *http.Request, h *db.Hub) string {
	key := h.ID + fmt.Sprint(h.UpdatedAt)
	artCache.Lock()
	e, ok := artCache.m[key]
	artCache.Unlock()
	if ok && time.Since(e.at) < 6*time.Hour {
		return e.path
	}
	path := ""
	for _, sh := range h.Shelves {
		row := s.resolveShelf(ctx, r, sh, 1)
		if row == nil {
			continue
		}
		for _, v := range row.Titles {
			if v.BackdropPath != "" {
				path = v.BackdropPath
				break
			}
		}
		for _, it := range row.Items {
			if it.BackdropPath != "" {
				path = it.BackdropPath
				break
			}
		}
		if path != "" {
			break
		}
	}
	artCache.Lock()
	artCache.m[key] = artEntry{path, time.Now()}
	artCache.Unlock()
	return path
}

type artEntry struct {
	path string
	at   time.Time
}

var artCache = struct {
	sync.Mutex
	m map[string]artEntry
}{m: map[string]artEntry{}}

// providerLogo is TMDB's logo for a service hub's watch provider.
func (s *Server) providerLogo(ctx context.Context, h *db.Hub) string {
	if !s.TMDB.Enabled() {
		return ""
	}
	id := 0
	for _, sh := range h.Shelves {
		if len(sh.Providers) > 0 {
			id = sh.Providers[0]
			break
		}
	}
	if id == 0 {
		return ""
	}
	ps, _ := s.TMDB.WatchProviders(ctx)
	for _, p := range ps {
		if p.ID == id {
			return p.LogoPath
		}
	}
	return ""
}

// resolveHub is a hub's page: every shelf with something in it.
func (s *Server) resolveHub(ctx context.Context, r *http.Request, h *db.Hub) []layoutRow {
	rows := make([]*layoutRow, len(h.Shelves))
	var wg sync.WaitGroup
	for i, sh := range h.Shelves {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = s.resolveShelf(ctx, r, sh, 1)
		}()
	}
	wg.Wait()
	out := []layoutRow{}
	for i, row := range rows {
		if row != nil {
			if row.ID == "" {
				row.ID = fmt.Sprint("shelf-", i)
			}
			out = append(out, *row)
		}
	}
	return out
}
