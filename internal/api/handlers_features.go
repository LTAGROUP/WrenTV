package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// ---------- preferences ----------

func (s *Server) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := s.DB.GetPrefs(r.Context(), principal(r).User.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handlePatchPrefs(w http.ResponseWriter, r *http.Request) {
	uid := principal(r).User.ID
	p, err := s.DB.GetPrefs(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var req struct {
		AudioLanguage    *string `json:"audioLanguage"`
		SubtitleLanguage *string `json:"subtitleLanguage"`
		WatchedPercent   *int    `json:"watchedPercent"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.AudioLanguage != nil {
		p.AudioLanguage = db.NormalizeLang(*req.AudioLanguage)
	}
	if req.SubtitleLanguage != nil {
		p.SubtitleLanguage = db.NormalizeLang(*req.SubtitleLanguage)
	}
	if req.WatchedPercent != nil {
		if *req.WatchedPercent < 50 || *req.WatchedPercent > 98 {
			writeErr(w, 400, "watchedPercent must be between 50 and 98")
			return
		}
		p.WatchedPercent = *req.WatchedPercent
	}
	if err := s.DB.SetPrefs(r.Context(), uid, p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

// ---------- genres ----------

// libraryScope is the caller's accessible libraries, narrowed to ?libraryId.
func (s *Server) libraryScope(r *http.Request) ([]string, error) {
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		return nil, err
	}
	if lid := r.URL.Query().Get("libraryId"); lid != "" {
		for _, id := range ids {
			if id == lid {
				return []string{lid}, nil
			}
		}
		return []string{}, nil
	}
	return ids, nil
}

func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	ids, err := s.libraryScope(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	gs, err := s.DB.GenreCounts(r.Context(), ids)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, gs)
}

func (s *Server) handleGenreItems(w http.ResponseWriter, r *http.Request) {
	g := r.URL.Query().Get("genre")
	if g == "" {
		writeErr(w, 400, "genre required")
		return
	}
	ids, err := s.libraryScope(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	kind := "movie,show"
	if k := r.URL.Query().Get("kind"); k == "movie" || k == "show" {
		kind = k
	}
	items, total, err := s.DB.QueryItems(r.Context(), db.ItemQuery{LibraryIDs: ids, Kind: kind, Genre: g, Sort: r.URL.Query().Get("sort"),
		Limit: queryInt(r, "limit", 5000), UserID: principal(r).User.ID, Unwatched: r.URL.Query().Get("unwatched") == "1"})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": nonNil(items), "total": total})
}

// ---------- collections ----------

// collectionItems resolves a collection to the library items the caller can
// see, plus (for TMDB sources) the titles the server doesn't have.
func (s *Server) collectionItems(ctx context.Context, r *http.Request, c *db.Collection, limit int) ([]*db.Item, []titleView, error) {
	_, ids, err := s.visibleLibraries(r)
	if err != nil {
		return nil, nil, err
	}
	if c.LibraryID != "" {
		scoped := []string{}
		for _, id := range ids {
			if id == c.LibraryID {
				scoped = append(scoped, id)
			}
		}
		ids = scoped
	}
	if c.ItemLimit > 0 && (limit <= 0 || c.ItemLimit < limit) {
		limit = c.ItemLimit
	}
	uid := principal(r).User.ID
	q := db.ItemQuery{LibraryIDs: ids, Kind: "movie,show", Sort: c.Sort, Limit: limit, UserID: uid}
	switch c.Kind {
	case "manual":
		q.CollectionID = c.ID
		if q.Sort == "" {
			q.Sort = "custom"
		}
	case "smart":
		ru := c.Rules
		if ru.Kind == "movie" || ru.Kind == "show" {
			q.Kind = ru.Kind
		}
		q.AnyGenres, q.ExcludeGenres = ru.Genres, ru.ExcludeGenres
		q.YearFrom, q.YearTo, q.MinRating = ru.YearFrom, ru.YearTo, ru.MinRating
		q.ContentRatings, q.Unwatched, q.MinHeight, q.HDR = ru.ContentRatings, ru.Unwatched, ru.MinHeight, ru.HDR
		q.Search = ru.TitleContains
		if ru.AddedDays > 0 {
			q.AddedSince = time.Now().AddDate(0, 0, -ru.AddedDays).Unix()
		}
	case "tmdb":
		return s.tmdbCollectionItems(ctx, r, c, q)
	}
	items, _, err := s.DB.QueryItems(ctx, q)
	return nonNil(items), []titleView{}, err
}

// collectionTitles is every title of a TMDB collection in its own order,
// linked to the library items the caller can see.
func (s *Server) collectionTitles(ctx context.Context, r *http.Request, c *db.Collection) []titleView {
	views := make([]titleView, 0, len(c.Titles))
	for _, t := range c.Titles {
		views = append(views, titleView{TMDBID: t.TMDBID, MediaType: t.MediaType, Title: t.Title, Year: t.Year, PosterPath: t.PosterPath})
	}
	return nonNil(s.linkLibrary(ctx, r, views, false))
}

// canStream reports whether the caller can play titles the server doesn't
// have. Then a TMDB collection holds everything in it.
func (s *Server) canStream(r *http.Request) bool {
	return s.limitsFor(principal(r).User).CanStream
}

func (s *Server) tmdbCollectionItems(ctx context.Context, r *http.Request, c *db.Collection, q db.ItemQuery) ([]*db.Item, []titleView, error) {
	// linkLibrary uses every accessible library; respect the collection's own scope.
	views := s.collectionTitles(ctx, r, c)
	streaming := s.canStream(r)
	var items []*db.Item
	missing := []titleView{}
	for _, v := range views {
		switch {
		case v.Item == nil:
			missing = append(missing, v)
		case streaming || len(q.LibraryIDs) == 0 || containsStr(q.LibraryIDs, v.Item.LibraryID):
			items = append(items, v.Item)
		}
	}
	if q.Sort != "" && q.Sort != "custom" {
		sortItems(items, q.Sort)
	}
	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}
	if !c.ShowMissing && !streaming {
		missing = []titleView{}
	}
	return nonNil(items), missing, nil
}

func sortItems(items []*db.Item, by string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch by {
		case "added":
			return a.AddedAt > b.AddedAt
		case "year":
			return a.Year > b.Year
		case "year_asc":
			return a.Year < b.Year
		case "rating":
			return a.Rating > b.Rating
		}
		return db.SortTitle(a.Title) < db.SortTitle(b.Title)
	})
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// visibleCollection reports whether the caller may see a collection now.
func visibleCollection(r *http.Request, c *db.Collection) bool {
	p := principal(r)
	if c.LibraryID != "" && !p.CanAccess(c.LibraryID) {
		return false
	}
	return p.IsAdmin() || c.ActiveOn(time.Now())
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.syncStaleCollections()
	lib := r.URL.Query().Get("libraryId")
	out := []*db.Collection{}
	for _, c := range cs {
		if !visibleCollection(r, c) || (lib != "" && c.LibraryID != "" && c.LibraryID != lib) {
			continue
		}
		items, _, err := s.collectionItems(ctx, r, c, 0)
		if err != nil {
			continue
		}
		// A collection that applies to all libraries shows under a library
		// only when it has something from it.
		if lib != "" && c.LibraryID == "" {
			n := 0
			for _, it := range items {
				if it.LibraryID == lib {
					n++
				}
			}
			if n == 0 {
				continue
			}
		}
		c.ItemCount = len(items)
		if c.Kind == "tmdb" && s.canStream(r) {
			c.ItemCount = len(c.Titles)
		}
		c.Active = c.ActiveOn(time.Now())
		if c.PosterPath == "" && len(items) > 0 {
			c.PosterPath = items[0].PosterPath
		}
		if c.BackdropPath == "" && len(items) > 0 {
			c.BackdropPath = items[0].BackdropPath
		}
		if c.ItemCount == 0 && !principal(r).IsAdmin() {
			continue
		}
		out = append(out, c)
	}
	writeJSON(w, 200, out)
}

func (s *Server) collectionFor(r *http.Request) (*db.Collection, bool) {
	c, err := s.DB.GetCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !visibleCollection(r, c) {
		return nil, false
	}
	return c, true
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collectionFor(r)
	if !ok {
		writeErr(w, 404, "collection not found")
		return
	}
	if srt := r.URL.Query().Get("sort"); srt != "" {
		c.Sort = srt
	}
	items, missing, err := s.collectionItems(r.Context(), r, c, 0)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	c.ItemCount = len(items)
	resp := map[string]any{"collection": c, "items": items, "missing": missing}
	// When streaming, a TMDB collection is everything in it, in its order.
	if c.Kind == "tmdb" && s.canStream(r) {
		resp["titles"] = s.collectionTitles(r.Context(), r, c)
		c.ItemCount = len(c.Titles)
	}
	c.Active = c.ActiveOn(time.Now())
	if c.BackdropPath == "" {
		for _, it := range items {
			if it.BackdropPath != "" {
				c.BackdropPath = it.BackdropPath
				break
			}
		}
	}
	writeJSON(w, 200, resp)
}

type collectionReq struct {
	Name         *string             `json:"name"`
	Description  *string             `json:"description"`
	Kind         *string             `json:"kind"`
	Rules        *db.CollectionRules `json:"rules"`
	LibraryID    *string             `json:"libraryId"`
	PosterPath   *string             `json:"posterPath"`
	Sort         *string             `json:"sort"`
	ItemLimit    *int                `json:"itemLimit"`
	ShowOnHome   *bool               `json:"showOnHome"`
	HomeOrder    *int                `json:"homeOrder"`
	ShowMissing  *bool               `json:"showMissing"`
	ShowInLib    *bool               `json:"showInLibrary"`
	LibraryOrder *int                `json:"libraryOrder"`
	VisibleFrom  *string             `json:"visibleFrom"`
	VisibleUntil *string             `json:"visibleUntil"`
	ItemIDs      []string            `json:"itemIds"`
}

func (req collectionReq) apply(c *db.Collection) error {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&c.Name, req.Name)
	set(&c.Description, req.Description)
	set(&c.Kind, req.Kind)
	set(&c.LibraryID, req.LibraryID)
	set(&c.PosterPath, req.PosterPath)
	set(&c.Sort, req.Sort)
	set(&c.VisibleFrom, req.VisibleFrom)
	set(&c.VisibleUntil, req.VisibleUntil)
	if req.Rules != nil {
		c.Rules = *req.Rules
	}
	if req.ItemLimit != nil {
		c.ItemLimit = max(0, *req.ItemLimit)
	}
	if req.ShowOnHome != nil {
		c.ShowOnHome = *req.ShowOnHome
	}
	if req.HomeOrder != nil {
		c.HomeOrder = *req.HomeOrder
	}
	if req.ShowMissing != nil {
		c.ShowMissing = *req.ShowMissing
	}
	if req.ShowInLib != nil {
		c.ShowInLibrary = *req.ShowInLib
	}
	if req.LibraryOrder != nil {
		c.LibraryOrder = *req.LibraryOrder
	}
	switch {
	case c.Name == "" && c.Kind != "tmdb":
		return errors.New("name required")
	case c.Kind != "manual" && c.Kind != "smart" && c.Kind != "tmdb":
		return errors.New(`kind must be "manual", "smart" or "tmdb"`)
	case c.Kind == "tmdb" && c.Rules.Source == "":
		return errors.New("a source is required")
	case c.Kind == "tmdb" && externalRef(c.Rules) != nil:
		return externalRef(c.Rules)
	case (c.VisibleFrom == "") != (c.VisibleUntil == ""):
		return errors.New("set both visibility dates, or neither")
	}
	for _, d := range []string{c.VisibleFrom, c.VisibleUntil} {
		if d != "" {
			if _, err := time.Parse("01-02", d); err != nil {
				return errors.New("visibility dates must be MM-DD")
			}
		}
	}
	return nil
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req collectionReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c := &db.Collection{Kind: "manual"}
	if err := req.apply(c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ownWatchlist(r, c, db.CollectionRules{})
	if c.Kind == "tmdb" {
		if err := s.syncCollection(r.Context(), c); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}
	if err := s.DB.SaveCollection(r.Context(), c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(req.ItemIDs) > 0 && c.Kind == "manual" {
		_ = s.DB.AddToCollection(r.Context(), c.ID, req.ItemIDs)
	}
	writeJSON(w, 201, c)
}

func (s *Server) handleUpdateCollection(w http.ResponseWriter, r *http.Request) {
	c, err := s.DB.GetCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "collection not found")
		return
	}
	var req collectionReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	before := c.Rules
	if err := req.apply(c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ownWatchlist(r, c, before)
	if c.Kind == "tmdb" && (req.Rules != nil && (before.Source != c.Rules.Source || before.SourceID != c.Rules.SourceID || before.MediaType != c.Rules.MediaType || before.SourceRef != c.Rules.SourceRef)) {
		if err := s.syncCollection(r.Context(), c); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}
	if err := s.DB.SaveCollection(r.Context(), c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteCollection(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCollectionAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemIDs []string `json:"itemIds"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c, err := s.DB.GetCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil || c.Kind != "manual" {
		writeErr(w, 404, "manual collection not found")
		return
	}
	if err := s.DB.AddToCollection(r.Context(), c.ID, req.ItemIDs); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCollectionRemove(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.RemoveFromCollection(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "itemId")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCollectionOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemIDs []string `json:"itemIds"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.DB.ReorderCollection(r.Context(), chi.URLParam(r, "id"), req.ItemIDs); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSyncCollection(w http.ResponseWriter, r *http.Request) {
	c, err := s.DB.GetCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "collection not found")
		return
	}
	if c.Kind == "tmdb" {
		if err := s.syncCollection(r.Context(), c); err != nil {
			c.SyncError = err.Error()
		}
		_ = s.DB.SaveCollection(r.Context(), c)
	}
	writeJSON(w, 200, c)
}

// handleItemCollections lists the manual collections containing an item.
func (s *Server) handleItemCollections(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ids, err := s.DB.CollectionsWithItem(r.Context(), it.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ids)
}

func (s *Server) handleSearchFranchises(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Enabled() {
		writeErr(w, 400, "add a TMDB key in Settings first")
		return
	}
	res, err := s.TMDB.SearchCollections(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(res))
}

// ownWatchlist ties a Simkl watchlist collection to the account of the
// admin who picked it; it stays with them until the source changes.
func ownWatchlist(r *http.Request, c *db.Collection, before db.CollectionRules) {
	ru := &c.Rules
	switch {
	case ru.Source != "simkl" || !strings.HasPrefix(ru.SourceRef, "watchlist/"):
		ru.SimklUser = ""
	case before.Source == ru.Source && before.SourceRef == ru.SourceRef && before.SimklUser != "":
		ru.SimklUser = before.SimklUser
	default:
		ru.SimklUser = principal(r).User.ID
	}
}

// syncCollection refreshes a TMDB collection's title list.
func (s *Server) syncCollection(ctx context.Context, c *db.Collection) error {
	if !s.TMDB.Enabled() {
		return errors.New("TMDB collections need a TMDB key (Settings)")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ru := c.Rules
	mt := "movie"
	if ru.MediaType == "show" {
		mt = "tv"
	}
	var titles []tmdb.Title
	switch ru.Source {
	case "mdblist", "simkl":
		var name string
		var ts []db.CollectionTitle
		var err error
		if ru.Source == "mdblist" {
			name, ts, err = mdblistTitles(ctx, ru.SourceRef)
		} else {
			name, ts, err = s.simklTitles(ctx, ru.SourceRef, ru.SimklUser)
		}
		if err != nil {
			return err
		}
		if len(ts) > maxExternal {
			ts = ts[:maxExternal]
		}
		s.withPosters(ctx, ts)
		if c.Name == "" {
			c.Name = name
		}
		c.Titles, c.SyncedAt, c.SyncError = ts, db.Now(), ""
		return nil
	case "collection":
		f, err := s.TMDB.Collection(ctx, ru.SourceID)
		if err != nil {
			return err
		}
		titles = f.Parts
		sort.SliceStable(titles, func(i, j int) bool {
			a, b := titles[i].ReleaseDate, titles[j].ReleaseDate
			return a != "" && (b == "" || a < b)
		})
		if c.Name == "" {
			c.Name = f.Name
		}
		if c.Description == "" {
			c.Description = f.Overview
		}
		if c.PosterPath == "" {
			c.PosterPath = f.PosterPath
		}
		if c.BackdropPath == "" {
			c.BackdropPath = f.BackdropPath
		}
	case "list":
		l, err := s.TMDB.List(ctx, ru.SourceID)
		if err != nil {
			return err
		}
		titles = l.Items
		if c.Name == "" {
			c.Name = l.Name
		}
		if c.Description == "" {
			c.Description = l.Description
		}
	default:
		ts, err := s.TMDB.Chart(ctx, ru.Source, mt, 5)
		if err != nil {
			return err
		}
		titles = ts
		if c.Name == "" {
			c.Name = chartName(ru.Source, mt)
		}
	}
	c.Titles = c.Titles[:0]
	for _, t := range titles {
		if t.MediaType != "movie" && t.MediaType != "tv" {
			continue
		}
		v := viewOf(t, "")
		c.Titles = append(c.Titles, db.CollectionTitle{TMDBID: v.TMDBID, MediaType: v.MediaType, Title: v.Title, Year: v.Year, PosterPath: v.PosterPath})
	}
	c.SyncedAt = db.Now()
	c.SyncError = ""
	return nil
}

func chartName(source, mt string) string {
	kind := "Movies"
	if mt == "tv" {
		kind = "Shows"
	}
	switch source {
	case "trending":
		return "Trending " + kind
	case "popular":
		return "Popular " + kind
	case "top_rated":
		return "Top Rated " + kind
	case "now_playing":
		return "In Theaters"
	case "upcoming":
		return "Coming Soon"
	case "airing":
		return "On the Air"
	}
	return "TMDB " + kind
}

var collectionSyncRunning atomic.Bool

// syncStaleCollections refreshes TMDB collections older than 12 hours in the background.
func (s *Server) syncStaleCollections() {
	if !s.TMDB.Enabled() || !collectionSyncRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer collectionSyncRunning.Store(false)
		ctx := context.Background()
		cs, err := s.DB.ListCollections(ctx)
		if err != nil {
			return
		}
		for _, c := range cs {
			if c.Kind != "tmdb" || db.Now()-c.SyncedAt < 12*3600 {
				continue
			}
			if err := s.syncCollection(ctx, c); err != nil {
				c.SyncError = err.Error()
				slog.Debug("collection sync", "collection", c.Name, "err", err)
			}
			_ = s.DB.SaveCollection(ctx, c)
		}
	}()
}

// handleAutoFranchises creates a TMDB collection for every franchise with at
// least two movies in the library (like Plex's automatic collections).
func (s *Server) handleAutoFranchises(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Enabled() {
		writeErr(w, 400, "add a TMDB key in Settings first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	movies, _, err := s.DB.QueryItems(ctx, db.ItemQuery{Kind: "movie"})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type group struct {
		ref  *tmdb.CollectionRef
		libs map[string]bool
		n    int
	}
	groups := map[int]*group{}
	for _, m := range movies {
		if m.TMDBID <= 0 {
			continue
		}
		md, err := s.TMDB.Movie(ctx, m.TMDBID) // cached from the scan
		if err != nil || md.BelongsToCollection == nil {
			continue
		}
		g := groups[md.BelongsToCollection.ID]
		if g == nil {
			g = &group{ref: md.BelongsToCollection, libs: map[string]bool{}}
			groups[g.ref.ID] = g
		}
		g.libs[m.LibraryID] = true
		g.n++
	}
	created := 0
	for id, g := range groups {
		if g.n < 2 {
			continue
		}
		if _, err := s.DB.CollectionByTMDB(ctx, "collection", id); err == nil {
			continue
		}
		c := &db.Collection{Kind: "tmdb", Name: g.ref.Name, PosterPath: g.ref.PosterPath, BackdropPath: g.ref.BackdropPath,
			ShowMissing: true, Rules: db.CollectionRules{Source: "collection", SourceID: id}}
		if len(g.libs) == 1 {
			for l := range g.libs {
				c.LibraryID = l
			}
		}
		if err := s.syncCollection(ctx, c); err != nil {
			continue
		}
		if s.DB.SaveCollection(ctx, c) == nil {
			created++
		}
	}
	writeJSON(w, 200, map[string]int{"created": created})
}

// homeCollections are the collections promoted to the home screen.
func (s *Server) homeCollections(ctx context.Context, r *http.Request) []map[string]any {
	out := []map[string]any{}
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		return out
	}
	s.syncStaleCollections()
	now := time.Now()
	for _, c := range cs {
		if !c.ShowOnHome || !c.ActiveOn(now) || !visibleCollection(r, c) {
			continue
		}
		items, missing, err := s.collectionItems(ctx, r, c, 24)
		if err != nil || len(items) == 0 {
			continue
		}
		c.ItemCount = len(items)
		c.Active = true
		if len(missing) > 12 {
			missing = missing[:12]
		}
		out = append(out, map[string]any{"collection": c, "items": items, "missing": missing})
	}
	return out
}

// handleLibraryRows lists the collections shown as rows on a library's
// page, in the admin's order, each with its first titles.
func (s *Server) handleLibraryRows(w http.ResponseWriter, r *http.Request) {
	ctx, lib := r.Context(), chi.URLParam(r, "id")
	if !principal(r).CanAccess(lib) {
		writeErr(w, 404, "library not found")
		return
	}
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.syncStaleCollections()
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].LibraryOrder < cs[j].LibraryOrder })
	now := time.Now()
	out := []map[string]any{}
	for _, c := range cs {
		if !c.ShowInLibrary || c.LibraryID != lib || !c.ActiveOn(now) || !visibleCollection(r, c) {
			continue
		}
		items, missing, err := s.collectionItems(ctx, r, c, 30)
		if err != nil || len(items)+len(missing) == 0 {
			continue
		}
		c.ItemCount, c.Active = len(items), true
		if len(missing) > 30 {
			missing = missing[:30]
		}
		out = append(out, map[string]any{"collection": c, "items": items, "missing": missing})
	}
	writeJSON(w, 200, out)
}

// handleLibraryRowsOrder sets which collections are rows on a library's
// page and their order: the listed ones, top to bottom; the rest are off.
func (s *Server) handleLibraryRowsOrder(w http.ResponseWriter, r *http.Request) {
	ctx, lib := r.Context(), chi.URLParam(r, "id")
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	cs, err := s.DB.ListCollections(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	pos := map[string]int{}
	for i, id := range req.IDs {
		pos[id] = i + 1
	}
	for _, c := range cs {
		n, on := pos[c.ID]
		if !on && !(c.ShowInLibrary && c.LibraryID == lib) {
			continue
		}
		if on {
			c.LibraryID = lib
		}
		c.ShowInLibrary, c.LibraryOrder = on, n
		if err := s.DB.SaveCollection(ctx, c); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- stats ----------

type nowPlaying struct {
	SessionID  string   `json:"sessionId"`
	UserID     string   `json:"userId"`
	UserName   string   `json:"userName"`
	ItemID     string   `json:"itemId"`
	Title      string   `json:"title"`
	Subtitle   string   `json:"subtitle,omitempty"`
	PosterPath string   `json:"posterPath,omitempty"`
	Backdrop   string   `json:"backdropPath,omitempty"`
	Method     string   `json:"method"`
	Engine     string   `json:"engine"`
	VideoCopy  bool     `json:"videoCopy"`
	AudioCodec string   `json:"audioCodec,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
	Quality    string   `json:"quality"`
	Client     string   `json:"client"`
	Position   float64  `json:"position"`
	Duration   float64  `json:"duration"`
	Started    int64    `json:"started"`
}

func (s *Server) nowPlaying(ctx context.Context) []nowPlaying {
	out := []nowPlaying{}
	for _, sess := range s.Streams.List() {
		np := nowPlaying{SessionID: sess.ID, UserID: sess.UserID, ItemID: sess.ItemID, Method: string(sess.Decision.Method),
			Engine: sess.Decision.Engine, VideoCopy: sess.Decision.VideoCopy, AudioCodec: sess.Decision.AudioCodec,
			Reasons: sess.Decision.Reasons, Started: sess.Started.Unix()}
		if u, err := s.DB.UserByID(ctx, sess.UserID); err == nil {
			np.UserName = firstNonEmpty(u.DisplayName, u.Username, u.Email, u.ID)
		}
		if it, err := s.DB.GetItem(ctx, sess.ItemID); err == nil {
			np.Title, np.PosterPath, np.Backdrop = it.Title, it.PosterPath, it.BackdropPath
			if it.Kind == "episode" {
				np.Subtitle = it.Title
				np.Backdrop = it.StillPath
				if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
					np.Title, np.PosterPath = sh.Title, sh.PosterPath
					if np.Backdrop == "" {
						np.Backdrop = sh.BackdropPath
					}
					np.Subtitle = "S" + strconv.Itoa(it.SeasonNumber) + " · E" + strconv.Itoa(it.EpisodeNumber) + " — " + it.Title
				}
			}
		}
		if f, err := s.DB.GetFile(ctx, sess.FileID); err == nil {
			np.Quality = versionLabel(f)
		}
		np.Position, np.Duration, np.Client = s.DB.PlayBySession(ctx, sess.ID)
		if sess.Info != nil && np.Duration == 0 {
			np.Duration = sess.Info.Duration
		}
		out = append(out, np)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := queryInt(r, "days", 30)
	if days < 1 || days > 365 {
		days = 30
	}
	tz := queryInt(r, "tz", 0)
	// Day buckets start at local midnight `days-1` days ago.
	loc := time.FixedZone("client", tz)
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	since := start.Unix()
	stats, err := s.DB.Stats(ctx, since, days, tz)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	lib, err := s.DB.LibraryStats(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	recent, _ := s.DB.RecentHistory(ctx, since, "", 25)
	writeJSON(w, 200, map[string]any{"days": days, "stats": stats, "library": lib, "nowPlaying": s.nowPlaying(ctx), "recent": recent})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	days := queryInt(r, "days", 90)
	since := time.Now().AddDate(0, 0, -days).Unix()
	rows, err := s.DB.RecentHistory(r.Context(), since, r.URL.Query().Get("user"), min(max(queryInt(r, "limit", 200), 1), 2000))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rows)
}
