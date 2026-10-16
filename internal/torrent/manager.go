// Package torrent makes torrents playable like regular library files.
//
// A torrent is added by the user (magnet or .torrent), resolved through the
// configured providers in order (debrid first if configured, then the
// built-in engine), and its video files become ordinary library items.
// Streaming is on demand: only the pieces the player needs are fetched, in
// order. The built-in engine never uploads (no seeding).
//
// If a torrent can't be played (no metadata, not cached, stalls during
// playback) Wren TV fires a "torrent.unplayable" webhook so a backend service
// can find another release and call the replace endpoint. Clients see a
// "finding a better source" state instead of an error and resume
// automatically once the replacement is ready.
package torrent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	alog "github.com/anacrolix/log"
	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

const (
	StatusResolving  = "resolving"
	StatusReady      = "ready"
	StatusUnplayable = "unplayable" // waiting for a replacement
	StatusReplacing  = "replacing"
	StatusFailed     = "failed"
)

var ErrPending = errors.New("torrent source not ready")

type Manager struct {
	cfg      func() config.TorrentConfig
	db       *db.DB
	scanner  *library.Scanner
	hooks    *webhook.Sender
	sessions *stream.Manager
	server   func() webhook.Server
	httpPort int

	client *atorrent.Client
	debrid Debrid

	mu       sync.Mutex
	active   map[string]*atorrent.Torrent // torrent row id -> engine torrent
	tokens   map[string]*fileSource
	watching map[*watch]struct{}
	lastRead map[string]int64 // torrent id -> bytes read at last stall check
	timers   map[string]*time.Timer
}

type watch struct {
	torrentID string
	since     time.Time
}

type Deps struct {
	Config   func() config.TorrentConfig
	DB       *db.DB
	Scanner  *library.Scanner
	Hooks    *webhook.Sender
	Sessions *stream.Manager
	Server   func() webhook.Server
	HTTPPort int
}

func NewManager(d Deps) (*Manager, error) {
	m := &Manager{
		cfg: d.Config, db: d.DB, scanner: d.Scanner, hooks: d.Hooks, sessions: d.Sessions, server: d.Server, httpPort: d.HTTPPort,
		active: map[string]*atorrent.Torrent{}, tokens: map[string]*fileSource{}, watching: map[*watch]struct{}{},
		lastRead: map[string]int64{}, timers: map[string]*time.Timer{},
	}
	cfg := d.Config()
	if !cfg.Enabled {
		return m, nil
	}
	if slices.Contains(cfg.Providers, "builtin") {
		if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
			return nil, err
		}
		cc := atorrent.NewDefaultClientConfig()
		cc.DataDir = cfg.DownloadDir
		cc.NoUpload = true // never upload: Wren TV does not seed
		cc.Seed = false
		cc.DisableAggressiveUpload = true
		cc.ListenPort = cfg.ListenPort
		// No UPnP / NAT-PMP on the router unless the owner opts in. DHT,
		// trackers and the listen port itself are unchanged.
		cc.NoDefaultPortForwarding = !cfg.PortForwarding
		// Read errors surface through our own stall detection; anacrolix logs
		// every canceled player range request at Error, so keep only Critical.
		cc.Logger = cc.Logger.WithFilterLevel(alog.Critical)
		cl, err := atorrent.NewClient(cc)
		if err != nil {
			// Port in use: fall back to an ephemeral port rather than failing.
			cc.ListenPort = 0
			if cl, err = atorrent.NewClient(cc); err != nil {
				return nil, fmt.Errorf("torrent engine: %w", err)
			}
		}
		m.client = cl
	}
	if slices.Contains(cfg.Providers, "debrid") && cfg.Debrid.APIKey != "" {
		switch cfg.Debrid.Service {
		case "realdebrid", "":
			m.debrid = NewRealDebrid(cfg.Debrid.APIKey)
		default:
			slog.Warn("unknown debrid service", "service", cfg.Debrid.Service)
		}
	}
	return m, nil
}

func (m *Manager) Enabled() bool { return m.client != nil || m.debrid != nil }

func (m *Manager) Providers() []string {
	var p []string
	for _, name := range m.cfg().Providers {
		if (name == "builtin" && m.client != nil) || (name == "debrid" && m.debrid != nil) {
			p = append(p, name)
		}
	}
	return p
}

