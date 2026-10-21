package api

import "testing"

func TestCollectionShelves(t *testing.T) {
	if got := collectionShelves(10, make([][]string, 10), make([]int, 10), "Top 20"); len(got) != 0 {
		t.Fatalf("a small collection is a grid, got %+v", got)
	}
	n := 30
	genres := make([][]string, n)
	order := make([]int, n)
	for i := range genres {
		order[i] = i
		genres[i] = []string{"Drama"}
		if i%3 == 0 {
			genres[i] = append(genres[i], "Comedy")
		}
		if i < 2 {
			genres[i] = append(genres[i], "Western")
		}
	}
	got := collectionShelves(n, genres, order, "Top 20")
	// Drama is everything, so it's no row; Western has too few.
	if len(got) != 2 || got[0].Title != "Top 20" || len(got[0].Index) != 20 || got[1].Title != "Comedy" || len(got[1].Index) != 10 {
		t.Fatalf("got %+v", got)
	}
}

func TestSearchKey(t *testing.T) {
	if searchKey("Spider-Man: Collection") != "spidermancollection" || searchKey(" Harry Potter ") != "harrypotter" {
		t.Fatal(searchKey("Spider-Man: Collection"))
	}
}
