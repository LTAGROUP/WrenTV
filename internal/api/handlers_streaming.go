package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/streaming"
)

// ---------- access ----------

// limits are a user's effective permissions: their own, else the server's.
type limits struct {
	CanStream    bool `json:"canStream"`
	CanDownload  bool `json:"canDownload"`
	MaxStreams   int  `json:"maxStreams"`
	MaxDownloads int  `json:"maxDownloads"`
}

func (s *Server) limitsFor(u *db.User) limits {
	d := s.Cfg.Users
	l := limits{CanStream: u.AllowStreaming && s.Streaming.Active(), CanDownload: d.DeviceDownloads,
		MaxStreams: d.MaxStreams, MaxDownloads: d.MaxDownloads}
	if u.AllowDownloads >= 0 {
		l.CanDownload = u.AllowDownloads == 1
	}
	if u.MaxStreams >= 0 {
		l.MaxStreams = u.MaxStreams
	}
	if u.MaxDownloads >= 0 {
		l.MaxDownloads = u.MaxDownloads
	}
	return l
}

// withStreamingAccess adds the streaming libraries to what a user may see
// when streaming is available to them, and hides them otherwise (nothing
// to play from, streaming off, or the user isn't allowed).
func (s *Server) withStreamingAccess(ctx context.Context, p *auth.Principal) *auth.Principal {
	streamIDs, err := s.DB.LibraryIDsBySource(ctx, "stream")
	if err != nil || len(streamIDs) == 0 {
		return p
	}
	cp := *p
	if p.User.AllowStreaming && s.Streaming.Active() {
		if cp.LibraryIDs != nil {
			cp.LibraryIDs = append(slices.Clone(cp.LibraryIDs), streamIDs...)
		}
		return &cp
	}
	if cp.LibraryIDs == nil {
		all, err := s.DB.LibraryIDsBySource(ctx, "files")
		if err != nil {
			return p
		}
		cp.LibraryIDs = all
		return &cp
	}
	cp.LibraryIDs = slices.DeleteFunc(slices.Clone(cp.LibraryIDs), func(id string) bool { return slices.Contains(streamIDs, id) })
	return &cp
}

// ---------- titles ----------

