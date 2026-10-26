package livetv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// A real sports addon needs the network and changes from minute to minute, so
// it is not part of the unit suite. Set WRENTV_SPORT_ADDON to an addon's
// manifest link to exercise its actual Stremio resources:
//
//	WRENTV_SPORT_ADDON=https://addon.example/<config>/manifest.json \
//	  go test ./internal/livetv -run TestLiveSportAddonResources -v
func TestLiveSportAddonResources(t *testing.T) {
	manifest := strings.TrimSpace(os.Getenv("WRENTV_SPORT_ADDON"))
	if manifest == "" {
		t.Skip("set WRENTV_SPORT_ADDON to a sports addon manifest link")
	}
	src := config.LiveTVSource{ID: "live", Name: "Live addon", Kind: "sport", URL: manifest, Enabled: true}
	s := New(&config.Config{}, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Acceptance is the manifest, not the presence of TV channels: the addon
	// must pass the capability check, and its sport catalogs must be browsable.
	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if !caps.HasBrowsableModel() || caps.SportCatalogs() == 0 {
		t.Fatalf("the addon has no browsable sport catalogs: %+v", caps)
	}
	if n, _, err := s.Check(ctx, src); err != nil || n != 0 {
		t.Fatalf("check = %d channels, %v; a sport-only addon is valid", n, err)
	}

	catalogs, err := s.SportCatalogs(ctx, src)
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	if len(catalogs) == 0 {
		t.Fatal("the addon declared no sport catalogs")
	}
	t.Logf("%d sport catalogs, first %q", len(catalogs), catalogs[0].Name)

	// Browse each shelf the addon declares until one event offers a playable
	// feed. An empty shelf is a valid answer and is not a failure.
	for _, catalog := range catalogs {
		items, err := s.SportItems(ctx, src, catalog.ID, "", 0)
		if err != nil {
			t.Errorf("catalog %s: %v", catalog.ID, err)
			continue
		}
		t.Logf("catalog %s (%s): %d events", catalog.ID, catalog.Name, len(items))
		for _, item := range items {
			if strings.TrimSpace(item.ID) == "" {
				t.Errorf("catalog %s listed an event without an id", catalog.ID)
				continue
			}
			streams, err := s.SportStreams(ctx, src, item.ID)
			if errors.Is(err, ErrNoSportStreams) {
				continue
			}
			if err != nil {
				t.Errorf("streams for %s: %v", item.ID, err)
				continue
			}
			for _, stream := range streams {
				if !strings.HasPrefix(stream.URL, "http://") && !strings.HasPrefix(stream.URL, "https://") {
					t.Errorf("event %s has a non-media feed %q", item.ID, stream.URL)
				}
			}
			t.Logf("event %q (%s): %d playable feeds, first labelled %q", item.Name, item.ID, len(streams), streams[0].Name)
			if os.Getenv("WRENTV_SPORT_ADDON_PLAYBACK") == "1" {
				assertLiveSportPlayback(t, s, item.ID, streams[0])
			}
			return
		}
	}
	t.Skip("the addon is not offering a playable event right now")
}

// assertLiveSportPlayback tunes the addon's real feed through the ordinary Live
// TV session, which is what the player uses. It proves the provider link, its
// headers and the HLS proxy work end to end.
func assertLiveSportPlayback(t *testing.T, s *Service, eventID string, stream SportStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sess, err := s.PlayURLWithHeaders(ctx, "viewer", "sports:live-test", stream.URL, stream.UserAgent, stream.Headers, false)
	if err != nil {
		t.Fatalf("session for %s: %v", eventID, err)
	}
	defer s.Stop(sess.ID, "viewer", false)
	t.Logf("session %s kind %s", sess.ID, sess.Type())
	rec := &responseRecorder{header: http.Header{}}
	sess.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live/"+sess.ID+"/index.m3u8", nil), "index.m3u8")
	if rec.status != http.StatusOK || !strings.Contains(rec.body.String(), "#EXTM3U") {
		t.Fatalf("live playlist through the proxy = %d %q", rec.status, rec.body.String())
	}
	t.Logf("playlist served through the proxy (%d bytes)", rec.body.Len())
}

// responseRecorder is the small part of httptest.ResponseRecorder the proxy
// needs, without buffering a whole segment in the test's memory report.
type responseRecorder struct {
	header http.Header
	body   strings.Builder
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}
func (r *responseRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
