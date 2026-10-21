package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
)

// ---------- layouts ----------

// layoutTarget is whose layout a config request is about: the caller's own
// ("me", the default) or everyone's default ("default", admins only).
func layoutTarget(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	switch r.URL.Query().Get("target") {
	case "", "me":
		return principal(r).User.ID, true
	case "default":
		if !principal(r).IsAdmin() {
			writeErr(w, 403, "admin only")
			return "", false
		}
		return "", true
	}
	writeErr(w, 400, "target must be me or default")
	return "", false
}

func (s *Server) pageScope(w http.ResponseWriter, r *http.Request) (*pageScope, bool) {
	sc, err := s.scopeFor(r, chi.URLParam(r, "scope"))
	if errors.Is(err, errNotFound) {
		writeErr(w, 404, "page not found")
		return nil, false
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, false
	}
	return sc, true
}

// handleLayout is a page's rows for the caller.
func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.pageScope(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	entries, custom := s.effectiveLayout(ctx, r, sc)
	writeJSON(w, 200, map[string]any{"rows": s.resolveLayout(ctx, r, sc, entries), "custom": custom})
}

// layoutOption is a row the editor can add, or one already on the page.
type layoutOption struct {
	db.LayoutEntry
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// handleLayoutConfig is a page's layout for the editor: the saved (or
// inherited) entries and every row that could go on the page.
func (s *Server) handleLayoutConfig(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.pageScope(w, r)
	if !ok {
		return
	}
	uid, ok := layoutTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	_, hasDefault, _ := s.DB.Layout(ctx, sc.key, "")
	entries, custom, err := s.DB.Layout(ctx, sc.key, uid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !custom {
		// A user starts from the default; the default starts from the built-in.
		if uid != "" && hasDefault {
			entries, _, _ = s.DB.Layout(ctx, sc.key, "")
		} else {
			entries = s.builtinLayout(r, sc)
		}
	}
	entries = withNewCollections(entries, s.scopeCollections(ctx, r, sc))
	options := s.layoutOptions(ctx, r, sc)
	byKey := map[string]layoutOption{}
	for _, o := range options {
		byKey[o.Type+"|"+o.Ref] = o
	}
	out := []layoutOption{}
	for _, e := range entries {
		o, ok := byKey[e.Type+"|"+e.Ref]
		if !ok {
			continue // gone: a deleted hub or collection
		}
		o.Hidden = e.Hidden
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"entries": out, "custom": custom, "hasDefault": hasDefault, "options": options})
}

func (s *Server) layoutOptions(ctx context.Context, r *http.Request, sc *pageScope) []layoutOption {
	out := []layoutOption{}
	for _, sh := range builtinShelves {
		if s.shelfFits(sc, sh) {
			out = append(out, layoutOption{LayoutEntry: db.LayoutEntry{Type: "shelf", Ref: sh.ref}, Title: sh.title, Detail: "Shelf"})
		}
	}
	out = append(out, layoutOption{LayoutEntry: db.LayoutEntry{Type: "category", Ref: genresRef(sc)}, Title: "Genres", Detail: "One hub for each genre of this page's titles"})
	if cats, err := s.DB.ListHubCategories(ctx); err == nil {
		for _, c := range cats {
			out = append(out, layoutOption{LayoutEntry: db.LayoutEntry{Type: "category", Ref: c.ID}, Title: c.Name, Detail: fmt.Sprintf("Hub category: %d hubs", len(c.HubIDs))})
		}
	}
	if hubs, err := s.DB.ListHubs(ctx); err == nil {
		for _, h := range hubs {
			if s.hubAvailable(h) {
				out = append(out, layoutOption{LayoutEntry: db.LayoutEntry{Type: "hub", Ref: h.ID}, Title: h.Name, Detail: "Hub"})
			}
		}
	}
	if cs, err := s.DB.ListCollections(ctx); err == nil {
		for _, c := range cs {
			if visibleCollection(r, c) && (sc.home() || c.LibraryID == "" || containsStr(sc.ids, c.LibraryID)) {
				out = append(out, layoutOption{LayoutEntry: db.LayoutEntry{Type: "collection", Ref: c.ID}, Title: c.Name, Detail: "Collection"})
			}
		}
	}
	return out
}

var layoutTypes = map[string]bool{"shelf": true, "hub": true, "category": true, "collection": true}

func (s *Server) handleSetLayout(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.pageScope(w, r)
	if !ok {
		return
	}
	uid, ok := layoutTarget(w, r)
	if !ok {
		return
	}
	var req struct {
		Entries []db.LayoutEntry `json:"entries"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.Entries) > 100 {
		writeErr(w, 400, "too many rows")
		return
	}
	seen := map[string]bool{}
	entries := []db.LayoutEntry{}
	for _, e := range req.Entries {
		e.Ref = strings.TrimSpace(e.Ref)
		if !layoutTypes[e.Type] || e.Ref == "" {
			writeErr(w, 400, "unknown row "+e.Type+":"+e.Ref)
			return
		}
		if k := e.Type + "|" + e.Ref; !seen[k] {
			seen[k] = true
			entries = append(entries, e)
		}
	}
	if err := s.DB.SetLayout(r.Context(), sc.key, uid, entries); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetLayout(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.pageScope(w, r)
	if !ok {
		return
	}
	uid, ok := layoutTarget(w, r)
	if !ok {
		return
	}
	if err := s.DB.ResetLayout(r.Context(), sc.key, uid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- hubs ----------

func (s *Server) handleHubs(w http.ResponseWriter, r *http.Request) {
	hubs, err := s.DB.ListHubs(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	cats, err := s.DB.ListHubCategories(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"hubs": nonNil(hubs), "categories": nonNil(cats)})
}

// handleHub is a hub's page: its card and resolved shelves.
func (s *Server) handleHub(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	id, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 400, "bad hub id")
		return
	}
	h, err := s.hubFor(ctx, r, id)
	if err != nil {
		writeErr(w, 404, "hub not found")
		return
	}
	rows := s.resolveHub(ctx, r, h)
	writeJSON(w, 200, map[string]any{"hub": s.cardOf(ctx, r, h), "rows": rows, "shelves": h.Shelves})
}

var hubKinds = map[string]bool{"genre": true, "service": true, "custom": true}
var shelfSources = map[string]bool{"discover": true, "chart": true, "library": true, "collection": true}

func validHub(h *db.Hub) string {
	h.Name = strings.TrimSpace(h.Name)
	if h.Name == "" {
		return "name required"
	}
	if h.Kind == "" {
		h.Kind = "custom"
	}
	if !hubKinds[h.Kind] {
		return "unknown kind " + h.Kind
	}
	for i := range h.Shelves {
		sh := &h.Shelves[i]
		if !shelfSources[sh.Source] {
			return "unknown shelf source " + sh.Source
		}
		if sh.ID == "" {
			sh.ID = db.NewID()
		}
	}
	return ""
}

func (s *Server) handleCreateHub(w http.ResponseWriter, r *http.Request) {
	var h db.Hub
	if err := decode(r, &h); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	h.ID = ""
	if msg := validHub(&h); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.DB.SaveHub(r.Context(), &h); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, h)
}

func (s *Server) handleUpdateHub(w http.ResponseWriter, r *http.Request) {
	h, err := s.DB.GetHub(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "hub not found")
		return
	}
	id, created := h.ID, h.CreatedAt
	if err := decode(r, h); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	h.ID, h.CreatedAt = id, created
	if msg := validHub(h); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.DB.SaveHub(r.Context(), h); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, h)
}

func (s *Server) handleDeleteHub(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteHub(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validCategory(c *db.HubCategory) string {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return "name required"
	}
	if c.Style == "" {
		c.Style = "tiles"
	}
	if c.Style != "tiles" && c.Style != "logos" {
		return "style must be tiles or logos"
	}
	if c.HubIDs == nil {
		c.HubIDs = []string{}
	}
	return ""
}

func (s *Server) handleCreateHubCategory(w http.ResponseWriter, r *http.Request) {
	var c db.HubCategory
	if err := decode(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c.ID = ""
	if msg := validCategory(&c); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.DB.SaveHubCategory(r.Context(), &c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) handleUpdateHubCategory(w http.ResponseWriter, r *http.Request) {
	c, err := s.DB.GetHubCategory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "category not found")
		return
	}
	id, created := c.ID, c.CreatedAt
	if err := decode(r, c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	c.ID, c.CreatedAt = id, created
	if msg := validCategory(c); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	if err := s.DB.SaveHubCategory(r.Context(), c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) handleDeleteHubCategory(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteHubCategory(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWatchProviders lists TMDB's streaming services, for building a service hub.
func (s *Server) handleWatchProviders(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Enabled() {
		writeJSON(w, 200, []any{})
		return
	}
	ps, err := s.TMDB.WatchProviders(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(ps))
}
