package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Stremio addons with "tv" catalogs list channels too: each catalog item is
// a channel, and its stream is asked for when it's played (addons' links
// often expire). The channels are saved as an M3U file, so they're cached,
// grouped and numbered like any playlist. Other content types are handled by
// the generic addon resources in addon.go; a valid addon with no tv catalogs
// is not an error there.

const (
	stremioMaxChannels = 5000
	stremioMaxPages    = 50
)

// These are manifest facts, not network failures: the addon answered, but it
// cannot supply a browsable Live TV channel list. Keeping them distinct lets
// the admin page explain the difference between an addon with no channel model
// and a provider that is merely unreachable right now.
var (
	ErrNoTVChannels    = errors.New("this addon has no tv channel catalogs")
	ErrUnlistableAddon = errors.New("this addon's live TV catalogs require a search query and cannot be listed automatically")
	// ErrAddonUnreachable covers a DNS, connection or HTTP failure; it is a
	// temporary or network problem, not a statement about the addon's format.
	ErrAddonUnreachable = errors.New("the addon is unavailable")
	// ErrAddonInvalidResponse means the addon answered, but not with the JSON
	// the Stremio protocol calls for.
	ErrAddonInvalidResponse = errors.New("the addon's response was invalid")
)

type stremioMeta struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Poster      string   `json:"poster"`
	Logo        string   `json:"logo"`
	Background  string   `json:"background"`
	ReleaseInfo string   `json:"releaseInfo"`
	Category    string   `json:"category"`
	Genres      []string `json:"genres"`
	Genre       []string `json:"genre"`
}

// providerText bounds a provider description. Matching only needs its leading
// words, and the value never leaves the server.
func providerText(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
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
//
// A provider that is refusing this server for now (429, or 503 with
// Retry-After) is retried at most once, and only when its own wait is short
// enough to sit out inside one request; otherwise the refusal is reported as a
// rate limit. Nothing else is retried: an addon answering the same way twice
// would answer that way a third time, and every repeat is another request the
// provider is counting.
func (s *Service) getJSON(ctx context.Context, u, userAgent string, v any) error {
	category := addonEndpointCategory(u)
	for attempt := 0; ; attempt++ {
		err := s.getJSONOnce(ctx, u, userAgent, v, category, attempt)
		limit, limited := RateLimited(err)
		if !limited || attempt >= addonRetryAttempts {
			return err
		}
		wait := limit.wait(addonRetryCap)
		if wait <= 0 {
			return err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return err
		}
	}
}

// getJSONOnce is one addon request, with its diagnostics. It logs the endpoint
// category, the status, how long it took, which attempt it was and how many
// playback sessions are open — never the link, its query or its credentials.
func (s *Service) getJSONOnce(ctx context.Context, u, userAgent string, v any, category string, attempt int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%w: that isn't an addon link", ErrAddonInvalidResponse)
	}
	req.Header.Set("User-Agent", userAgent)
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		slog.Warn("addon request failed", "endpoint", category, "ms", time.Since(start).Milliseconds(),
			"retry", attempt, "sessions", s.Playing(), "err", err)
		return fmt.Errorf("%w: it didn't answer: %v", ErrAddonUnreachable, err)
	}
	defer resp.Body.Close()
	slog.Debug("addon request", "endpoint", category, "status", resp.StatusCode,
		"ms", time.Since(start).Milliseconds(), "retry", attempt, "sessions", s.Playing())
	if resp.StatusCode >= 400 {
		if limit := addonRateLimit(resp); limit != nil {
			slog.Warn("addon rate limited", "endpoint", category, "status", resp.StatusCode,
				"retryAfter", limit.RetryAfter.String(), "ms", time.Since(start).Milliseconds(),
				"retry", attempt, "sessions", s.Playing())
			return limit
		}
		// A 4xx (apart from a timeout) means the link or its settings are wrong.
		// 5xx and 408 are temporary.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
			return fmt.Errorf("%w: it answered %s", ErrAddonUnreachable, resp.Status)
		}
		return fmt.Errorf("%w: it answered %s", ErrAddonInvalidResponse, resp.Status)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<20)).Decode(v); err != nil {
		return fmt.Errorf("%w: it sent something that isn't Stremio JSON", ErrAddonInvalidResponse)
	}
	return nil
}

