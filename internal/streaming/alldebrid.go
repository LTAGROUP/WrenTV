package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

var allDebridGuard = newGuard("alldebrid", 100*time.Millisecond, time.Minute)

const allDebridAPI = "https://api.alldebrid.com"

// allDebrid has no cache check that doesn't add the magnet: Add uploads it
// and removes it again when it isn't ready at once.
type allDebrid struct{ key string }

func newAllDebrid(key string) *allDebrid { return &allDebrid{key: key} }

func (a *allDebrid) Name() string { return "alldebrid" }

func (a *allDebrid) call(ctx context.Context, endpoint string, form url.Values) (map[string]any, error) {
	req, _ := http.NewRequest(http.MethodPost, allDebridAPI+endpoint+"?agent=wrentv", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	b, _, err := apiCall(ctx, allDebridGuard, apiClient, endpoint, req)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("alldebrid %s: invalid response", endpoint)
	}
	if s, _ := raw["status"].(string); s == "error" {
		return nil, allDebridErr(raw["error"])
	}
	return raw, nil
}

func allDebridErr(v any) error {
	m, _ := v.(map[string]any)
	code, _ := m["code"].(string)
	msg, _ := m["message"].(string)
	switch code {
	case "LINK_DOWN", "MAGNET_INVALID_ID", "MAGNET_LINKS_REMOVED", "LINK_HOST_NOT_SUPPORTED":
		return fmt.Errorf("%w: alldebrid %s", ErrStale, code)
	case "MAGNET_MUST_BE_PREMIUM", "AUTH_BAD_APIKEY", "AUTH_BLOCKED":
		return fmt.Errorf("alldebrid: %s", msg)
	}
	return fmt.Errorf("alldebrid %s: %s", code, msg)
}

func (a *allDebrid) Check(context.Context, []string) (map[string]bool, bool, error) {
	return nil, false, nil
}

func (a *allDebrid) Add(ctx context.Context, hash, magnet string) (*Torrent, error) {
	raw, err := a.call(ctx, "/v4/magnet/upload", url.Values{"magnets[]": {magnet}})
	if err != nil {
		return nil, err
	}
	data, _ := raw["data"].(map[string]any)
	mags, _ := data["magnets"].([]any)
	if len(mags) == 0 {
		return nil, fmt.Errorf("alldebrid: no magnet returned")
	}
	m, _ := mags[0].(map[string]any)
	if e := m["error"]; e != nil {
		return nil, allDebridErr(e)
	}
	id := idString(m["id"])
	if ready, _ := m["ready"].(bool); !ready {
		_, _ = a.call(context.Background(), "/v4/magnet/delete", url.Values{"id": {id}})
		return nil, ErrNotCached
	}
	raw, err = a.call(ctx, "/v4/magnet/files", url.Values{"id[]": {id}})
	if err != nil {
		return nil, err
	}
	tor := &Torrent{Ref: id}
	data, _ = raw["data"].(map[string]any)
	list, _ := data["magnets"].([]any)
	for _, v := range list {
		mm, _ := v.(map[string]any)
		if idString(mm["id"]) != id {
			continue
		}
		files, _ := mm["files"].([]any)
		walkAllDebrid(files, "", &tor.Files)
	}
	if len(tor.Files) == 0 {
		return nil, ErrNotCached
	}
	return tor, nil
}

// walkAllDebrid flattens AllDebrid's file tree (n=name, s=size, l=link, e=entries).
func walkAllDebrid(nodes []any, parent string, out *[]RemoteFile) {
	for _, v := range nodes {
		m, _ := v.(map[string]any)
		name, _ := m["n"].(string)
		full := path.Join(parent, name)
		if kids, _ := m["e"].([]any); len(kids) > 0 {
			walkAllDebrid(kids, full, out)
			continue
		}
		if l, _ := m["l"].(string); l != "" {
			size, _ := m["s"].(float64)
			*out = append(*out, RemoteFile{ID: l, Name: full, Size: int64(size)})
		}
	}
}

func (a *allDebrid) Link(ctx context.Context, _, fileID string) (string, error) {
	raw, err := a.call(ctx, "/v4/link/unlock", url.Values{"link": {fileID}})
	if err != nil {
		return "", err
	}
	data, _ := raw["data"].(map[string]any)
	if s, _ := data["link"].(string); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("alldebrid: no link returned")
}
