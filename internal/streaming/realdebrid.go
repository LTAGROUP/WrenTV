package streaming

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
	"time"
)

// Real-Debrid allows 250 requests a minute.
var realDebridGuard = newGuard("realdebrid", 300*time.Millisecond, time.Minute)

const realDebridAPI = "https://api.real-debrid.com/rest/1.0"

// realDebrid no longer offers a cache check: Add adds the magnet, selects
// its videos and keeps it only if it is downloaded straight away.
type realDebrid struct{ key string }

func newRealDebrid(key string) *realDebrid { return &realDebrid{key: key} }

func (rd *realDebrid) Name() string { return "realdebrid" }

func (rd *realDebrid) do(ctx context.Context, method, endpoint string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, realDebridAPI+endpoint, body)
	req.Header.Set("Authorization", "Bearer "+rd.key)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	name := endpoint
	if i := strings.IndexByte(name[1:], '/'); i > 0 {
		name = name[:i+1]
	}
	b, status, err := apiCall(ctx, realDebridGuard, apiClient, name, req)
	if err != nil {
		if status == http.StatusNotFound {
			return ErrStale
		}
		return err
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type rdInfo struct {
	Status string `json:"status"`
	Files  []struct {
		ID       int    `json:"id"`
		Path     string `json:"path"`
		Bytes    int64  `json:"bytes"`
		Selected int    `json:"selected"`
	} `json:"files"`
	Links []string `json:"links"`
}

func (rd *realDebrid) Check(context.Context, []string) (map[string]bool, bool, error) {
	return nil, false, nil
}

func (rd *realDebrid) Add(ctx context.Context, hash, magnet string) (*Torrent, error) {
	var add struct {
		ID string `json:"id"`
	}
	if err := rd.do(ctx, http.MethodPost, "/torrents/addMagnet", url.Values{"magnet": {magnet}}, &add); err != nil {
		return nil, err
	}
	drop := func(err error) (*Torrent, error) {
		_ = rd.do(context.Background(), http.MethodDelete, "/torrents/delete/"+add.ID, nil, nil)
		return nil, err
	}
	deadline := time.Now().Add(12 * time.Second)
	selected := false
	for {
		var info rdInfo
		if err := rd.do(ctx, http.MethodGet, "/torrents/info/"+add.ID, nil, &info); err != nil {
			return drop(err)
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
					return drop(errors.New("real-debrid: no video files"))
				}
				if err := rd.do(ctx, http.MethodPost, "/torrents/selectFiles/"+add.ID, url.Values{"files": {strings.Join(ids, ",")}}, nil); err != nil {
					return drop(err)
				}
				selected = true
			}
		case "downloaded":
			tor := &Torrent{Ref: add.ID}
			i := 0
			for _, f := range info.Files {
				if f.Selected != 1 {
					continue
				}
				// Links are listed in the order of the selected files.
				if i < len(info.Links) {
					tor.Files = append(tor.Files, RemoteFile{ID: info.Links[i], Name: strings.TrimPrefix(f.Path, "/"), Size: f.Bytes})
				}
				i++
			}
			return tor, nil
		case "queued", "downloading", "magnet_conversion", "compressing", "uploading":
			if selected {
				return drop(ErrNotCached) // it would have to download
			}
		case "error", "magnet_error", "virus", "dead":
			return drop(fmt.Errorf("real-debrid: torrent %s", info.Status))
		}
		if time.Now().After(deadline) {
			return drop(ErrNotCached)
		}
		select {
		case <-ctx.Done():
			return drop(ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func (rd *realDebrid) Link(ctx context.Context, _, fileID string) (string, error) {
	var un struct {
		Download string `json:"download"`
	}
	if err := rd.do(ctx, http.MethodPost, "/unrestrict/link", url.Values{"link": {fileID}}, &un); err != nil {
		return "", err
	}
	if un.Download == "" {
		return "", errors.New("real-debrid: no link returned")
	}
	return un.Download, nil
}
