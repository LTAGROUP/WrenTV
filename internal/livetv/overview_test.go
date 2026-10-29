package livetv

import (
	"fmt"
	"testing"
	"time"
)

func TestOverviewTakesProvidersInTurn(t *testing.T) {
	now := time.Now().Unix()
	s := &Service{guide: Guide{}}
	// Both providers carry more news than a shelf holds, the first listed first.
	for _, src := range []string{"a", "b"} {
		for i := range shelfMax + 6 {
			c := &Channel{ID: fmt.Sprintf("%s%d", src, i), Name: fmt.Sprintf("News %s%d", src, i), Group: "News", Source: src}
			s.visible = append(s.visible, c)
			s.guide[c.ID] = []Program{{Title: fmt.Sprintf("Report %s%d", src, i), Start: now - 600, End: now + 600}}
		}
	}
	// The same programme on both: it shows once.
	s.guide["a0"][0].Title, s.guide["b0"][0].Title = "The Bulletin", "The Bulletin"

	var news *Shelf
	for _, sh := range s.Overview(nil, nil, nil) {
		if sh.ID == "news" {
			news = &sh
		}
	}
	if news == nil {
		t.Fatal("no news shelf")
	}
	from := map[byte]int{}
	bulletins := 0
	for _, tl := range news.Tiles {
		from[tl.ChannelID[0]]++
		if tl.Program.Title == "The Bulletin" {
			bulletins++
		}
	}
	if from['a'] == 0 || from['b'] == 0 {
		t.Fatalf("news from one provider only: %v", from)
	}
	if bulletins != 1 {
		t.Fatalf("a programme on both providers showed %d times", bulletins)
	}
}
