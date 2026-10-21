package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ---------- hubs ----------

// ShelfDef is one row of titles inside a hub.
type ShelfDef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Source is "discover" (TMDB's catalog, filtered), "chart" (a TMDB
	// chart), "library" (the server's own items, filtered by Rules) or
	// "collection" (a collection's titles, by CollectionID).
	Source    string `json:"source"`
	MediaType string `json:"mediaType,omitempty"` // "movie" or "show"
	// Sort is "popular", "top_rated" or "new" for discover, and a library
	// sort ("added", "rating", "title", "year") for library shelves.
	Sort         string          `json:"sort,omitempty"`
	Chart        string          `json:"chart,omitempty"`
	GenreIDs     []int           `json:"genreIds,omitempty"`  // TMDB genres (any of)
	Providers    []int           `json:"providers,omitempty"` // TMDB watch providers (any of)
	Rules        CollectionRules `json:"rules,omitempty"`
	CollectionID string          `json:"collectionId,omitempty"`
	Ranked       bool            `json:"ranked,omitempty"`
}

// Hub is a destination with its own shelves: a genre, a streaming service
// or anything an admin puts together.
type Hub struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"` // "genre", "service" or "custom"
	Tagline      string     `json:"tagline"`
	BackdropPath string     `json:"backdropPath,omitempty"`
	LogoPath     string     `json:"logoPath,omitempty"`
	Color        string     `json:"color,omitempty"` // "#rrggbb" behind the tile
	Shelves      []ShelfDef `json:"shelves"`
	CreatedAt    int64      `json:"createdAt"`
	UpdatedAt    int64      `json:"updatedAt"`
}

// HubCategory groups hubs into one row of tiles.
type HubCategory struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Style     string   `json:"style"` // "tiles" (artwork) or "logos"
	HubIDs    []string `json:"hubIds"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

func scanHub(r interface{ Scan(...any) error }) (*Hub, error) {
	h := &Hub{}
	var shelves string
	err := r.Scan(&h.ID, &h.Name, &h.Kind, &h.Tagline, &h.BackdropPath, &h.LogoPath, &h.Color, &shelves, &h.CreatedAt, &h.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(shelves), &h.Shelves)
	if h.Shelves == nil {
		h.Shelves = []ShelfDef{}
	}
	return h, nil
}

const hubCols = `id, name, kind, tagline, backdrop_path, logo_path, color, shelves, created_at, updated_at`

func (d *DB) ListHubs(ctx context.Context) ([]*Hub, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+hubCols+` FROM hubs ORDER BY kind, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Hub{}
	for rows.Next() {
		h, err := scanHub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (d *DB) GetHub(ctx context.Context, id string) (*Hub, error) {
	return scanHub(d.QueryRowContext(ctx, `SELECT `+hubCols+` FROM hubs WHERE id=?`, id))
}

func (d *DB) SaveHub(ctx context.Context, h *Hub) error {
	now := Now()
	if h.ID == "" {
		h.ID = NewID()
		h.CreatedAt = now
	}
	h.UpdatedAt = now
	if h.Shelves == nil {
		h.Shelves = []ShelfDef{}
	}
	shelves, _ := json.Marshal(h.Shelves)
	_, err := d.ExecContext(ctx, `INSERT INTO hubs (`+hubCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, kind=excluded.kind, tagline=excluded.tagline, backdrop_path=excluded.backdrop_path,
		logo_path=excluded.logo_path, color=excluded.color, shelves=excluded.shelves, updated_at=excluded.updated_at`,
		h.ID, h.Name, h.Kind, h.Tagline, h.BackdropPath, h.LogoPath, h.Color, string(shelves), h.CreatedAt, h.UpdatedAt)
	return err
}

// DeleteHub removes a hub and takes it out of every category.
func (d *DB) DeleteHub(ctx context.Context, id string) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM hubs WHERE id=?`, id); err != nil {
		return err
	}
	cs, err := d.ListHubCategories(ctx)
	if err != nil {
		return err
	}
	for _, c := range cs {
		kept := []string{}
		for _, h := range c.HubIDs {
			if h != id {
				kept = append(kept, h)
			}
		}
		if len(kept) != len(c.HubIDs) {
			c.HubIDs = kept
			if err := d.SaveHubCategory(ctx, c); err != nil {
				return err
			}
		}
	}
	return nil
}

const hubCategoryCols = `id, name, style, hub_ids, created_at, updated_at`

func scanHubCategory(r interface{ Scan(...any) error }) (*HubCategory, error) {
	c := &HubCategory{}
	var ids string
	err := r.Scan(&c.ID, &c.Name, &c.Style, &ids, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(ids), &c.HubIDs)
	if c.HubIDs == nil {
		c.HubIDs = []string{}
	}
	return c, nil
}

func (d *DB) ListHubCategories(ctx context.Context) ([]*HubCategory, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+hubCategoryCols+` FROM hub_categories ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*HubCategory{}
	for rows.Next() {
		c, err := scanHubCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) GetHubCategory(ctx context.Context, id string) (*HubCategory, error) {
	return scanHubCategory(d.QueryRowContext(ctx, `SELECT `+hubCategoryCols+` FROM hub_categories WHERE id=?`, id))
}

func (d *DB) SaveHubCategory(ctx context.Context, c *HubCategory) error {
	now := Now()
	if c.ID == "" {
		c.ID = NewID()
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.HubIDs == nil {
		c.HubIDs = []string{}
	}
	ids, _ := json.Marshal(c.HubIDs)
	_, err := d.ExecContext(ctx, `INSERT INTO hub_categories (`+hubCategoryCols+`) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, style=excluded.style, hub_ids=excluded.hub_ids, updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Style, string(ids), c.CreatedAt, c.UpdatedAt)
	return err
}