// addonEndpointCategory names the part of the Stremio protocol a request
// belongs to: manifest, catalog, meta, stream or other. Only the category is
// logged: the link itself carries the user's addon settings.
func addonEndpointCategory(raw string) string {
	path := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		path = u.Path
	}
	switch {
	case strings.HasSuffix(path, "/manifest.json"):
		return "manifest"
	case strings.Contains(path, "/catalog/"):
		return "catalog"
	case strings.Contains(path, "/meta/"):
		return "meta"
	case strings.Contains(path, "/stream/"):
		return "stream"
	}
	return "other"
}

// StremioChannels lists the channels in an addon's tv catalogs. A manifest
// with no tv catalogs is reported with ErrNoTVChannels so callers that
// explicitly asked for channels can say why; it does not make the manifest
// invalid, which is why AddonCapabilities is the acceptance path.
func (s *Service) StremioChannels(ctx context.Context, src config.LiveTVSource) ([]*Channel, error) {
	m, err := s.fetchAddonManifest(ctx, src, false)
	if err != nil {
		return nil, err
	}
	return s.channelsFromAddon(ctx, src, m)
}

// AddonChannels is the capability-aware name for StremioChannels.
func (s *Service) AddonChannels(ctx context.Context, src config.LiveTVSource) ([]*Channel, error) {
	return s.StremioChannels(ctx, src)
}

// channelsFromAddon lists every tv catalog the manifest declares and can be
// requested without an extra Wren TV can't supply.
func (s *Service) channelsFromAddon(ctx context.Context, src config.LiveTVSource, m *addonManifest) ([]*Channel, error) {
	base := stremioBase(src.URL)
	agent := ua(src.UserAgent)
	specs := m.manifestCatalogs()
	specByKey := make(map[string]config.AddonCatalog, len(specs))
	for _, cat := range specs {
		specByKey[cat.Type+"\x00"+cat.ID] = cat
	}

	var out []*Channel
	seen := map[string]bool{}
	catalogTypes := map[string]bool{}
	tvCatalogs := 0
	listable := 0
	for _, cat := range m.Catalogs {
		kind := strings.ToLower(strings.TrimSpace(cat.Type))
		if kind != "" {
			catalogTypes[kind] = true
		}
		if kind != "tv" {
			continue
		}
		tvCatalogs++
		spec, ok := specByKey[kind+"\x00"+strings.TrimSpace(cat.ID)]
		if !ok {
			continue
		}
		if len(spec.Unsupported) > 0 {
			continue
		}
		// A catalog that needs a search cannot be listed. Genre, skip and
		// date are all meaningful for a channel list: a genre is fetched
		// once per option, skip paginates, and a date marks a native EPG
		// catalog whose channel list is served through metasDetailed.
		genres := []string{""}
		date := ""
		skip := false
		searchOnly := false
		for _, e := range cat.Extra {
			required := e.IsRequired || contains(cat.ExtraRequired, e.Name)
			switch {
			case e.Name == "genre" && len(e.Options) > 0:
				genres = e.Options
			case e.Name == "skip":
				skip = true
			case e.Name == "date":
				date = time.Now().UTC().Format("2006-01-02")
			case e.Name == "search" && required:
				searchOnly = true
			}
		}
		if searchOnly {
			continue
		}
		listable++
		for _, g := range genres {
			for page, from := 0, 0; page < stremioMaxPages && len(out) < stremioMaxChannels; page++ {
				var extra []string
				if date != "" {
					extra = append(extra, "date="+date)
				}
				if g != "" {
					extra = append(extra, "genre="+url.PathEscape(g))
				}
				if from > 0 || skip {
					extra = append(extra, fmt.Sprintf("skip=%d", from))
				}
				u := base + "/catalog/tv/" + url.PathEscape(spec.ID)
				if len(extra) > 0 {
					u += "/" + strings.Join(extra, "&")
				}
				var res struct {
					Metas         []stremioMeta `json:"metas"`
					MetasDetailed []stremioMeta `json:"metasDetailed"`
				}
				if err := s.getJSON(ctx, u+".json", agent, &res); err != nil {
					if len(out) == 0 && page == 0 {
						return nil, err
					}
					break
				}
				items := append(append([]stremioMeta(nil), res.Metas...), res.MetasDetailed...)
				added := 0
				for _, mt := range items {
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
						Metadata: providerText(mt.Description, 400),
						URL:      base + "/stream/tv/" + url.PathEscape(mt.ID) + ".json",
					})
				}
				// Addons that don't page send the same list again.
				if added == 0 {
					break
				}
				from += len(items)
			}
		}
	}
	if tvCatalogs == 0 {
		types := make([]string, 0, len(catalogTypes))
		for t := range catalogTypes {
			types = append(types, t)
		}
		sort.Strings(types)
		if len(types) == 0 {
			return nil, fmt.Errorf("%w: it lists no catalogs", ErrNoTVChannels)
		}
		return nil, fmt.Errorf("%w (it lists %s catalogs instead)", ErrNoTVChannels, strings.Join(types, ", "))
	}
	if listable == 0 {
		return nil, ErrUnlistableAddon
	}
	return out, nil
}

