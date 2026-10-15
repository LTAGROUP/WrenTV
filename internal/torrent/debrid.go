package torrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Debrid resolves a magnet to direct HTTPS links via a debrid service.
// Real-Debrid is implemented; others can satisfy the same interface.
type Debrid interface {
	Name() string
	// Resolve adds the magnet and returns its files once the service has them
	// cached. It fails fast (ErrNotCached) rather than waiting for a download.
	Resolve(ctx context.Context, magnet string, wait time.Duration) (*DebridTorrent, error)
	// Link turns a file into a fresh direct URL (links expire).
	Link(ctx context.Context, ref string, fileIdx int) (string, error)
	Delete(ctx context.Context, ref string) error
}

var ErrNotCached = errors.New("not cached by debrid service")

type DebridTorrent struct {
	Ref   string
	Name  string
	Files []DebridFile
}

type DebridFile struct {
	Index int // index into the debrid torrent's link list
	Path  string
	Size  int64
}

type realDebrid struct {
	key  string
	http *http.Client

	mu    sync.Mutex
	links map[string]cachedLink
}

type cachedLink struct {
	url string
	at  time.Time
}

func NewRealDebrid(apiKey string) Debrid {
	return &realDebrid{key: apiKey, http: &http.Client{Timeout: 20 * time.Second}, links: map[string]cachedLink{}}
}

func (rd *realDebrid) Name() string { return "realdebrid" }

const rdBase = "https://api.real-debrid.com/rest/1.0"

func (rd *realDebrid) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rdBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+rd.key)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := rd.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return fmt.Errorf("real-debrid %s %s: %s %s", method, path, resp.Status, e.Error)
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type rdInfo struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Files    []struct {
		ID       int    `json:"id"`
		Path     string `json:"path"`
		Bytes    int64  `json:"bytes"`
		Selected int    `json:"selected"`
	} `json:"files"`
	Links []string `json:"links"`
}

func (rd *realDebrid) Resolve(ctx context.Context, magnet string, wait time.Duration) (*DebridTorrent, error) {
	var add struct {
		ID string `json:"id"`
	}
	if err := rd.do(ctx, http.MethodPost, "/torrents/addMagnet", url.Values{"magnet": {magnet}}, &add); err != nil {
		return nil, err
	}
	fail := func(err error) (*DebridTorrent, error) {
		_ = rd.Delete(context.Background(), add.ID)
		return nil, err
	}
	deadline := time.Now().Add(max(wait, 15*time.Second))
	selected := false
	for {
		var info rdInfo
		if err := rd.do(ctx, http.MethodGet, "/torrents/info/"+add.ID, nil, &info); err != nil {
			return fail(err)
		}
		switch info.Status {
		case "waiting_files_selection":
			if !selected {
				var ids []string
				for _, f := range info.Files {
					if isVideoName(f.Path) {
						ids = append(ids, strconv.Itoa(f.ID))
					}
				}
				if len(ids) == 0 {
					return fail(errors.New("torrent contains no video files"))
				}
				if err := rd.do(ctx, http.MethodPost, "/torrents/selectFiles/"+add.ID, url.Values{"files": {strings.Join(ids, ",")}}, nil); err != nil {
					return fail(err)
				}
				selected = true
			}
		case "downloaded":
			dt := &DebridTorrent{Ref: add.ID, Name: info.Filename}
			i := 0
			for _, f := range info.Files {
				if f.Selected != 1 {
					continue
				}
				// Links are listed in the order of selected files.
				if i < len(info.Links) {
					dt.Files = append(dt.Files, DebridFile{Index: i, Path: strings.TrimPrefix(f.Path, "/"), Size: f.Bytes})
				}
				i++
			}
			return dt, nil
		case "error", "magnet_error", "virus", "dead":
			return fail(fmt.Errorf("real-debrid status %s", info.Status))
		}
		if time.Now().After(deadline) {
			return fail(ErrNotCached)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func (rd *realDebrid) Link(ctx context.Context, ref string, fileIdx int) (string, error) {
	key := ref + "/" + strconv.Itoa(fileIdx)
	rd.mu.Lock()
	if c, ok := rd.links[key]; ok && time.Since(c.at) < 2*time.Hour {
		rd.mu.Unlock()
		return c.url, nil
	}
	rd.mu.Unlock()
	var info rdInfo
	if err := rd.do(ctx, http.MethodGet, "/torrents/info/"+ref, nil, &info); err != nil {
		return "", err
	}
	if fileIdx >= len(info.Links) {
		return "", errors.New("debrid link missing")
	}
	var un struct {
		Download string `json:"download"`
	}
	if err := rd.do(ctx, http.MethodPost, "/unrestrict/link", url.Values{"link": {info.Links[fileIdx]}}, &un); err != nil {
		return "", err
	}
	rd.mu.Lock()
	rd.links[key] = cachedLink{url: un.Download, at: time.Now()}
	rd.mu.Unlock()
	return un.Download, nil
}

func (rd *realDebrid) Delete(ctx context.Context, ref string) error {
	return rd.do(ctx, http.MethodDelete, "/torrents/delete/"+ref, nil, nil)
}

func isVideoName(p string) bool {
	p = strings.ToLower(p)
	for _, e := range []string{".mkv", ".mp4", ".m4v", ".mov", ".avi", ".ts", ".m2ts", ".webm"} {
		if strings.HasSuffix(p, e) {
			return !strings.Contains(p, "sample")
		}
	}
	return false
}
