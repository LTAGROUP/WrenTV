package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/go-chi/chi/v5"
)

func TestSearchKey(t *testing.T) {
	if searchKey("Spider-Man: Collection") != "spidermancollection" || searchKey(" Harry Potter ") != "harrypotter" {
		t.Fatal(searchKey("Spider-Man: Collection"))
	}
}

func TestCollectionPages(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &db.User{Username: "u", AllowDownloads: -1, MaxStreams: -1, MaxDownloads: -1}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib := &db.Library{Name: "Movies", Type: "movies", Paths: []string{t.TempDir()}}
	if err := d.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	collection := func(name string, n int) *db.Collection {
		c := &db.Collection{Name: name, Kind: "manual"}
		if err := d.SaveCollection(ctx, c); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for i := 0; i < n; i++ {
			it := &db.Item{LibraryID: lib.ID, Kind: "movie", Title: fmt.Sprintf("%s %03d", name, i), MatchKey: fmt.Sprintf("%s%d", name, i)}
			if err := d.InsertItem(ctx, it); err != nil {
				t.Fatal(err)
			}
			if err := d.InsertFile(ctx, &db.MediaFile{ItemID: it.ID, LibraryID: lib.ID, Path: "/m/" + it.ID + ".mkv"}); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, it.ID)
		}
		if err := d.AddToCollection(ctx, c.ID, ids); err != nil {
			t.Fatal(err)
		}
		return c
	}
	big, small := collection("Big", 100), collection("Small", 50)
	s := &Server{Cfg: &config.Config{}, DB: d}
	page := func(c *db.Collection, query string) (titles []string, total int) {
		req := httptest.NewRequest("GET", "/api/collections/"+c.ID+"?"+query, nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", c.ID)
		req = req.WithContext(context.WithValue(context.WithValue(req.Context(), chi.RouteCtxKey, rc), principalKey, &auth.Principal{User: u, Role: "owner"}))
		w := httptest.NewRecorder()
		s.handleCollection(w, req)
		var out struct {
			Entries []titleView `json:"entries"`
			Total   int         `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		for _, e := range out.Entries {
			if e.Item == nil {
				t.Fatalf("entry without its item: %+v", e)
			}
			titles = append(titles, e.Title)
		}
		return titles, out.Total
	}
	if got, total := page(big, "limit=40"); len(got) != 40 || total != 100 || got[0] != "Big 000" {
		t.Errorf("first page: %d of %d, %v", len(got), total, got[:min(len(got), 1)])
	}
	if got, _ := page(big, "offset=80&limit=40"); len(got) != 20 || got[0] != "Big 080" {
		t.Errorf("last page: %d, %v", len(got), got[:min(len(got), 1)])
	}
	if got, _ := page(big, "limit=40&sort=title&offset=40"); len(got) != 40 || got[0] != "Big 040" {
		t.Errorf("sorted page: %v", got[:min(len(got), 1)])
	}
	// Most of a collection in one page is all of it.
	if got, total := page(small, "limit=40"); len(got) != 50 || total != 50 {
		t.Errorf("small collection: %d of %d", len(got), total)
	}
}