// saveStremio writes an addon's channels to file as a playlist, unless the
// file is younger than maxAge. An addon with no tv catalogs removes any stale
// playlist and returns no error: it is a valid addon in another content model.
func (s *Service) saveStremio(ctx context.Context, src config.LiveTVSource, file string, maxAge time.Duration, force bool) (time.Time, *config.AddonCapabilities, error) {
	m, caps, err := s.discoverAddon(ctx, src)
	if err != nil {
		return time.Time{}, nil, err
	}
	if st, statErr := os.Stat(file); statErr == nil && !force && time.Since(st.ModTime()) < maxAge {
		return st.ModTime(), caps, nil
	}
	chans, err := s.channelsFromAddon(ctx, src, m)
	if errors.Is(err, ErrNoTVChannels) || errors.Is(err, ErrUnlistableAddon) {
		// No channel model: a stale playlist from an earlier manifest would
		// otherwise keep channels the addon no longer offers.
		_ = os.Remove(file)
		return time.Now(), caps, nil
	}
	if err != nil {
		return time.Time{}, caps, err
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
		return time.Time{}, caps, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return time.Time{}, caps, err
	}
	return time.Now(), caps, nil
}

// addonChannelFeeds asks the addon for a channel's feeds and returns them
// best-first. The links are short-lived, so they are asked for on every tune
// instead of being cached with the channel.
func (s *Service) addonChannelFeeds(ctx context.Context, c *Channel) ([]AddonStream, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var res addonStreamResponse
	if err := s.getJSON(ctx, c.URL, c.UserAgent, &res); err != nil {
		return nil, err
	}
	feeds := RankAddonStreams(parseAddonStreams(res, c.UserAgent))
	if len(feeds) == 0 {
		return nil, errors.New("the addon has no stream for this channel right now")
	}
	return feeds, nil
}

// resolveStremio asks the addon for a channel's stream. It prefers the
// best-ranked feed that actually starts, so a dead link or an expired token
// doesn't reach the player while a working stream is on offer. When every feed
// refuses the check it still returns the best one: a provider that only serves
// its own client must not be hidden from the viewer. It also returns the
// addon's allowlisted request headers, because a provider may serve media only
// with a referer or an origin.
func (s *Service) resolveStremio(ctx context.Context, c *Channel) (link, userAgent string, headers map[string]string, err error) {
	feeds, err := s.addonChannelFeeds(ctx, c)
	if err != nil {
		return "", "", nil, err
	}
	chosen := feeds[0]
	for _, feed := range feeds {
		err := s.CheckAddonStream(ctx, feed)
		if err == nil {
			chosen = feed
			break
		}
		// A rate limit applies to every feed from that host, so the remaining
		// checks would only add to it. The best-ranked feed is still returned: a
		// provider that refills its limit may play it, and hiding the channel
		// would be worse than letting the player try.
		if _, limited := RateLimited(err); limited {
			slog.Warn("addon channel rate limited while choosing a feed", "sessions", s.Playing())
			break
		}
	}
	return chosen.URL, chosen.UserAgent, chosen.Headers, nil
}
