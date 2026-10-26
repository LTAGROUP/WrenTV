package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// The viewer's Live TV list is one endpoint: channels and addon events arrive
// together, and the provider list describes both. The events carry no channel
// number and no stream URL.
func TestLiveChannelsIncludeAddonEvents(t *testing.T) {
	f := newSportAddonFixture(t)
	src := config.LiveTVSource{ID: "highfly", Name: "Sports Streams", Kind: "sport", URL: f.addon.URL + "/manifest.json", Enabled: true}
	h := newLiveSportTestServer(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.s.LiveTV.Start(ctx)
	h.s.LiveTV.Refresh(true)

	deadline := time.Now().Add(5 * time.Second)
	var body map[string]any
	for time.Now().Before(deadline) {
		rec, got := h.do(t, "GET", "/api/livetv/channels", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("channels status = %d: %s", rec.Code, rec.Body.String())
		}
		body = got
		if events, _ := got["events"].([]any); len(events) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	events, _ := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("events = %s", body)
	}
	first := events[0].(map[string]any)
	if first["source"] != "highfly" || first["sourceName"] != "Sports Streams" {
		t.Fatalf("event source = %+v", first)
	}
	if first["type"] != "sport" || first["itemId"] != "streamed:one" || first["name"] != "Alpha vs Beta" {
		t.Fatalf("event = %+v", first)
	}
	if _, hasNumber := first["number"]; hasNumber {
		t.Fatalf("an event was given a channel number: %+v", first)
	}
	// No provider link or headers leak into the list the browser receives.
	if _, hasURL := first["url"]; hasURL {
		t.Fatalf("event carried a stream URL: %+v", first)
	}

	// The one provider list contains the addon with its event count, so the
	// picker does not need the addon browsing endpoints to describe it.
	providers, _ := body["providers"].([]any)
	if len(providers) != 1 {
		t.Fatalf("providers = %+v", body["providers"])
	}
	provider := providers[0].(map[string]any)
	if provider["id"] != "highfly" || provider["addon"] != true || provider["events"] != float64(2) {
		t.Fatalf("provider = %+v", provider)
	}

	// Filtering the list by source keeps the event identity intact.
	rec, filtered := h.do(t, "GET", "/api/livetv/channels?source=highfly", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered status = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := filtered["events"].([]any); len(got) != 2 {
		t.Fatalf("filtered events = %s", rec.Body.String())
	}
	otherRec, other := h.do(t, "GET", "/api/livetv/channels?source=elsewhere", "")
	if otherRec.Code != http.StatusOK {
		t.Fatalf("unknown source status = %d", otherRec.Code)
	}
	if got, _ := other["events"].([]any); len(got) != 0 {
		t.Fatalf("another source picked up the events: %s", otherRec.Body.String())
	}
}
