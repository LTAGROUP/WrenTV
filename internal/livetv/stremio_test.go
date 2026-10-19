package livetv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// A Stremio addon's tv catalog becomes channels, paged and grouped, and a
// channel's stream is asked for when it's played.
func TestStremioChannels(t *testing.T) {
	var addon *httptest.Server
	addon = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/manifest.json"):
			json.NewEncoder(w).Encode(map[string]any{"catalogs": []map[string]any{
				{"type": "movie", "id": "films"},
				{"type": "tv", "id": "search", "extra": []map[string]any{{"name": "search", "isRequired": true}}},
				{"type": "tv", "id": "chans", "name": "Channels"},
			}})
		case strings.HasSuffix(p, "/catalog/tv/chans.json"):
			json.NewEncoder(w).Encode(map[string]any{"metas": []map[string]any{
				{"id": "ch:news", "type": "tv", "name": "News 24", "logo": "http://logo/news.png", "genres": []string{"News"}},
				{"id": "ch:sport", "type": "tv", "name": "Sport One", "poster": "http://logo/sport.png"},
			}})
		case strings.HasSuffix(p, "/catalog/tv/chans/skip=2.json"):
			json.NewEncoder(w).Encode(map[string]any{"metas": []map[string]any{
				{"id": "ch:kids", "type": "tv", "name": "Kids"},
			}})
		case strings.HasSuffix(p, "/catalog/tv/chans/skip=3.json"):
			json.NewEncoder(w).Encode(map[string]any{"metas": []any{}})
		case strings.HasSuffix(p, "/stream/tv/ch:news.json"):
			json.NewEncoder(w).Encode(map[string]any{"streams": []map[string]any{
				{"infoHash": "abc"},
				{"url": "http://cdn.example/news.m3u8", "behaviorHints": map[string]any{"proxyHeaders": map[string]any{"request": map[string]string{"User-Agent": "Addon/1"}}}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer addon.Close()
	src := config.LiveTVSource{ID: "st", Name: "Addon", Kind: "stremio", Enabled: true, URL: addon.URL + "/tok/manifest.json"}
	s := New(&config.Config{LiveTV: config.LiveTVConfig{Enabled: true, Sources: []config.LiveTVSource{src}}}, t.TempDir())
	ctx := context.Background()
	if n, _, err := s.Check(ctx, src); err != nil || n != 3 {
		t.Fatalf("check = %d, %v", n, err)
	}
	s.refresh(ctx, true)
	chans := s.Channels()
	if len(chans) != 3 {
		t.Fatalf("channels = %d", len(chans))
	}
	byName := map[string]ChannelView{}
	for _, c := range chans {
		byName[c.Name] = c
	}
	if c := byName["News 24"]; c.Group != "News" || c.Logo != "http://logo/news.png" {
		t.Errorf("news: %+v", c.Channel)
	}
	if c := byName["Sport One"]; c.Group != "Channels" || c.Logo != "http://logo/sport.png" {
		t.Errorf("sport: %+v", c.Channel)
	}
	link, agent, err := s.resolveStremio(ctx, s.Channel(byName["News 24"].ID))
	if err != nil || link != "http://cdn.example/news.m3u8" || agent != "Addon/1" {
		t.Fatalf("resolve = %q, %q, %v", link, agent, err)
	}
	sess, err := s.Play(ctx, "u", byName["News 24"].ID, false)
	if err != nil || sess.upstream != "http://cdn.example/news.m3u8" || sess.Kind != "hls" {
		t.Fatalf("play = %+v, %v", sess, err)
	}
	if _, err := s.Play(ctx, "u", byName["Kids"].ID, false); err == nil {
		t.Fatal("a channel with no stream played")
	}
}
