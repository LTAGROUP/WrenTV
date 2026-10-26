package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

const celticsPlaylist = `#EXTM3U
#EXTINF:-1 tvg-id="celtics-heat" group-title="NBA",Boston Celtics vs Miami Heat
http://127.0.0.1:1/celtics.m3u8
`

const unrelatedPlaylist = `#EXTM3U
#EXTINF:-1 group-title="News",World News
http://127.0.0.1:1/news.m3u8
`

// TestBrowseLiveTVIsReadyOnFirstVisit is the regression for the empty Browse
// Live TV page: a match computed before the first provider refresh must be
// reported as pending (and never cached), and a refresh must invalidate a
// cached match instead of serving it until its TTL runs out.
func TestBrowseLiveTVIsReadyOnFirstVisit(t *testing.T) {
	f := newSportsFixture(t)
	var playlist atomic.Value
	playlist.Store(celticsPlaylist)
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		_, _ = io.WriteString(w, playlist.Load().(string))
	}))
	t.Cleanup(media.Close)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "iptv", Name: "Fixture IPTV", Kind: "m3u",
		URL: media.URL + "/tv.m3u", Enabled: true,
	})
	sportsProviderRoutes(f)

	// The client has just loaded the page: providers are configured but their
	// first refresh has not finished. This is a pending answer, not "nothing".
	rec, early := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("early streams: %d %s", rec.Code, rec.Body.String())
	}
	if early["ready"] != false || early["liveTV"] != false || early["code"] != "livetv_pending" {
		t.Fatalf("early streams = %+v", early)
	}
	if got, _ := early["groups"].([]any); len(got) != 0 {
		t.Fatalf("early groups = %+v", got)
	}
	_, listing := f.do(t, "GET", "/api/livetv/channels", "")
	if listing["prepared"] != false {
		t.Fatalf("channels claimed to be prepared: %+v", listing)
	}
	if providers, _ := listing["providers"].([]any); len(providers) != 1 {
		t.Fatalf("providers = %+v", listing["providers"])
	}

	startSportsLiveTV(t, f)
	// Same URL, no prior search: the first real answer arrives now. The early
	// empty answer must not have been cached.
	rec, ready := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("streams after start: %d %s", rec.Code, rec.Body.String())
	}
	if ready["ready"] != true || ready["liveTV"] != true {
		t.Fatalf("streams after start = %+v", ready)
	}
	groups, _ := ready["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("first visit did not find the channel: %+v", ready)
	}
	primary := groups[0].(map[string]any)["primary"].(map[string]any)
	if primary["channelName"] != "Boston Celtics vs Miami Heat" {
		t.Fatalf("wrong channel matched: %+v", primary)
	}
	_, listing = f.do(t, "GET", "/api/livetv/channels", "")
	if listing["prepared"] != true {
		t.Fatalf("channels are still unprepared after the refresh: %+v", listing)
	}
	if channels, _ := listing["channels"].([]any); len(channels) != 1 {
		t.Fatalf("channels = %+v", listing["channels"])
	}

	// A provider refresh replaces the playlist. Returning to the page in the
	// same 15-second match window must see the new data, not the cached match.
	oldRevision := f.s.LiveTV.Revision()
	playlist.Store(unrelatedPlaylist)
	f.s.LiveTV.Refresh(true)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := f.s.LiveTV.Status()
		if f.s.LiveTV.Revision() > oldRevision && !status.Refreshing {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if f.s.LiveTV.Revision() == oldRevision {
		t.Fatal("the provider refresh never completed")
	}
	rec, after := f.do(t, "GET", "/api/sports/events/402/streams", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("streams after refresh: %d %s", rec.Code, rec.Body.String())
	}
	if groups, _ := after["groups"].([]any); len(groups) != 0 {
		t.Fatalf("a cached match outlived the provider refresh: %+v", after)
	}
}

// TestLiveChannelsReportReadiness keeps the client contract explicit: the
// listing says whether the first refresh finished, so an empty list can be
// distinguished from a provider with nothing to show.
func TestLiveChannelsReportReadiness(t *testing.T) {
	f := newSportsFixture(t)
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		_, _ = io.WriteString(w, celticsPlaylist)
	}))
	t.Cleanup(media.Close)
	configuredSportsLiveTV(f, config.LiveTVSource{
		ID: "iptv", Name: "Fixture IPTV", Kind: "m3u",
		URL: media.URL + "/tv.m3u", Enabled: true,
	})
	sportsProviderRoutes(f)
	startSportsLiveTV(t, f)

	rec, body := f.do(t, "GET", "/api/livetv/channels", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("channels: %d %s", rec.Code, rec.Body.String())
	}
	if body["prepared"] != true {
		t.Fatalf("prepared = %v", body["prepared"])
	}
	if _, ok := body["refreshing"]; !ok {
		t.Fatal("the response does not say whether a refresh is running")
	}
	if body["revision"] == nil {
		t.Fatal("the response carries no provider revision")
	}
	if channels, _ := body["channels"].([]any); len(channels) != 1 {
		t.Fatalf("channels = %+v", body["channels"])
	}
}
