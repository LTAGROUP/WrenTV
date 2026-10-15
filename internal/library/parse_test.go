package library

import "testing"

func TestParseMovie(t *testing.T) {
	cases := []struct {
		root, path, title string
		year              int
	}{
		{"/m", "/m/The Matrix (1999)/The Matrix (1999) - 2160p.mkv", "The Matrix", 1999},
		{"/m", "/m/Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.DV.HDR.HEVC.TrueHD.Atmos.7.1-GRP.mkv", "Dune Part Two", 2024},
		{"/m", "/m/2001 A Space Odyssey (1968)/movie.mkv", "2001 A Space Odyssey", 1968},
		{"/m", "/m/Blade Runner 2049 (2017) [tmdbid-335984]/Blade Runner 2049.mkv", "Blade Runner 2049", 2017},
		{"/m", "/m/Oppenheimer.2023.1080p.WEB-DL.DDP5.1.Atmos.H.264.mkv", "Oppenheimer", 2023},
		{"/m", "/m/Heat/Heat.mkv", "Heat", 0},
	}
	for _, c := range cases {
		got := ParseMovie(c.root, c.path)
		if got.Title != c.title || got.Year != c.year {
			t.Errorf("%s: got %q (%d), want %q (%d)", c.path, got.Title, got.Year, c.title, c.year)
		}
	}
	if got := ParseMovie("/m", "/m/Blade Runner 2049 (2017) [tmdbid-335984]/x.mkv"); got.TMDBID != 335984 {
		t.Errorf("tmdb id: got %d", got.TMDBID)
	}
	if got := ParseMovie("/m", "/m/2 Fast 2 Furious (2003) {imdb-tt0322259}/2.Fast.2.Furious.2003.1080p.BluRay.mkv"); got.IMDBID != "tt0322259" || got.Title != "2 Fast 2 Furious" || got.Year != 2003 {
		t.Errorf("imdb tag: got %+v", got)
	}
	if got := ParseEpisode("/tv", "/tv/Aladdin (1994) {imdb-tt0105935}/Season 1/Aladdin.S01E02.mkv"); got.ShowIMDB != "tt0105935" || got.ShowTitle != "Aladdin" || got.ShowYear != 1994 {
		t.Errorf("show imdb tag: got %+v", got)
	}
}

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		root, path, show string
		year, s, e, end  int
	}{
		{"/tv", "/tv/Severance (2022)/Season 2/Severance - S02E03 - Who Is Alive.mkv", "Severance", 2022, 2, 3, 0},
		{"/tv", "/tv/The.Bear.S03.2160p.WEB-DL.DV.HDR/The.Bear.S03E01.2160p.WEB-DL.mkv", "The Bear", 0, 3, 1, 0},
		{"/tv", "/tv/Shogun.2024.S01E02.1080p.WEB.h264.mkv", "Shogun", 2024, 1, 2, 0},
		{"/tv", "/tv/Friends/Season 1/1x05.mkv", "Friends", 0, 1, 5, 0},
		{"/tv", "/tv/Friends/Season 02/Episode 7.mkv", "Friends", 0, 2, 7, 0},
		{"/tv", "/tv/Doctor Who/Specials/Doctor Who S00E01.mkv", "Doctor Who", 0, 0, 1, 0},
		{"/tv", "/tv/Lost/Season 1/Lost.S01E01E02.Pilot.mkv", "Lost", 0, 1, 1, 2},
		{"", "Better.Call.Saul.S06.COMPLETE.1080p/Better.Call.Saul.S06E13.1080p.mkv", "Better Call Saul", 0, 6, 13, 0},
	}
	for _, c := range cases {
		got := ParseEpisode(c.root, c.path)
		if !got.OK || got.ShowTitle != c.show || got.ShowYear != c.year || got.Season != c.s || got.Episode != c.e || got.EndEp != c.end {
			t.Errorf("%s: got %+v", c.path, got)
		}
	}
}
