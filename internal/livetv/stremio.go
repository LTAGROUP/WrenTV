package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Stremio addons with "tv" catalogs list channels too: each catalog item is
// a channel, and its stream is asked for when it's played (addons' links
// often expire). The channels are saved as an M3U file, so they're cached,
// grouped and numbered like any playlist.

const (
	stremioMaxChannels = 5000
	stremioMaxPages    = 50
)

type stremioManifest struct {
	Catalogs []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Extra []struct {
			Name       string   `json:"name"`
			IsRequired bool     `json:"isRequired"`
			Options    []string `json:"options"`
		} `json:"extra"`
		ExtraRequired []string `json:"extraRequired"`
	} `json:"catalogs"`
}

type stremioMeta struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Poster string   `json:"poster"`
	Logo   string   `json:"logo"`
	Genres []string `json:"genres"`
	Genre  []string `json:"genre"`
}

// stremioBase is an addon's address from its manifest link.
func stremioBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "stremio://") {
		raw = "https://" + strings.TrimPrefix(raw, "stremio://")
	}
	raw = strings.TrimSuffix(raw, "/")
	return strings.TrimSuffix(raw, "/manifest.json")
}

// getJSON fetches an addon resource. Errors leave the link out: addon links
// carry the user's settings and keys.
func (s *Service) getJSON(ctx context.Context, u, userAgent string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errors.New("that isn't an addon link")
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("the addon didn't answer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("the addon answered %s", resp.Status)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<20)).Decode(v); err != nil {
		return errors.New("the addon sent something that isn't Stremio JSON")
	}
	return nil
}

// StremioChannels lists the channels in an addon's tv catalogs.
func (s *Service) StremioChannels(ctx context.Context, src config.LiveTVSource) ([]*Channel, error) {
	base := stremioBase(src.URL)
	agent := ua(src.UserAgent)
	var m stremioManifest
	if err := s.getJSON(ctx, base+"/manifest.json", agent, &m); err != nil {
		return nil, err
	}
	var out []*Channel
	seen := map[string]bool{}
	tvCatalogs := 0
	for _, cat := range m.Catalogs {
		if cat.Type != "tv" {
			continue
		}
		// Catalogs that need a search can't be listed; ones that need a
		// genre are listed a genre at a time.
		genres := []string{""}
		skip := false
		for _, e := range cat.Extra {
			required := e.IsRequired || contains(cat.ExtraRequired, e.Name)
			switch {
			case !required:
			case e.Name == "genre" && len(e.Options) > 0:
				genres = e.Options
			default:
				skip = true
			}
		}
		if skip {
			continue
		}
		tvCatalogs++
		for _, g := range genres {
			for page, from := 0, 0; page < stremioMaxPages && len(out) < stremioMaxChannels; page++ {
				var extra []string
				if g != "" {
					extra = append(extra, "genre="+url.PathEscape(g))
				}
				if from > 0 {
					extra = append(extra, fmt.Sprintf("skip=%d", from))
				}
				u := base + "/catalog/tv/" + url.PathEscape(cat.ID)
				if len(extra) > 0 {
					u += "/" + strings.Join(extra, "&")
				}
				var res struct {
					Metas []stremioMeta `json:"metas"`
				}
				if err := s.getJSON(ctx, u+".json", agent, &res); err != nil {
					if len(out) == 0 && page == 0 {
						return nil, err
					}
					break
				}
				added := 0
				for _, mt := range res.Metas {
					if mt.ID == "" || mt.Name == "" || seen[mt.ID] {
						continue
					}
					seen[mt.ID] = true
					added++
					group := g
					if group == "" {
						if gs := append(mt.Genres, mt.Genre...); len(gs) > 0 {
							group = gs[0]
						} else {
							group = cat.Name
						}
					}
					logo := mt.Logo
					if logo == "" {
						logo = mt.Poster
					}
					out = append(out, &Channel{
						Name: mt.Name, Logo: logo, Group: group, TvgID: mt.ID,
						URL: base + "/stream/tv/" + url.PathEscape(mt.ID) + ".json",
					})
				}
				// Addons that don't page send the same list again.
				if added == 0 {
					break
				}
				from += len(res.Metas)
			}
		}
	}
	if tvCatalogs == 0 {
		return nil, errors.New("that addon has no TV channel catalogs")
	}
	return out, nil
}

// saveStremio writes an addon's channels to file as a playlist, unless the
// file is younger than maxAge.
func (s *Service) saveStremio(ctx context.Context, src config.LiveTVSource, file string, maxAge time.Duration, force bool) (time.Time, error) {
	old := time.Time{}
	if st, err := os.Stat(file); err == nil {
		if !force && time.Since(st.ModTime()) < maxAge {
			return st.ModTime(), nil
		}
		old = st.ModTime()
	}
	chans, err := s.StremioChannels(ctx, src)
	if err != nil {
		return old, err
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	attr := func(v string) string { return strings.NewReplacer(`"`, "'", "\n", " ").Replace(v) }
	for _, c := range chans {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n%s\n",
			attr(c.TvgID), attr(c.Logo), attr(c.Group), strings.ReplaceAll(c.Name, "\n", " "), c.URL)
	}
	tmp := file + ".part"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return old, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return old, err
	}
	return time.Now(), nil
}

// resolveStremio asks the addon for a channel's stream: the first one that
// plays from a link (not a torrent or a web page).
func (s *Service) resolveStremio(ctx context.Context, c *Channel) (link, userAgent string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var res struct {
		Streams []struct {
			URL           string `json:"url"`
			BehaviorHints struct {
				ProxyHeaders struct {
					Request map[string]string `json:"request"`
				} `json:"proxyHeaders"`
			} `json:"behaviorHints"`
		} `json:"streams"`
	}
	if err := s.getJSON(ctx, c.URL, c.UserAgent, &res); err != nil {
		return "", "", err
	}
	for _, st := range res.Streams {
		if !strings.HasPrefix(st.URL, "http://") && !strings.HasPrefix(st.URL, "https://") {
			continue
		}
		agent := c.UserAgent
		for k, v := range st.BehaviorHints.ProxyHeaders.Request {
			if strings.EqualFold(k, "User-Agent") && v != "" {
				agent = v
			}
		}
		return st.URL, agent, nil
	}
	return "", "", errors.New("the addon has no stream for this channel right now")
}
