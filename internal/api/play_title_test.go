package api

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/media"
)

func TestNotTheTitle(t *testing.T) {
	film := &db.Item{Kind: "movie", RuntimeMin: 172}
	video := []media.Stream{{}}
	for _, c := range []struct {
		it    *db.Item
		info  media.Info
		wrong bool
	}{
		{film, media.Info{Duration: 172 * 60, Video: video}, false},
		{film, media.Info{Duration: 150 * 60, Video: video}, false}, // a shorter cut
		{film, media.Info{Duration: 150, Video: video}, true},       // a trailer
		{film, media.Info{Duration: 6 * 60, Video: video}, true},    // a prologue
		{film, media.Info{Duration: 172 * 60}, true},                // sound only
		{&db.Item{Kind: "movie"}, media.Info{Duration: 150, Video: video}, false},
		{&db.Item{Kind: "episode", RuntimeMin: 60}, media.Info{Duration: 20 * 60, Video: video}, false},
	} {
		if got := notTheTitle(c.it, &c.info) != ""; got != c.wrong {
			t.Errorf("%+v %v: wrong=%v", c.it, c.info.Duration, got)
		}
	}
}
