//go:build live

package streaming

import (
	"context"
	"testing"
	"time"
)

// go test -tags live -run Live ./internal/streaming
func TestLiveAddons(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, raw := range []string{"https://torrentio.strem.fun/manifest.json", "https://comet.feels.legal/manifest.json"} {
		a, _ := ParseAddon("", raw)
		name, err := Manifest(ctx, a)
		t.Logf("manifest %s: %q %v", a.BaseURL, name, err)
		for _, q := range []struct {
			id   string
			s, e int
		}{{"tt15239678", 0, 0}, {"tt0903747", 1, 2}} {
			rels, err := Search(ctx, []Addon{a}, q.id, q.s, q.e)
			n4, n1 := len(Rank(rels, "2160p", 0, q.s)), len(Rank(rels, "1080p", 0, q.s))
			t.Logf("  %s s%d e%d: %d releases (2160p %d, 1080p %d) err=%v", q.id, q.s, q.e, len(rels), n4, n1, err)
			if len(rels) > 0 {
				r := Rank(rels, "1080p", 0, q.s)
				if len(r) > 0 {
					t.Logf("    top 1080p: %q size=%d seeders=%d fileIdx=%d file=%q", r[0].Name, r[0].Size, r[0].Seeders, r[0].FileIdx, r[0].FileName)
				}
			}
		}
	}
}