// Start restores torrents from the database and runs the stall detector.
func (m *Manager) Start(ctx context.Context) {
	if !m.Enabled() {
		return
	}
	ts, err := m.db.ListTorrents(ctx)
	if err == nil {
		for _, t := range ts {
			switch t.Status {
			case StatusReady:
				if t.Provider == "builtin" {
					go m.restore(ctx, t)
				}
			case StatusResolving, StatusReplacing:
				go m.resolve(context.Background(), t, nil)
			case StatusUnplayable:
				m.armReplaceTimer(t.ID)
			}
		}
	}
	go m.stallLoop(ctx)
}

func (m *Manager) Close() {
	if m.client != nil {
		m.client.Close()
	}
}

// ---------- add / replace / delete ----------

type AddRequest struct {
	LibraryID   string
	Magnet      string
	TorrentFile []byte
	AddedBy     string
	ExternalRef string // opaque id from a backend service, echoed in webhooks
}

func (m *Manager) Add(ctx context.Context, req AddRequest) (*db.Torrent, error) {
	if !m.Enabled() {
		return nil, errors.New("torrent playback is disabled on this server")
	}
	lib, err := m.db.GetLibrary(ctx, req.LibraryID)
	if err != nil {
		return nil, fmt.Errorf("library: %w", err)
	}
	magnet, hash, mi, err := parseInput(req.Magnet, req.TorrentFile)
	if err != nil {
		return nil, err
	}
	existing, _ := m.db.ListTorrents(ctx)
	for _, t := range existing {
		if strings.EqualFold(t.InfoHash, hash) && t.LibraryID == lib.ID && t.Status != StatusFailed {
			return t, nil
		}
	}
	t := &db.Torrent{LibraryID: lib.ID, Magnet: magnet, InfoHash: hash, Name: displayName(magnet), Status: StatusResolving,
		AddedBy: req.AddedBy, ExternalRef: req.ExternalRef}
	if err := m.db.InsertTorrent(ctx, t); err != nil {
		return nil, err
	}
	if mi != nil {
		m.saveMetainfo(hash, mi)
	}
	m.publish(t)
	go m.resolve(context.Background(), t, nil)
	return t, nil
}