// handleDiscoverSearch searches TMDB for every movie and show, linked to the
// library when the server has it. With streaming, the rest can be played.
func (s *Server) handleDiscoverSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || !s.TMDB.Enabled() {
		writeJSON(w, 200, map[string]any{"streaming": false, "items": []titleView{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ts, err := s.TMDB.SearchMulti(ctx, q)
	if err != nil {
		writeErr(w, 502, "search unavailable: "+err.Error())
		return
	}
	views := make([]titleView, 0, len(ts))
	for _, t := range ts {
		if t.PosterPath != "" {
			views = append(views, viewOf(t, ""))
		}
	}
	lim := s.limitsFor(principal(r).User)
	writeJSON(w, 200, map[string]any{"streaming": lim.CanStream, "items": nonNil(s.linkLibrary(ctx, r, views, false))})
}

// handleAddStreamTitle opens a TMDB movie or show in the streaming library,
// adding it on first use. Clients then show it like any other item.
func (s *Server) handleAddStreamTitle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TMDBID    int    `json:"tmdbId"`
		MediaType string `json:"mediaType"` // "movie" or "show"
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !s.limitsFor(principal(r).User).CanStream {
		writeErr(w, http.StatusForbidden, "streaming isn't available on this server")
		return
	}
	if req.MediaType == "tv" {
		req.MediaType = "show"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	it, err := s.Streaming.AddTitle(ctx, req.MediaType, req.TMDBID)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, it)
}

func (s *Server) isStreamLibrary(ctx context.Context, libraryID string) bool {
	l, err := s.DB.GetLibrary(ctx, libraryID)
	return err == nil && l.Source == "stream"
}

// handleItemStreams lists what's stored for a streamed movie or episode (admin).
func (s *Server) handleItemStreams(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	sts, _ := s.DB.StreamsForItem(r.Context(), it.ID)
	resp := map[string]any{"streams": nonNil(sts)}
	if l, err := s.DB.GetStreamLookup(r.Context(), it.ID); err == nil {
		resp["lookup"] = l
	}
	if dl, err := s.DB.ActiveDownloadFor(r.Context(), it.ID); err == nil {
		resp["download"] = dl
	}
	writeJSON(w, 200, resp)
}

// handleForgetStreams drops stored streams so the next play finds new ones (admin).
func (s *Server) handleForgetStreams(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ids := []string{it.ID}
	if it.Kind == "show" || it.Kind == "season" {
		children, _ := s.DB.ItemsByShow(r.Context(), it.ShowID)
		if it.Kind == "show" {
			children, _ = s.DB.ItemsByShow(r.Context(), it.ID)
		}
		ids = ids[:0]
		for _, c := range children {
			if c.Kind == "episode" && (it.Kind == "show" || c.ParentID == it.ID) {
				ids = append(ids, c.ID)
			}
		}
	}
	for _, id := range ids {
		_ = s.Streaming.Forget(r.Context(), id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- server downloads (admin) ----------

func (s *Server) handleServerDownload(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok {
		writeErr(w, 404, "item not found")
		return
	}
	ctx := r.Context()
	var items []*db.Item
	switch it.Kind {
	case "movie", "episode":
		items = []*db.Item{it}
	case "season":
		items, _, _ = s.DB.QueryItems(ctx, db.ItemQuery{ParentID: it.ID, Kind: "episode", Sort: "episode"})
	case "show":
		all, _ := s.DB.ItemsByShow(ctx, it.ID)
		for _, c := range all {
			if c.Kind == "episode" {
				items = append(items, c)
			}
		}
	}
	queued, firstErr := 0, error(nil)
	for _, x := range items {
		if ready, err := s.Streaming.Ensure(ctx, x, 20*time.Second); !ready {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if _, err := s.Streaming.QueueDownload(ctx, x, principal(r).User.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		queued++
	}
	if queued == 0 {
		if firstErr == nil {
			firstErr = streaming.ErrNoStreams
		}
		writeErr(w, 400, firstErr.Error())
		return
	}
	writeJSON(w, 202, map[string]int{"queued": queued})
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	list, err := s.Streaming.Downloads(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, nonNil(list))
}

func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	if err := s.Streaming.CancelDownload(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 404, "download not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	if err := s.Streaming.RetryDownload(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 404, "download not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- debrid keys and addons (admin) ----------

func (s *Server) handleCheckDebrid(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Key      string `json:"key"` // empty: the saved key
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = map[string]string{"torbox": s.Cfg.Streaming.TorBoxKey, "alldebrid": s.Cfg.Streaming.AllDebridKey,
			"realdebrid": s.Cfg.Streaming.RealDebridKey}[req.Provider]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, 200, streaming.CheckAccount(ctx, req.Provider, key))
}

// handleCheckUsenet tries the news servers and indexers on the settings
// page, with saved secrets filling in any not retyped.
func (s *Server) handleCheckUsenet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Indexers    *[]indexerPatch    `json:"indexers"`
		NewsServers *[]newsServerPatch `json:"newsServers"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ixs, nss, err := usenetFromPatch(&s.Cfg.Streaming, req.Indexers, req.NewsServers)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := streaming.CheckUsenet(ctx, nss, ixs); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCheckAddon(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	a, err := streaming.ParseAddon("", req.URL)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	name, err := streaming.Manifest(ctx, a)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"name": name, "url": a.BaseURL + "/manifest.json"})
}

// ServeInternal serves stream sources to ffmpeg. Loopback only.
func (s *Server) serveInternalStream(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		http.NotFound(w, r)
		return
	}
	s.Streaming.ServeInternal(w, r, chi.URLParam(r, "token"))
}

// ---------- device downloads ----------

// A ticket is a capability URL for saving one file to a device: download
// managers resume with Range requests and can't always send headers.
type ticket struct {
	userID, itemID, fileID, name string
	expires                      time.Time
}

type tickets struct {
	mu     sync.Mutex
	m      map[string]*ticket
	active map[string]int // user id -> downloads in progress
}

func (t *tickets) get(id string) *ticket {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	return t.m[id]
}

// handleDeviceDownload returns a URL to save a movie or episode to the device.
func (s *Server) handleDeviceDownload(w http.ResponseWriter, r *http.Request) {
	it, ok := s.itemFor(r)
	if !ok || (it.Kind != "movie" && it.Kind != "episode") {
		writeErr(w, 404, "movie or episode not found")
		return
	}
	p := principal(r)
	if !s.limitsFor(p.User).CanDownload {
		writeErr(w, http.StatusForbidden, "downloads aren't enabled for your account")
		return
	}
	var req struct {
		FileID string `json:"fileId"`
	}
	_ = decode(r, &req)
	ctx := r.Context()
	files, err := s.playableFiles(ctx, it)
	if err != nil || len(files) == 0 {
		if s.isStreamLibrary(ctx, it.LibraryID) {
			if ready, err := s.Streaming.Ensure(ctx, it, 25*time.Second); !ready {
				if err == nil {
					writeJSON(w, 202, map[string]string{"status": "pending", "message": "Finding a stream…"})
					return
				}
				writeErr(w, http.StatusFailedDependency, err.Error())
				return
			}
			files, _ = s.playableFiles(ctx, it)
		}
		if len(files) == 0 {
			writeErr(w, 404, "nothing to download")
			return
		}
	}
	f := files[0] // best quality first
	for _, x := range files {
		if x.ID == req.FileID {
			f = x
		}
	}
	name := s.downloadName(ctx, it, f)
	s.ensureTickets()
	t := &ticket{userID: p.User.ID, itemID: it.ID, fileID: f.ID, name: name, expires: time.Now().Add(24 * time.Hour)}
	id := db.NewID() + db.NewID()
	s.tickets.mu.Lock()
	s.tickets.m[id] = t
	s.tickets.mu.Unlock()
	writeJSON(w, 200, map[string]any{
		"status": "ready", "url": "/download/" + id + "/" + url.PathEscape(name), "fileName": name, "fileId": f.ID,
		"size": f.Size, "label": versionLabel(f), "expiresAt": t.expires.Unix(),
	})
}

func (s *Server) ensureTickets() {
	s.ticketsOnce.Do(func() { s.tickets = &tickets{m: map[string]*ticket{}, active: map[string]int{}} })
}

// downloadName is a friendly file name: "Title (2024).mkv" or
// "Show - S01E02 - Title.mkv".
func (s *Server) downloadName(ctx context.Context, it *db.Item, f *db.MediaFile) string {
	ext := filepath.Ext(f.Path)
	if f.StreamID != "" {
		if st, err := s.DB.GetStream(ctx, f.StreamID); err == nil {
			ext = filepath.Ext(st.FileName)
		}
	}
	if ext == "" || len(ext) > 6 {
		ext = ".mkv"
	}
	clean := strings.NewReplacer("/", "-", `\`, "-", ":", " -", "?", "", "*", "", `"`, "", "<", "", ">", "", "|", "-").Replace
	name := it.Title
	if it.Year > 0 && it.Kind == "movie" {
		name = fmt.Sprintf("%s (%d)", it.Title, it.Year)
	}
	if it.Kind == "episode" {
		show := ""
		if sh, err := s.DB.GetItem(ctx, it.ShowID); err == nil {
			show = sh.Title + " - "
		}
		name = fmt.Sprintf("%sS%02dE%02d - %s", show, it.SeasonNumber, it.EpisodeNumber, it.Title)
	}
	return strings.TrimSpace(clean(name)) + ext
}

// serveDeviceDownload streams the file with Range support, within the
// user's limit on simultaneous downloads.
func (s *Server) serveDeviceDownload(w http.ResponseWriter, r *http.Request) {
	s.ensureTickets()
	t := s.tickets.get(chi.URLParam(r, "ticket"))
	if t == nil {
		http.Error(w, "this download link has expired", http.StatusGone)
		return
	}
	ctx := r.Context()
	u, err := s.DB.UserByID(ctx, t.userID)
	if err != nil {
		http.Error(w, "account not found", http.StatusGone)
		return
	}
	lim := s.limitsFor(u)
	if !lim.CanDownload {
		http.Error(w, "downloads aren't enabled for your account", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodHead {
		s.tickets.mu.Lock()
		if lim.MaxDownloads > 0 && s.tickets.active[t.userID] >= lim.MaxDownloads {
			s.tickets.mu.Unlock()
			w.Header().Set("Retry-After", "30")
			writeErr(w, http.StatusTooManyRequests, fmt.Sprintf("You can download %d at a time. This one starts when another finishes.", lim.MaxDownloads))
			return
		}
		s.tickets.active[t.userID]++
		s.tickets.mu.Unlock()
		defer func() {
			s.tickets.mu.Lock()
			s.tickets.active[t.userID]--
			s.tickets.mu.Unlock()
		}()
	}
	f, err := s.DB.GetFile(ctx, t.fileID)
	if err != nil {
		http.Error(w, "file no longer available", http.StatusGone)
		return
	}
	src, err := s.openSource(ctx, f)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, streaming.ErrBusy) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), status)
		return
	}
	defer src.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=UTF-8''%s`, url.PathEscape(t.name)))
	src.ServeHTTP(w, r)
}
