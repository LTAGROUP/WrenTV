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