// Replace swaps in a new release for an existing torrent, keeping the same
// library items (and therefore watch progress). Called by backend services.
func (m *Manager) Replace(ctx context.Context, id, newMagnet string, torrentFile []byte) (*db.Torrent, error) {
	t, err := m.db.GetTorrent(ctx, id)
	if err != nil {
		return nil, err
	}
	magnet, hash, mi, err := parseInput(newMagnet, torrentFile)
	if err != nil {
		return nil, err
	}
	// Anchor items: what the old files belonged to.
	oldFiles, _ := m.db.FilesForTorrent(ctx, t.ID)
	anchor := m.anchorFor(ctx, oldFiles)

	m.stopTimer(t.ID)
	m.dropEngine(t)
	t.Magnet, t.InfoHash, t.Name = magnet, hash, displayName(magnet)
	t.Status, t.Error, t.Provider, t.ProviderRef = StatusReplacing, "", "", ""
	if err := m.db.UpdateTorrent(ctx, t); err != nil {
		return nil, err
	}
	if mi != nil {
		m.saveMetainfo(hash, mi)
	}
	m.publish(t)
	go m.resolve(context.Background(), t, &replaceState{oldFiles: oldFiles, anchor: anchor})
	return t, nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	t, err := m.db.GetTorrent(ctx, id)
	if err != nil {
		return err
	}
	files, _ := m.db.FilesForTorrent(ctx, id)
	for _, f := range files {
		m.sessions.StopForFile(f.ID)
	}
	m.stopTimer(id)
	m.dropEngine(t)
	if t.Provider == "builtin" && m.client != nil {
		// Remove downloaded data; it was only a streaming cache.
		if t.Name != "" && !strings.ContainsAny(t.Name, `/\`) && t.Name != "." && t.Name != ".." {
			_ = os.RemoveAll(filepath.Join(m.cfg().DownloadDir, t.Name))
		}
		_ = os.Remove(m.metainfoPath(t.InfoHash))
	}
	if err := m.db.DeleteTorrent(ctx, id); err != nil {
		return err
	}
	_ = m.db.PruneEmpty(ctx, t.LibraryID)
	m.scanner.Hub().Publish(library.Event{Type: "library.updated", LibraryID: t.LibraryID})
	return nil
}

// ---------- resolution ----------

type replaceState struct {
	oldFiles []*db.MediaFile
	anchor   *db.Item
}

type resolvedFile struct {
	path  string // path within the torrent (for name parsing)
	size  int64
	index int // engine file index or debrid link index
}

func (m *Manager) resolve(ctx context.Context, t *db.Torrent, rs *replaceState) {
	cfg := m.cfg()
	var lastErr error
	for _, p := range m.Providers() {
		var files []resolvedFile
		var name string
		var err error
		switch p {
		case "debrid":
			files, name, err = m.resolveDebrid(ctx, t)
		case "builtin":
			files, name, err = m.resolveBuiltin(ctx, t, time.Duration(cfg.Health.MetadataTimeoutSec)*time.Second)
		}
		if err != nil {
			slog.Info("torrent provider failed", "torrent", t.Name, "provider", p, "err", err)
			lastErr = err
			continue
		}
		if name != "" {
			t.Name = name
		}
		if err := m.ingest(ctx, t, files, rs); err != nil {
			lastErr = err
			m.dropEngine(t)
			continue
		}
		// Persist the provider first: verify opens sources via the DB row.
		// Status stays resolving/replacing, so players still get "pending".
		_ = m.db.UpdateTorrent(ctx, t)
		// Prove it actually streams before calling it ready.
		if err := m.verify(ctx, t); err != nil {
			slog.Info("torrent failed playback check", "torrent", t.Name, "provider", p, "err", err)
			lastErr = err
			m.dropEngine(t)
			continue
		}
		t.Status, t.Error = StatusReady, ""
		_ = m.db.UpdateTorrent(ctx, t)
		m.publish(t)
		m.scanner.Kick()
		m.scanner.Hub().Publish(library.Event{Type: "library.updated", LibraryID: t.LibraryID})
		slog.Info("torrent ready", "torrent", t.Name, "provider", p)
		return
	}
	if lastErr == nil {
		lastErr = errors.New("no torrent providers available")
	}
	m.markUnplayable(t.ID, lastErr.Error())
}

func (m *Manager) resolveDebrid(ctx context.Context, t *db.Torrent) ([]resolvedFile, string, error) {
	dt, err := m.debrid.Resolve(ctx, t.Magnet, 0)
	if err != nil {
		return nil, "", err
	}
	t.Provider, t.ProviderRef = "debrid", dt.Ref
	var out []resolvedFile
	for _, f := range dt.Files {
		p := f.Path
		if dt.Name != "" && !strings.HasPrefix(p, dt.Name) {
			p = dt.Name + "/" + p
		}
		out = append(out, resolvedFile{path: p, size: f.Size, index: f.Index})
	}
	return out, dt.Name, nil
}

func (m *Manager) resolveBuiltin(ctx context.Context, t *db.Torrent, timeout time.Duration) ([]resolvedFile, string, error) {
	at, err := m.addToEngine(t)
	if err != nil {
		return nil, "", err
	}
	select {
	case <-at.GotInfo():
	case <-time.After(timeout):
		m.dropEngine(t)
		return nil, "", fmt.Errorf("no metadata from peers within %s", timeout)
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	mi := at.Metainfo()
	m.saveMetainfo(t.InfoHash, &mi)
	t.Provider, t.ProviderRef = "builtin", ""
	var out []resolvedFile
	for i, f := range at.Files() {
		if library.IsVideo(f.Path()) && !library.IsIgnored(f.Path()) {
			p := f.Path()
			if len(at.Files()) == 1 {
				p = f.DisplayPath()
			} else if !strings.HasPrefix(p, at.Name()+"/") {
				p = at.Name() + "/" + f.DisplayPath()
			}
			out = append(out, resolvedFile{path: p, size: f.Length(), index: i})
		}
	}
	if len(out) == 0 {
		m.dropEngine(t)
		return nil, "", errors.New("torrent contains no video files")
	}
	return out, at.Name(), nil
}

func (m *Manager) addToEngine(t *db.Torrent) (*atorrent.Torrent, error) {
	m.mu.Lock()
	if at := m.active[t.ID]; at != nil {
		m.mu.Unlock()
		return at, nil
	}
	m.mu.Unlock()
	var at *atorrent.Torrent
	var err error
	if mi, lerr := metainfo.LoadFromFile(m.metainfoPath(t.InfoHash)); lerr == nil {
		at, err = m.client.AddTorrent(mi)
	} else {
		at, err = m.client.AddMagnet(t.Magnet)
	}
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.active[t.ID] = at
	m.mu.Unlock()
	return at, nil
}

func (m *Manager) restore(ctx context.Context, t *db.Torrent) {
	if m.client == nil {
		return
	}
	at, err := m.addToEngine(t)
	if err != nil {
		slog.Warn("restore torrent failed", "torrent", t.Name, "err", err)
		return
	}
	select {
	case <-at.GotInfo():
	case <-time.After(5 * time.Minute):
	case <-ctx.Done():
	}
}

func (m *Manager) dropEngine(t *db.Torrent) {
	m.mu.Lock()
	at := m.active[t.ID]
	delete(m.active, t.ID)
	m.mu.Unlock()
	if at != nil {
		at.Drop()
	}
	if t.Provider == "debrid" && t.ProviderRef != "" && m.debrid != nil {
		_ = m.debrid.Delete(context.Background(), t.ProviderRef)
	}
}

// ingest maps torrent files onto library items.
func (m *Manager) ingest(ctx context.Context, t *db.Torrent, files []resolvedFile, rs *replaceState) error {
	lib, err := m.db.GetLibrary(ctx, t.LibraryID)
	if err != nil {
		return err
	}
	// Movies: only the main feature (largest file).
	if lib.Type == "movies" && len(files) > 1 {
		sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })
		files = files[:1]
	}
	var anchor *db.Item
	if rs != nil {
		anchor = rs.anchor
	}
	keep := map[string]bool{}
	added := 0
	for _, f := range files {
		store := fmt.Sprintf("torrent://%s/%d/%s", t.ID, f.index, f.path)
		keep[store] = true
		if ex, err := m.db.FileByPath(ctx, store); err == nil && ex != nil {
			added++
			continue
		}
		if _, err := m.scanner.IngestAs(ctx, lib, "", f.path, store, f.size, time.Now().Unix(), t.ID, f.index, anchor); err != nil {
			slog.Debug("torrent file not ingested", "path", f.path, "err", err)
			continue
		}
		added++
	}
	if added == 0 {
		return errors.New("no files could be matched to library items")
	}
	// Retire files from the previous release (after the new ones are in place,
	// so items never disappear).
	old, _ := m.db.FilesForTorrent(ctx, t.ID)
	for _, f := range old {
		if !keep[f.Path] {
			m.sessions.StopForFile(f.ID)
			_ = m.db.DeleteFile(ctx, f.ID)
		}
	}
	_ = m.db.PruneEmpty(ctx, t.LibraryID)
	return nil
}

func (m *Manager) anchorFor(ctx context.Context, files []*db.MediaFile) *db.Item {
	for _, f := range files {
		it, err := m.db.GetItem(ctx, f.ItemID)
		if err != nil {
			continue
		}
		if it.Kind == "movie" {
			return it
		}
		if it.ShowID != "" {
			if sh, err := m.db.GetItem(ctx, it.ShowID); err == nil {
				return sh
			}
		}
	}
	return nil
}

// verify probes the main file through the provider; this both proves the
// source streams and records media info for playback decisions.
func (m *Manager) verify(ctx context.Context, t *db.Torrent) error {
	files, err := m.db.FilesForTorrent(ctx, t.ID)
	if err != nil || len(files) == 0 {
		return errors.New("no files")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })
	timeout := time.Duration(max(m.cfg().Health.StallTimeoutSec, 15)) * time.Second * 2
	vctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for i, f := range files {
		if i >= 3 && f.ProbedAt > 0 {
			break
		}
		src, err := m.SourceFor(vctx, f)
		if err != nil {
			return err
		}
		err = m.scanner.ProbeFile(vctx, f, src.Input())
		src.Close()
		if err != nil {
			if i == 0 {
				return fmt.Errorf("could not read media: %w", err)
			}
			continue
		}
	}
	return nil
}

// ---------- playback ----------

// SourceFor returns a stream source for a torrent-backed file, or ErrPending
// if the torrent is between sources.
func (m *Manager) SourceFor(ctx context.Context, f *db.MediaFile) (stream.Source, error) {
	t, err := m.db.GetTorrent(ctx, f.TorrentID)
	if err != nil {
		return nil, err
	}
	switch t.Provider {
	case "debrid":
		if m.debrid == nil {
			return nil, errors.New("debrid provider not configured")
		}
		url, err := m.debrid.Link(ctx, t.ProviderRef, f.TorrentFileIndex)
		if err != nil {
			return nil, err
		}
		return newHTTPSource(ctx, url, f.Size)
	case "builtin":
		if m.client == nil {
			return nil, errors.New("built-in torrent engine disabled")
		}
		at, err := m.addToEngine(t)
		if err != nil {
			return nil, err
		}
		select {
		case <-at.GotInfo():
		case <-ctx.Done():
			return nil, ErrPending
		}
		fs := at.Files()
		if f.TorrentFileIndex < 0 || f.TorrentFileIndex >= len(fs) {
			return nil, errors.New("torrent file index out of range")
		}
		src := &fileSource{m: m, torrentID: t.ID, f: fs[f.TorrentFileIndex], token: randToken()}
		m.mu.Lock()
		m.tokens[src.token] = src
		m.mu.Unlock()
		return src, nil
	}
	return nil, ErrPending
}

// Status reports whether a torrent-backed file can play now.
func (m *Manager) Status(ctx context.Context, torrentID string) (*db.Torrent, error) {
	return m.db.GetTorrent(ctx, torrentID)
}

// ServeInternal feeds ffmpeg (loopback only) for built-in torrents.
func (m *Manager) ServeInternal(w http.ResponseWriter, r *http.Request, token string) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	src := m.tokens[token]
	m.mu.Unlock()
	if src == nil {
		http.NotFound(w, r)
		return
	}
	src.ServeHTTP(w, r)
}

// ---------- health ----------

func (m *Manager) watch(torrentID string) *watch {
	w := &watch{torrentID: torrentID, since: time.Now()}
	m.mu.Lock()
	m.watching[w] = struct{}{}
	m.mu.Unlock()
	return w
}

func (m *Manager) unwatch(w *watch) {
	m.mu.Lock()
	delete(m.watching, w)
	m.mu.Unlock()
}

// stallLoop flags torrents whose readers have been blocked with no data
// arriving for longer than the stall timeout.
func (m *Manager) stallLoop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		timeout := time.Duration(m.cfg().Health.StallTimeoutSec) * time.Second
		m.mu.Lock()
		stalled := map[string]bool{}
		for w := range m.watching {
			if time.Since(w.since) > timeout {
				stalled[w.torrentID] = true
			}
		}
		progress := map[string]int64{}
		for id, at := range m.active {
			st := at.Stats()
			progress[id] = st.BytesReadData.Int64()
		}
		for id := range stalled {
			if progress[id] > m.lastRead[id] {
				delete(stalled, id) // data is flowing, just slowly
			}
		}
		for id, n := range progress {
			m.lastRead[id] = n
		}
		m.mu.Unlock()
		for id := range stalled {
			t, err := m.db.GetTorrent(ctx, id)
			if err != nil || t.Status != StatusReady {
				continue
			}
			peers := 0
			m.mu.Lock()
			if at := m.active[id]; at != nil {
				peers = at.Stats().ActivePeers
			}
			m.mu.Unlock()
			m.markUnplayable(id, fmt.Sprintf("stalled during playback (%d peers)", peers))
		}
	}
}

func (m *Manager) markUnplayable(id, reason string) {
	ctx := context.Background()
	t, err := m.db.GetTorrent(ctx, id)
	if err != nil {
		return
	}
	t.Status, t.Error = StatusUnplayable, reason
	_ = m.db.UpdateTorrent(ctx, t)
	files, _ := m.db.FilesForTorrent(ctx, id)
	for _, f := range files {
		m.sessions.StopForFile(f.ID)
	}
	m.dropEngine(t)
	m.publish(t)
	slog.Warn("torrent unplayable, requesting replacement", "torrent", t.Name, "reason", reason)
	m.hooks.Send(webhook.Envelope{Event: "torrent.unplayable", ID: randToken(), Server: m.server(), Data: m.payload(ctx, t, files)})
	m.armReplaceTimer(id)
}

func (m *Manager) armReplaceTimer(id string) {
	d := time.Duration(max(m.cfg().Health.ReplaceTimeoutMin, 1)) * time.Minute
	m.mu.Lock()
	if old := m.timers[id]; old != nil {
		old.Stop()
	}
	m.timers[id] = time.AfterFunc(d, func() {
		ctx := context.Background()
		t, err := m.db.GetTorrent(ctx, id)
		if err != nil || t.Status != StatusUnplayable {
			return
		}
		t.Status = StatusFailed
		_ = m.db.UpdateTorrent(ctx, t)
		m.publish(t)
		files, _ := m.db.FilesForTorrent(ctx, id)
		m.hooks.Send(webhook.Envelope{Event: "torrent.failed", ID: randToken(), Server: m.server(), Data: m.payload(ctx, t, files)})
	})
	m.mu.Unlock()
}

func (m *Manager) stopTimer(id string) {
	m.mu.Lock()
	if t := m.timers[id]; t != nil {
		t.Stop()
		delete(m.timers, id)
	}
	m.mu.Unlock()
}

// Payload sent with torrent webhooks: everything a backend needs to find a
// replacement release and call back.
type Payload struct {
	Torrent    TorrentInfo `json:"torrent"`
	Items      []ItemInfo  `json:"items"`
	ReplaceURL string      `json:"replaceUrl"`
}

type TorrentInfo struct {
	ID          string `json:"id"`
	InfoHash    string `json:"infoHash"`
	Name        string `json:"name"`
	Magnet      string `json:"magnet"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	ExternalRef string `json:"externalRef,omitempty"`
	LibraryID   string `json:"libraryId"`
}

type ItemInfo struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Year       int    `json:"year,omitempty"`
	TMDBID     int    `json:"tmdbId,omitempty"`
	IMDBID     string `json:"imdbId,omitempty"`
	Season     int    `json:"season,omitempty"`
	Episode    int    `json:"episode,omitempty"`
	ShowTitle  string `json:"showTitle,omitempty"`
	ShowTMDBID int    `json:"showTmdbId,omitempty"`
	ShowIMDBID string `json:"showImdbId,omitempty"`
}

func (m *Manager) payload(ctx context.Context, t *db.Torrent, files []*db.MediaFile) Payload {
	p := Payload{
		Torrent: TorrentInfo{ID: t.ID, InfoHash: t.InfoHash, Name: t.Name, Magnet: t.Magnet, Provider: t.Provider, Status: t.Status,
			Reason: t.Error, ExternalRef: t.ExternalRef, LibraryID: t.LibraryID},
		Items: []ItemInfo{},
	}
	if base := m.server().URL; base != "" {
		p.ReplaceURL = strings.TrimSuffix(base, "/") + "/api/integration/torrents/" + t.ID + "/replace"
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.ItemID] {
			continue
		}
		seen[f.ItemID] = true
		it, err := m.db.GetItem(ctx, f.ItemID)
		if err != nil {
			continue
		}
		ii := ItemInfo{ID: it.ID, Kind: it.Kind, Title: it.Title, Year: it.Year, TMDBID: it.TMDBID, IMDBID: it.IMDBID}
		if it.Kind == "episode" {
			ii.Season, ii.Episode = it.SeasonNumber, it.EpisodeNumber
			if sh, err := m.db.GetItem(ctx, it.ShowID); err == nil {
				ii.ShowTitle, ii.ShowTMDBID, ii.ShowIMDBID = sh.Title, sh.TMDBID, sh.IMDBID
			}
		}
		p.Items = append(p.Items, ii)
	}
	return p
}

