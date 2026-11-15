package db

import (
	"context"
	"testing"
	"time"
)

// Stats split streamed plays by the service, the indexer or addon that
// found them, and add up what each news server sent.
func TestSourceStats(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	u := &User{Username: "ann", DisplayName: "Ann"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	lib, _ := d.StreamLibrary(ctx, "movies")
	play := func(n int, kind, provider, addon string) {
		for i := 0; i < n; i++ {
			it := &Item{LibraryID: lib.ID, Kind: "movie", MatchKey: "tmdb:" + NewID(), Title: "Film", SeasonNumber: -1, EpisodeNumber: -1}
			if err := d.InsertItem(ctx, it); err != nil {
				t.Fatal(err)
			}
			if err := d.StartPlay(ctx, PlayStart{UserID: u.ID, Item: it, Method: "direct",
				SourceKind: kind, SourceProvider: provider, SourceAddon: addon}); err != nil {
				t.Fatal(err)
			}
		}
	}
	play(3, "usenet", "usenet", "NZBGeek")
	play(1, "usenet", "torbox", "DrunkenSlug")
	play(2, "torrent", "realdebrid", "Torrentio")
	play(1, "", "", "") // a stream played before sources were recorded

	today := time.Now().UTC().Format("2006-01-02")
	for _, x := range []struct {
		server string
		bytes  int64
	}{{"news.primary.example", 700}, {"news.primary.example", 300}, {"block.example", 50}} {
		if err := d.AddNewsUsage(ctx, today, x.server, 1, x.bytes); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.AddNewsUsage(ctx, "2000-01-01", "news.primary.example", 1, 1<<40) // outside the period

	s, err := d.Stats(ctx, time.Now().Add(-24*time.Hour).Unix(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sources) != 4 || s.Sources[0].Name != "usenet" || s.Sources[0].Plays != 3 {
		t.Fatalf("sources = %+v", s.Sources)
	}
	if got := s.Sources[len(s.Sources)-1]; got.Name != "unknown" || got.Plays != 1 {
		t.Fatalf("a stream library play with no source = %+v, want unknown", got)
	}
	if len(s.Indexers) != 2 || s.Indexers[0] != (SourceStat{"NZBGeek", 3, 1, 0}) || s.Indexers[1].Name != "DrunkenSlug" {
		t.Fatalf("indexers = %+v", s.Indexers)
	}
	if len(s.Addons) != 1 || s.Addons[0].Name != "Torrentio" || s.Addons[0].Plays != 2 {
		t.Fatalf("addons = %+v", s.Addons)
	}
	if len(s.NewsServers) != 2 || s.NewsServers[0] != (NewsUsage{"news.primary.example", 2, 1000}) || s.NewsServers[1].Bytes != 50 {
		t.Fatalf("news servers = %+v", s.NewsServers)
	}
}
