package api

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
)

func TestUsenetSettingsKeepSecrets(t *testing.T) {
	c := &config.StreamingConfig{
		Indexers:    []config.IndexerConfig{{Name: "NZBGeek", URL: "https://api.nzbgeek.info/api", APIKey: "k1", Enabled: true}},
		NewsServers: []config.NewsServerConfig{{Host: "news.example.com", Username: "me", Password: "p1", Connections: 10}},
	}
	for _, u := range []string{"https://api.nzbgeek.info", "https://api.nzbgeek.info/api", "https://nzbgeek.info/"} {
		ixs := []indexerPatch{{IndexerConfig: config.IndexerConfig{Name: "NZBGeek", URL: u, Enabled: true}}}
		nss := []newsServerPatch{{NewsServerConfig: config.NewsServerConfig{Host: "news.example.com", Username: "me", Connections: 10}}}
		got, servers, err := usenetFromPatch(c, &ixs, &nss)
		if err != nil || got[0].APIKey != "k1" || servers[0].Password != "p1" {
			t.Fatalf("%s: key %q, password %q, err %v", u, got[0].APIKey, servers[0].Password, err)
		}
	}
	// A new key replaces the saved one; another site doesn't get it.
	ixs := []indexerPatch{{IndexerConfig: config.IndexerConfig{Name: "Geek", URL: "https://api.nzbgeek.info"}, APIKey: "k2"},
		{IndexerConfig: config.IndexerConfig{Name: "Other", URL: "https://other.example.org"}}}
	got, _, _ := usenetFromPatch(c, &ixs, nil)
	if got[0].APIKey != "k2" || got[1].APIKey != "" {
		t.Fatalf("keys = %q, %q", got[0].APIKey, got[1].APIKey)
	}
}