// ---------- helpers ----------

func (m *Manager) publish(t *db.Torrent) {
	m.scanner.Hub().Publish(library.Event{Type: "torrent.updated", TorrentID: t.ID, LibraryID: t.LibraryID, Message: t.Status})
}

func (m *Manager) metainfoPath(hash string) string {
	return filepath.Join(m.cfg().DownloadDir, ".meta", strings.ToLower(hash)+".torrent")
}

func (m *Manager) saveMetainfo(hash string, mi *metainfo.MetaInfo) {
	p := m.metainfoPath(hash)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	var b bytes.Buffer
	if err := mi.Write(&b); err == nil {
		_ = os.WriteFile(p, b.Bytes(), 0o644)
	}
}

func parseInput(magnet string, torrentFile []byte) (string, string, *metainfo.MetaInfo, error) {
	if len(torrentFile) > 0 {
		mi, err := metainfo.Load(bytes.NewReader(torrentFile))
		if err != nil {
			return "", "", nil, fmt.Errorf("invalid .torrent file: %w", err)
		}
		info, err := mi.UnmarshalInfo()
		if err != nil {
			return "", "", nil, fmt.Errorf("invalid .torrent file: %w", err)
		}
		h := mi.HashInfoBytes()
		return mi.Magnet(&h, &info).String(), h.HexString(), mi, nil
	}
	magnet = strings.TrimSpace(magnet)
	mg, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid magnet link: %w", err)
	}
	return magnet, mg.InfoHash.HexString(), nil, nil
}

func displayName(magnet string) string {
	if mg, err := metainfo.ParseMagnetUri(magnet); err == nil && mg.DisplayName != "" {
		return mg.DisplayName
	}
	return ""
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
