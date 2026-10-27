package onlinesubs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
)

func TestToVTT(t *testing.T) {
	srt := "1\r\n00:00:01,500 --> 00:00:03,000\r\nHello\r\nthere\r\n\r\n2\r\n0:00:04,25 --> 0:00:05,000\r\nCaf\xe9 \x93quoted\x94\r\n"
	got, err := ToVTT([]byte(srt))
	if err != nil {
		t.Fatal(err)
	}
	want := "WEBVTT\n\n00:00:01.500 --> 00:00:03.000\nHello\nthere\n\n00:00:04.250 --> 00:00:05.000\nCafé “quoted”\n\n"
	if string(got) != want {
		t.Fatalf("srt:\n%q\nwant\n%q", got, want)
	}

	ass := "[Script Info]\nTitle: x\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\\i1}Hi,{\\i0} you\\Nthere\n"
	got, err = ToVTT([]byte(ass))
	if err != nil {
		t.Fatal(err)
	}
	if want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHi, you\nthere\n\n"; string(got) != want {
		t.Fatalf("ass: %q", got)
	}
	if _, err := ToVTT([]byte("not subtitles")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestSearchAndFetch(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	for _, n := range []string{"Show.S01E01.srt", "Show.S01E02.srt"} {
		w, _ := zw.Create(n)
		w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\n" + n + "\n"))
	}
	zw.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/os/subtitles", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Header.Get("Api-Key") != "os-key" || q.Get("parent_imdb_id") != "123" || q.Get("languages") != "en,fr" {
			http.Error(w, `{"message":"bad query"}`, 400)
			return
		}
		w.Write([]byte(`{"data":[{"attributes":{"language":"fr","download_count":9,"release":"Show.S01E02.FR","files":[{"file_id":7}]}},
			{"attributes":{"language":"en","download_count":3,"release":"Show.S01E02.EN","files":[{"file_id":8}]}},
			{"attributes":{"language":"en","download_count":50,"machine_translated":true,"release":"MT","files":[{"file_id":9}]}}]}`))
	})
	var srv *httptest.Server
	mux.HandleFunc("/os/download", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["file_id"] != float64(8) || body["sub_format"] != "webvtt" {
			http.Error(w, `{"message":"bad"}`, 400)
			return
		}
		w.Write([]byte(`{"link":"` + srv.URL + `/file.vtt"}`))
	})
	mux.HandleFunc("/file.vtt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nfrom os\n"))
	})
	mux.HandleFunc("/subdl/subtitles", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api_key") != "sd-key" || q.Get("imdb_id") != "tt0000123" || q.Get("type") != "tv" || q.Get("languages") != "EN,FR" {
			w.Write([]byte(`{"status":false,"error":"bad query"}`))
			return
		}
		w.Write([]byte(`{"status":true,"subtitles":[{"release_name":"Show.S01.Pack","language":"EN","url":"/subtitle/1-2.zip"}]}`))
	})
	mux.HandleFunc("/dl/subtitle/1-2.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zipped.Bytes()) })
	srv = httptest.NewServer(mux)
	defer srv.Close()
	defer func(a, b, c string, f bool) {
		openSubtitlesBase, subDLBase, subDLDownload, followSignInHost = a, b, c, f
	}(openSubtitlesBase, subDLBase, subDLDownload, followSignInHost)
	openSubtitlesBase, subDLBase, subDLDownload, followSignInHost = srv.URL+"/os", srv.URL+"/subdl", srv.URL+"/dl", false

	s := New(func() config.SubtitlesConfig {
		return config.SubtitlesConfig{OpenSubtitles: config.OpenSubtitlesConfig{APIKey: "os-key"}, SubDL: config.SubDLConfig{APIKey: "sd-key"}}
	}, "WrenTV test")
	q := Query{IMDBID: "tt0000123", Season: 1, Episode: 2, Languages: []string{"en", "fr"}}
	rs, err := s.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range rs {
		order = append(order, r.Name)
	}
	// English first, people over machines, then the most downloaded.
	if got := strings.Join(order, ","); got != "Show.S01E02.EN,Show.S01.Pack,MT,Show.S01E02.FR" {
		t.Fatalf("order %s", got)
	}
	vtt, err := s.Fetch(context.Background(), "opensubtitles", "8", q)
	if err != nil || !strings.Contains(string(vtt), "from os") {
		t.Fatalf("opensubtitles fetch %q %v", vtt, err)
	}
	vtt, err = s.Fetch(context.Background(), "subdl", "/subtitle/1-2.zip", q)
	if err != nil || !strings.Contains(string(vtt), "Show.S01E02.srt") || !strings.HasPrefix(string(vtt), "WEBVTT") {
		t.Fatalf("subdl fetch %q %v", vtt, err)
	}
}