func (d *DB) DeleteHubCategory(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM hub_categories WHERE id=?`, id)
	return err
}

// ---------- layouts ----------

// LayoutEntry is one row of a page's layout.
type LayoutEntry struct {
	// Type is "shelf" (a built-in row, by Ref), "hub", "category" or
	// "collection" (by ID in Ref).
	Type   string `json:"type"`
	Ref    string `json:"ref"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Layout returns the rows saved for a page ("home", "movies", "shows",
// "library:<id>"): the user's own when userID is set, the admin's default
// when it's empty. ok is false when none was saved.
func (d *DB) Layout(ctx context.Context, scope, userID string) (entries []LayoutEntry, ok bool, err error) {
	var body string
	err = d.QueryRowContext(ctx, `SELECT entries FROM layouts WHERE scope=? AND user_id=?`, scope, userID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	_ = json.Unmarshal([]byte(body), &entries)
	if entries == nil {
		entries = []LayoutEntry{}
	}
	return entries, true, nil
}

func (d *DB) SetLayout(ctx context.Context, scope, userID string, entries []LayoutEntry) error {
	if entries == nil {
		entries = []LayoutEntry{}
	}
	body, _ := json.Marshal(entries)
	_, err := d.ExecContext(ctx, `INSERT INTO layouts (scope, user_id, entries, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(scope, user_id) DO UPDATE SET entries=excluded.entries, updated_at=excluded.updated_at`, scope, userID, string(body), Now())
	return err
}

// ResetLayout forgets a saved layout, so the page falls back to the next one
// (a user's to the admin's default, the default to the built-in rows).
func (d *DB) ResetLayout(ctx context.Context, scope, userID string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM layouts WHERE scope=? AND user_id=?`, scope, userID)
	return err
}

// ---------- seeded hubs ----------

// genreSeed is a starter genre hub: its TMDB genres for movies and shows
// and the names the library tags it with.
type genreSeed struct {
	id, name, tagline string
	movie, show       int
	tags              []string
}

var genreSeeds = []genreSeed{
	{"action", "Action", "Chases, fights and big set pieces.", 28, 10759, []string{"Action", "Action & Adventure"}},
	{"comedy", "Comedy", "Something to laugh at.", 35, 35, []string{"Comedy"}},
	{"drama", "Drama", "Stories that stay with you.", 18, 18, []string{"Drama"}},
	{"scifi", "Sci-Fi", "Other worlds, other times.", 878, 10765, []string{"Science Fiction", "Sci-Fi & Fantasy"}},
	{"horror", "Horror", "Lights off.", 27, 0, []string{"Horror"}},
	{"thriller", "Thriller", "Edge of your seat.", 53, 0, []string{"Thriller"}},
	{"crime", "Crime", "Heists, detectives and the people they chase.", 80, 80, []string{"Crime"}},
	{"animation", "Animation", "Drawn, modelled and stop-motion.", 16, 16, []string{"Animation"}},
	{"documentary", "Documentary", "True stories.", 99, 99, []string{"Documentary"}},
	{"family", "Family", "For everyone on the couch.", 10751, 10751, []string{"Family"}},
}

// serviceSeeds are starter streaming-service hubs, by TMDB watch provider.
var serviceSeeds = []struct {
	id, name, color string
	provider        int
}{
	{"netflix", "Netflix", "#b20710", 8},
	{"prime", "Prime Video", "#0f79af", 9},
	{"disney", "Disney+", "#0b1f5c", 337},
	{"max", "Max", "#002be7", 1899},
	{"hulu", "Hulu", "#118a4f", 15},
	{"appletv", "Apple TV+", "#2b2b2b", 350},
	{"paramount", "Paramount+", "#0064ff", 531},
	{"peacock", "Peacock", "#3a3a3a", 386},
}

func genreShelves(g genreSeed) []ShelfDef {
	shelves := []ShelfDef{{ID: "popular-movies", Title: "Popular " + g.name + " Movies", Source: "discover", MediaType: "movie", Sort: "popular", GenreIDs: []int{g.movie}}}
	if g.show > 0 {
		shelves = append(shelves, ShelfDef{ID: "popular-shows", Title: "Popular " + g.name + " Series", Source: "discover", MediaType: "show", Sort: "popular", GenreIDs: []int{g.show}})
	}
	return append(shelves,
		ShelfDef{ID: "library", Title: "In Your Library", Source: "library", Sort: "added", Rules: CollectionRules{Genres: g.tags}},
		ShelfDef{ID: "top-movies", Title: "Top Rated " + g.name, Source: "discover", MediaType: "movie", Sort: "top_rated", GenreIDs: []int{g.movie}, Ranked: true},
		ShelfDef{ID: "new-movies", Title: "New " + g.name + " Releases", Source: "discover", MediaType: "movie", Sort: "new", GenreIDs: []int{g.movie}},
	)
}

func serviceShelves(name string, provider int) []ShelfDef {
	p := []int{provider}
	return []ShelfDef{
		{ID: "popular-movies", Title: "Popular Movies on " + name, Source: "discover", MediaType: "movie", Sort: "popular", Providers: p},
		{ID: "popular-shows", Title: "Popular Series on " + name, Source: "discover", MediaType: "show", Sort: "popular", Providers: p},
		{ID: "new-movies", Title: "New Movies", Source: "discover", MediaType: "movie", Sort: "new", Providers: p},
		{ID: "new-shows", Title: "New Series", Source: "discover", MediaType: "show", Sort: "new", Providers: p},
		{ID: "top-movies", Title: "Top Rated Movies", Source: "discover", MediaType: "movie", Sort: "top_rated", Providers: p, Ranked: true},
		{ID: "top-shows", Title: "Top Rated Series", Source: "discover", MediaType: "show", Sort: "top_rated", Providers: p, Ranked: true},
	}
}

// Seeded category IDs, used by the built-in home layout.
const (
	GenresCategoryID   = "genres"
	ServicesCategoryID = "streaming-services"
)

// seedHubsSQL inserts the starter genre and streaming-service hubs and
// their two categories. Admins can change or delete them afterwards.
func seedHubsSQL() string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	js := func(v any) string { b, _ := json.Marshal(v); return q(string(b)) }
	var b strings.Builder
	var genres, services []string
	for _, g := range genreSeeds {
		id := "genre-" + g.id
		genres = append(genres, id)
		fmt.Fprintf(&b, "INSERT INTO hubs (%s) VALUES (%s,%s,'genre',%s,'','','',%s,strftime('%%s','now'),strftime('%%s','now'));\n",
			hubCols, q(id), q(g.name), q(g.tagline), js(genreShelves(g)))
	}
	for _, s := range serviceSeeds {
		id := "service-" + s.id
		services = append(services, id)
		fmt.Fprintf(&b, "INSERT INTO hubs (%s) VALUES (%s,%s,'service',%s,'','',%s,%s,strftime('%%s','now'),strftime('%%s','now'));\n",
			hubCols, q(id), q(s.name), q("What's on "+s.name+"."), q(s.color), js(serviceShelves(s.name, s.provider)))
	}
	fmt.Fprintf(&b, "INSERT INTO hub_categories (%s) VALUES (%s,'Genres','tiles',%s,strftime('%%s','now'),strftime('%%s','now'));\n",
		hubCategoryCols, q(GenresCategoryID), js(genres))
	fmt.Fprintf(&b, "INSERT INTO hub_categories (%s) VALUES (%s,'Streaming Services','logos',%s,strftime('%%s','now'),strftime('%%s','now'));\n",
		hubCategoryCols, q(ServicesCategoryID), js(services))
	return b.String()
}
