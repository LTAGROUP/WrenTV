package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TorBox allows 300 requests a minute per endpoint; stay well under it.
// Guards are shared by every TorBox client (clients are rebuilt when keys change).
var torboxGuard = newGuard("torbox", 250*time.Millisecond, time.Minute)

const torboxAPI = "https://api.torbox.app/v1/api"

type torBox struct{ key string }

func newTorBox(key string) *torBox { return &torBox{key: key} }

func (t *torBox) Name() string { return "torbox" }

func (t *torBox) call(ctx context.Context, endpoint string, req *http.Request) (map[string]any, error) {
	req.Header.Set("Authorization", "Bearer "+t.key)
	b, _, err := apiCall(ctx, torboxGuard, apiClient, endpoint, req)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("torbox %s: invalid response", endpoint)
	}
	if ok, _ := raw["success"].(bool); !ok {
		return raw, fmt.Errorf("torbox %s: %s", endpoint, apiMessage(b))
	}
	return raw, nil
}

func (t *torBox) Check(ctx context.Context, hashes []string) (map[string]bool, bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(hashes); start += 50 {
		chunk := hashes[start:min(start+50, len(hashes))]
		req, _ := http.NewRequest(http.MethodGet, torboxAPI+"/torrents/checkcached?format=object&hash="+url.QueryEscape(strings.Join(chunk, ",")), nil)
		raw, err := t.call(ctx, "checkcached", req)
		if err != nil {
			return out, true, err
		}
		data, _ := raw["data"].(map[string]any)
		for h, v := range data {
			if m, _ := v.(map[string]any); len(m) > 0 {
				out[strings.ToLower(h)] = true
			}
		}
	}
	return out, true, nil
}

func (t *torBox) Add(ctx context.Context, hash, magnet string) (*Torrent, error) {
	if id := t.findByHash(ctx, hash); id != "" {
		if tor, err := t.files(ctx, id); err == nil {
			return tor, nil
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("magnet", magnet)
	_ = mw.WriteField("seed", "3") // never seed
	_ = mw.WriteField("allow_zip", "false")
	_ = mw.WriteField("add_only_if_cached", "true")
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, torboxAPI+"/torrents/createtorrent", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	raw, err := t.call(ctx, "createtorrent", req)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cached") {
			return nil, ErrNotCached
		}
		return nil, err
	}
	data, _ := raw["data"].(map[string]any)
	id := idString(data["torrent_id"])
	if id == "" {
		return nil, fmt.Errorf("torbox: no torrent id")
	}
	// Cached torrents finish at once; give the listing a moment to catch up.
	deadline := time.Now().Add(20 * time.Second)
	for {
		tor, err := t.files(ctx, id)
		if err == nil {
			return tor, nil
		}
		if err != errNotReady || time.Now().After(deadline) {
			return nil, ErrNotCached
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

var errNotReady = fmt.Errorf("torbox: not ready")

func (t *torBox) files(ctx context.Context, id string) (*Torrent, error) {
	req, _ := http.NewRequest(http.MethodGet, torboxAPI+"/torrents/mylist?bypass_cache=true&id="+url.QueryEscape(id), nil)
	raw, err := t.call(ctx, "mylist", req)
	if err != nil {
		return nil, err
	}
	m, _ := raw["data"].(map[string]any)
	if m == nil {
		if a, _ := raw["data"].([]any); len(a) > 0 {
			m, _ = a[0].(map[string]any)
		}
	}
	if m == nil {
		return nil, ErrStale
	}
	finished, _ := m["download_finished"].(bool)
	present, _ := m["download_present"].(bool)
	if !finished || !present {
		return nil, errNotReady
	}
	tor := &Torrent{Ref: id}
	files, _ := m["files"].([]any)
	for _, v := range files {
		f, _ := v.(map[string]any)
		name, _ := f["name"].(string)
		size, _ := f["size"].(float64)
		tor.Files = append(tor.Files, RemoteFile{ID: idString(f["id"]), Name: name, Size: int64(size)})
	}
	return tor, nil
}

// findByHash returns the account's torrent with this hash, if already added.
func (t *torBox) findByHash(ctx context.Context, hash string) string {
	req, _ := http.NewRequest(http.MethodGet, torboxAPI+"/torrents/mylist?limit=1000", nil)
	raw, err := t.call(ctx, "mylist", req)
	if err != nil {
		return ""
	}
	list, _ := raw["data"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		if h, _ := m["hash"].(string); strings.EqualFold(h, hash) {
			return idString(m["id"])
		}
	}
	return ""
}

func (t *torBox) Link(ctx context.Context, ref, fileID string) (string, error) {
	v := url.Values{"token": {t.key}, "torrent_id": {ref}, "file_id": {fileID}, "redirect": {"true"}}
	req, _ := http.NewRequest(http.MethodGet, torboxAPI+"/torrents/requestdl?"+v.Encode(), nil)
	if err := torboxGuard.wait(ctx, "requestdl"); err != nil {
		return "", err
	}
	c := *apiClient
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("torbox requestdl: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "http") {
			return loc, nil
		}
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", torboxGuard.block(retryAfter(resp.Header.Get("Retry-After")))
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return "", ErrStale
	}
	var raw map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if s, _ := raw["data"].(string); strings.HasPrefix(s, "http") {
		return s, nil
	}
	if msg, _ := raw["detail"].(string); strings.Contains(strings.ToLower(msg), "not found") || strings.Contains(strings.ToLower(msg), "does not exist") {
		return "", ErrStale
	}
	return "", fmt.Errorf("torbox requestdl: %s", resp.Status)
}

func idString(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		return x
	}
	return ""
}
