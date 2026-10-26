package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Streaming.Enabled {
		t.Fatal("streaming should be off until the owner enables it")
	}
	if len(c.Streaming.Addons) != 2 || c.Streaming.Addons[0].Name != "torrentio" || c.Streaming.Addons[1].Name != "comet" {
		t.Fatalf("default addons = %+v", c.Streaming.Addons)
	}

	t.Setenv("WRENTV_STREAMING", "true")
	if c, err = Load(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !c.Streaming.Enabled {
		t.Fatal("WRENTV_STREAMING=true should enable it")
	}
}

func TestLiveTVSourceIDsAreUnique(t *testing.T) {
	l := LiveTVConfig{Sources: []LiveTVSource{{Name: "A"}, {ID: "same", Name: "B"}, {ID: "same", Name: "C"}, {ID: " padded ", Name: "D"}}}
	if !l.NormalizeSourceIDs() {
		t.Fatal("empty and duplicate ids were not normalized")
	}
	seen := map[string]bool{}
	for _, src := range l.Sources {
		if src.ID == "" || seen[src.ID] || strings.TrimSpace(src.ID) != src.ID {
			t.Fatalf("source ids are not unique and trimmed: %+v", l.Sources)
		}
		seen[src.ID] = true
	}
	if l.NormalizeSourceIDs() {
		t.Fatal("a normalized list should be stable")
	}
}

func TestLegacyRealDebridKeyCarriesOver(t *testing.T) {
	dir := t.TempDir()
	legacy := "torrents:\n  enabled: true\n  debrid:\n    service: realdebrid\n    api_key: rd-secret\n"
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Streaming.RealDebridKey != "rd-secret" {
		t.Fatalf("real-debrid key = %q", c.Streaming.RealDebridKey)
	}
	b, _ := os.ReadFile(filepath.Join(dir, configFile))
	if strings.Contains(string(b), "torrents:") {
		t.Fatal("legacy torrents section should be dropped on save")
	}
}

// An addon's discovered capabilities are part of the provider's identity: they
// are saved with the source and describe it again after a restart.
func TestLiveTVAddonCapabilitiesSurviveSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.LiveTV.Enabled = true
	c.LiveTV.Sources = []LiveTVSource{{
		ID: "highfly", Name: "Sports Streams", Kind: "stremio", Enabled: true,
		URL: "https://addons.example/manifest.json",
		Capabilities: &AddonCapabilities{
			ID: "community.sports", Name: "Sports Streams", Version: "1.0.0",
			Types: []string{"sport"}, Resources: []string{"catalog", "meta", "stream"},
			Catalogs:  []AddonCatalog{{Type: "sport", ID: "sports_live", Name: "Live Now", Searchable: true, Browsable: true, Skip: true}},
			Models:    []AddonTypeModel{{Type: "sport", Model: "events", Catalogs: 1, Catalog: true, Meta: true, Stream: true}},
			Browsable: true, Playable: true,
			Limitations: []string{"It lists no TV channel catalogs."},
		},
	}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.LiveTV.Sources) != 1 {
		t.Fatalf("sources = %+v", reloaded.LiveTV.Sources)
	}
	caps := reloaded.LiveTV.Sources[0].Capabilities
	if caps == nil {
		t.Fatal("capabilities were not reloaded")
	}
	if caps.ID != "community.sports" || !caps.Browsable || !caps.Playable || caps.SportCatalogs() != 1 {
		t.Fatalf("capabilities = %+v", caps)
	}
	if !caps.HasBrowsableModel() || caps.HasTVChannels() {
		t.Fatalf("capability model = %+v", caps.Models)
	}
	if len(caps.Catalogs) != 1 || !caps.Catalogs[0].Searchable || !caps.Catalogs[0].Browsable {
		t.Fatalf("catalogs = %+v", caps.Catalogs)
	}
	if len(caps.Limitations) != 1 {
		t.Fatalf("limitations = %+v", caps.Limitations)
	}
}
