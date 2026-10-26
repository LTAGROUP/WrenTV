package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Stremio addons are capability-driven: a manifest declares which content types
// it serves and which of the catalog, meta and stream resources it implements
// for each. Wren TV reads those declarations instead of requiring a particular
// kind of addon, so a manifest with only sport catalogs, only film catalogs or
// no catalogs at all is accepted for what it is. Television channels are one
// model among several: tv catalogs are imported into the channel list, sport
// catalogs are browsed as events, and any other catalog type is browsed through
// the same generic item/stream path.

var (
	// ErrUnsupportedResource: the manifest is valid but does not implement the
	// resource (or the content type) the request needs.
	ErrUnsupportedResource = errors.New("this addon doesn't offer that resource")
	// ErrUnknownCatalog: the addon no longer declares the requested catalog.
	ErrUnknownCatalog = errors.New("the addon doesn't offer that catalog")
	// ErrCatalogUnsupported: the catalog exists but needs extras Wren TV can't
	// supply, such as a required genre without options.
	ErrCatalogUnsupported = errors.New("this catalog needs options Wren TV can't supply")
	// ErrNoStreams: the addon answered but has nothing playable for the item.
	ErrNoStreams = errors.New("the addon has no playable streams for this item right now")
)

// addonResource is one entry of a manifest's resources array. The Stremio
// protocol allows either a bare name ("catalog") or an object declaring which
// content types it serves.
type addonResource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types"`
	IDPrefixes []string `json:"idPrefixes"`
}

func (r *addonResource) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		r.Name = strings.TrimSpace(name)
		return nil
	}
	type plain addonResource
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = addonResource(p)
	r.Name = strings.TrimSpace(r.Name)
	return nil
}

type addonExtra struct {
	Name       string   `json:"name"`
	IsRequired bool     `json:"isRequired"`
	Options    []string `json:"options"`
}

type addonCatalogSpec struct {
	Type          string       `json:"type"`
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Extra         []addonExtra `json:"extra"`
	ExtraRequired []string     `json:"extraRequired"`
}

type addonBehaviorHints struct {
	Adult                 bool `json:"adult"`
	P2P                   bool `json:"p2p"`
	Configurable          bool `json:"configurable"`
	ConfigurationRequired bool `json:"configurationRequired"`
}

// addonManifest is the parts of a Stremio manifest Wren TV acts on.
type addonManifest struct {
	ID            string             `json:"id"`
	Version       string             `json:"version"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Logo          string             `json:"logo"`
	Types         []string           `json:"types"`
	Resources     []addonResource    `json:"resources"`
	Catalogs      []addonCatalogSpec `json:"catalogs"`
	IDPrefixes    []string           `json:"idPrefixes"`
	BehaviorHints addonBehaviorHints `json:"behaviorHints"`
}

// valid reports whether the document looks like a Stremio manifest at all.
// An empty JSON object, an HTML page or a random API answer is invalid; a
// manifest that merely lacks catalogs or channels is valid and stays valid.
func (m *addonManifest) valid() bool {
	if m == nil {
		return false
	}
	return strings.TrimSpace(m.ID) != "" || strings.TrimSpace(m.Name) != "" ||
		len(m.Resources) > 0 || len(m.Catalogs) > 0 || len(m.Types) > 0
}

// serves reports whether the manifest implements resource for contentType.
// A bare resource name applies to every declared content type.
func (m *addonManifest) serves(resource, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	for _, res := range m.Resources {
		if !strings.EqualFold(strings.TrimSpace(res.Name), resource) {
			continue
		}
		if len(res.Types) == 0 {
			return true
		}
		for _, t := range res.Types {
			if strings.EqualFold(strings.TrimSpace(t), contentType) {
				return true
			}
		}
	}
	return false
}

// declaredTypes is every content type named by the manifest: its types array,
// its catalogs and its per-resource types.
func (m *addonManifest) declaredTypes() []string {
	seen := map[string]bool{}
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			seen[t] = true
		}
	}
	for _, t := range m.Types {
		add(t)
	}
	for _, cat := range m.Catalogs {
		add(cat.Type)
	}
	for _, res := range m.Resources {
		for _, t := range res.Types {
			add(t)
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// resourceNames is the set of declared resource names, for the capabilities.
func (m *addonManifest) resourceNames() []string {
	seen := map[string]bool{}
	for _, res := range m.Resources {
		name := strings.ToLower(strings.TrimSpace(res.Name))
		if name != "" {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// manifestCatalogs analyses every declared catalog. A catalog is only usable
// when the addon declares the catalog resource for its type and every required
// extra is one Wren TV can supply.
func (m *addonManifest) manifestCatalogs() []config.AddonCatalog {
	out := make([]config.AddonCatalog, 0, len(m.Catalogs))
	seen := map[string]bool{}
	for _, cat := range m.Catalogs {
		contentType := strings.ToLower(strings.TrimSpace(cat.Type))
		id := strings.TrimSpace(cat.ID)
		if contentType == "" || id == "" {
			continue
		}
		key := contentType + "\x00" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		name := strings.TrimSpace(cat.Name)
		if name == "" {
			name = id
		}
		entry := config.AddonCatalog{Type: contentType, ID: id, Name: name}
		searchRequired := false
		for _, extra := range cat.Extra {
			name := strings.ToLower(strings.TrimSpace(extra.Name))
			required := extra.IsRequired || contains(cat.ExtraRequired, extra.Name)
			switch name {
			case "search":
				entry.Searchable = true
				if required {
					searchRequired = true
				}
			case "skip":
				entry.Skip = true
			case "date":
				// Today's date is a valid value for native EPG catalogs.
			case "genre":
				if required && len(extra.Options) == 0 {
					entry.Unsupported = append(entry.Unsupported, "genre")
				}
			default:
				if required {
					entry.Unsupported = append(entry.Unsupported, name)
				}
			}
		}
		entry.Unsupported = normalizeStrings(entry.Unsupported)
		entry.Browsable = m.serves("catalog", contentType) && len(entry.Unsupported) == 0 && !searchRequired
		out = append(out, entry)
	}
	return out
}

// capabilities turns a manifest into the stored description of what the addon
// can do. It never fails on a valid manifest: limitations are recorded for the
// settings page, and an addon with no channels, no catalogs or no streams is
// still a valid addon.
func (m *addonManifest) capabilities() *config.AddonCapabilities {
	caps := &config.AddonCapabilities{
		ID: strings.TrimSpace(m.ID), Name: strings.TrimSpace(m.Name),
		Version: strings.TrimSpace(m.Version), Description: providerText(m.Description, 500),
		Logo: strings.TrimSpace(m.Logo), Types: m.declaredTypes(),
		Resources: m.resourceNames(), Catalogs: m.manifestCatalogs(),
		ConfigurationRequired: m.BehaviorHints.ConfigurationRequired,
		Checked:               time.Now().UTC(),
	}
	if caps.Types == nil {
		caps.Types = []string{}
	}
	if caps.Resources == nil {
		caps.Resources = []string{}
	}
	if caps.Catalogs == nil {
		caps.Catalogs = []config.AddonCatalog{}
	}

	var limitations []string
	if caps.ConfigurationRequired {
		limitations = append(limitations, "The addon says it must be configured first: install the manifest link your configuration produced.")
	}
	hasCatalogResource := false
	hasStreamResource := false
	for _, res := range m.Resources {
		switch {
		case strings.EqualFold(strings.TrimSpace(res.Name), "catalog"):
			hasCatalogResource = true
		case strings.EqualFold(strings.TrimSpace(res.Name), "stream"):
			hasStreamResource = true
		}
	}
	browsable := 0
	searchOnly := 0
	unsupported := 0
	for _, cat := range caps.Catalogs {
		switch {
		case len(cat.Unsupported) > 0:
			unsupported++
		case cat.Browsable:
			browsable++
		case cat.Searchable:
			searchOnly++
		}
	}
	if len(caps.Catalogs) == 0 {
		if hasCatalogResource {
			limitations = append(limitations, "It declares no catalogs, so there is nothing to browse.")
		} else {
			limitations = append(limitations, "It doesn't offer catalogs, so there is nothing to browse.")
		}
	} else if !hasCatalogResource {
		limitations = append(limitations, "It doesn't implement the catalog resource, so its catalogs can't be listed.")
	}
	if unsupported > 0 {
		limitations = append(limitations, fmt.Sprintf("%d catalog%s need options Wren TV can't supply.", unsupported, pluralS(unsupported)))
	}
	if searchOnly > 0 {
		limitations = append(limitations, fmt.Sprintf("%d catalog%s only answer a search query.", searchOnly, pluralS(searchOnly)))
	}
	if !hasStreamResource {
		limitations = append(limitations, "It doesn't implement the stream resource, so nothing can be played.")
	}

	// One model per declared content type. A type can be browsable and not
	// playable (metadata-only), or playable and not browsable (requires ids).
	models := make([]config.AddonTypeModel, 0, len(caps.Types))
	for _, contentType := range caps.Types {
		model := config.AddonTypeModel{
			Type: contentType, Catalog: m.serves("catalog", contentType),
			Meta: m.serves("meta", contentType), Stream: m.serves("stream", contentType),
		}
		for _, cat := range caps.Catalogs {
			if cat.Type == contentType {
				model.Catalogs++
			}
		}
		switch contentType {
		case "tv":
			model.Model = "channels"
		case "sport":
			model.Model = "events"
		default:
			model.Model = "catalog"
		}
		if model.Stream {
			caps.Playable = true
		}
		if model.Catalogs > 0 && !model.Stream && hasStreamResource {
			limitations = append(limitations, fmt.Sprintf("Its %s content can be browsed but not played: the addon declares no stream resource for %s.", contentType, contentType))
		}
		models = append(models, model)
	}
	caps.Models = models
	caps.Browsable = browsable > 0 || searchOnly > 0
	if !caps.Browsable && caps.Playable {
		limitations = append(limitations, "It has no browsable catalogs: items can only be opened by id, which Wren TV can't discover on its own.")
	}
	if !caps.Browsable && !caps.Playable {
		limitations = append(limitations, "Nothing can be browsed or played with this addon yet, but the manifest itself is valid.")
	}
	if len(caps.Catalogs) > 0 && !caps.HasTVChannels() {
		limitations = append(limitations, "It lists no TV channel catalogs, so it doesn't add channels: its content is browsed as catalog items or events.")
	}
	caps.Limitations = limitations
	return caps
}

// addonManifestTTL is how long an addon's manifest is reused. One viewer
// action asks for the same manifest several times — open an item (metadata and
// streams), then automatic playback resolving and proving each feed — and a
// provider that counts requests should be asked once. A manifest changes when
// the addon's configuration link changes, which is a different cache key, and
// saving settings or an admin check reads it fresh anyway.
const addonManifestTTL = 5 * time.Minute

// addonManifestWait bounds one manifest fetch. A hanging addon must not hold the
// entry's lock — and every request queued behind it — indefinitely.
const addonManifestWait = 20 * time.Second

// addonManifestEntry is one addon's cached manifest. Its mutex makes concurrent
// callers of the same addon share a single fetch instead of requesting the
// manifest twice at once.
//
// A manifest is only ever stored after it was read and looked valid: a failure
// is never cached, so an addon that was down for a moment is asked again rather
// than remembered as broken.
type addonManifestEntry struct {
	mu       sync.Mutex
	manifest *addonManifest
	at       time.Time
}

// AddonCapabilities fetches an addon's manifest and describes what it can do.
// The manifest is always read rather than served from the short-lived cache:
// this is the "what does the addon offer now" answer the settings page, the
// admin Check and the browse routes need.
func (s *Service) AddonCapabilities(ctx context.Context, src config.LiveTVSource) (*config.AddonCapabilities, error) {
	manifest, err := s.fetchAddonManifest(ctx, src, true)
	if err != nil {
		return nil, err
	}
	return manifest.capabilities(), nil
}

// discoverAddon fetches and analyses the manifest once, so callers that need
// both the manifest and its capabilities don't request it twice. This is the
// resource path (catalogs, metadata, streams), which reuses the manifest the
// same viewer action already read.
func (s *Service) discoverAddon(ctx context.Context, src config.LiveTVSource) (*addonManifest, *config.AddonCapabilities, error) {
	manifest, err := s.fetchAddonManifest(ctx, src, false)
	if err != nil {
		return nil, nil, err
	}
	return manifest, manifest.capabilities(), nil
}

// fetchAddonManifest reads an addon's manifest, from the cache unless [fresh]
// asks for the addon's answer right now. The cached copy is refreshed by
// whichever call read it last.
func (s *Service) fetchAddonManifest(ctx context.Context, src config.LiveTVSource, fresh bool) (*addonManifest, error) {
	base, agent := stremioBase(src.URL), ua(src.UserAgent)
	entry := s.addonManifestEntry(base + "\x00" + agent)
	// Held across the fetch: a second caller for the same addon waits for it and
	// then finds the manifest already read, instead of issuing its own request.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !fresh && entry.manifest != nil && time.Since(entry.at) < addonManifestTTL {
		return entry.manifest, nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, addonManifestWait)
	defer cancel()
	var m addonManifest
	if err := s.getJSON(fetchCtx, base+"/manifest.json", agent, &m); err != nil {
		return nil, err
	}
	if !m.valid() {
		return nil, fmt.Errorf("%w: it answered, but not with a Stremio manifest", ErrAddonInvalidResponse)
	}
	entry.manifest, entry.at = &m, time.Now()
	return &m, nil
}

// addonManifestEntry returns one addon's cache slot, creating it on first use.
func (s *Service) addonManifestEntry(key string) *addonManifestEntry {
	s.addonMu.Lock()
	defer s.addonMu.Unlock()
	if s.addonCache == nil {
		s.addonCache = map[string]*addonManifestEntry{}
	}
	entry := s.addonCache[key]
	if entry == nil {
		entry = &addonManifestEntry{}
		s.addonCache[key] = entry
	}
	return entry
}

// ForgetAddonManifests drops the cached manifests, so the next call reads each
// addon's manifest again. A forced refresh and a saved settings change use it:
// the addon may have been reconfigured behind the same link.
func (s *Service) ForgetAddonManifests() {
	s.addonMu.Lock()
	defer s.addonMu.Unlock()
	s.addonCache = nil
}

// AddonCatalogsOf lists the usable catalogs of one content type, including
// catalogs that need a search before they list anything.
func (s *Service) AddonCatalogsOf(ctx context.Context, src config.LiveTVSource, contentType string) ([]config.AddonCatalog, error) {
	_, caps, err := s.discoverAddon(ctx, src)
	if err != nil {
		return nil, err
	}
	return catalogsOf(caps, contentType), nil
}

func catalogsOf(caps *config.AddonCapabilities, contentType string) []config.AddonCatalog {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	out := make([]config.AddonCatalog, 0, len(caps.Catalogs))
	for _, cat := range caps.Catalogs {
		if contentType != "" && cat.Type != contentType {
			continue
		}
		out = append(out, cat)
	}
	return out
}

// findCatalog returns one declared catalog.
func findCatalog(caps *config.AddonCapabilities, contentType, catalogID string) (config.AddonCatalog, bool) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	catalogID = strings.TrimSpace(catalogID)
	for _, cat := range caps.Catalogs {
		if cat.Type == contentType && cat.ID == catalogID {
			return cat, true
		}
	}
	return config.AddonCatalog{}, false
}

// AddonItems lists one catalog. The catalog's declared content type is used in
// the request path, its search extra is sent only when the catalog declares
// one, and its skip extra only when it declares pagination.
func (s *Service) AddonItems(ctx context.Context, src config.LiveTVSource, contentType, catalogID, search string, skip int) ([]AddonItem, error) {
	_, caps, err := s.discoverAddon(ctx, src)
	if err != nil {
		return nil, err
	}
	return s.AddonCatalogItems(ctx, src, caps, contentType, catalogID, search, skip)
}

// AddonCatalogItems reads one catalog from capabilities that were already
// discovered. A provider-wide search over many catalogs uses this so it does
// not fetch the same manifest once per catalog.
func (s *Service) AddonCatalogItems(ctx context.Context, src config.LiveTVSource, caps *config.AddonCapabilities, contentType, catalogID, search string, skip int) ([]AddonItem, error) {
	cat, ok := findCatalog(caps, contentType, catalogID)
	if !ok {
		return nil, ErrUnknownCatalog
	}
	if len(cat.Unsupported) > 0 {
		return nil, fmt.Errorf("%w (it needs %s)", ErrCatalogUnsupported, strings.Join(cat.Unsupported, ", "))
	}
	search = strings.TrimSpace(search)
	if search == "" && cat.Searchable && !cat.Browsable {
		return nil, fmt.Errorf("%w (it only lists items for a search query)", ErrCatalogUnsupported)
	}
	if search != "" && !cat.Searchable {
		// A harmless query on a catalog that doesn't declare search is
		// filtered locally rather than sent as an unsupported extra.
		return s.filterCatalogItems(ctx, src, cat, search, skip)
	}
	endpoint := stremioBase(src.URL) + "/catalog/" + url.PathEscape(cat.Type) + "/" + url.PathEscape(cat.ID)
	var extra []string
	if search != "" {
		extra = append(extra, "search="+url.PathEscape(search))
	}
	if skip > 0 && cat.Skip {
		extra = append(extra, fmt.Sprintf("skip=%d", skip))
	}
	if len(extra) > 0 {
		endpoint += "/" + strings.Join(extra, "&")
	}
	items, err := s.catalogItems(ctx, src, endpoint, cat.Type)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// filterCatalogItems reads a catalog without extras and matches the query
// locally. It is what a search does when the catalog doesn't declare search.
func (s *Service) filterCatalogItems(ctx context.Context, src config.LiveTVSource, cat config.AddonCatalog, query string, skip int) ([]AddonItem, error) {
	endpoint := stremioBase(src.URL) + "/catalog/" + url.PathEscape(cat.Type) + "/" + url.PathEscape(cat.ID)
	var extra []string
	if skip > 0 && cat.Skip {
		extra = append(extra, fmt.Sprintf("skip=%d", skip))
	}
	if len(extra) > 0 {
		endpoint += "/" + strings.Join(extra, "&")
	}
	items, err := s.catalogItems(ctx, src, endpoint, cat.Type)
	if err != nil {
		return nil, err
	}
	// A catalog without a search extra is filtered locally by its words, not by
	// the exact phrase: a provider naming the same event differently still
	// matches, while a single shared word does not.
	tokens := make([]string, 0, 8)
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if len(token) >= 2 {
			tokens = append(tokens, token)
		}
	}
	out := make([]AddonItem, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.Name, item.Description, item.Group}, " "))
		matched := len(tokens) > 0
		for _, token := range tokens {
			if !strings.Contains(haystack, token) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, item)
		}
	}
	return out, nil
}

// catalogItems decodes both response shapes the protocol allows: metas, and
// metasDetailed for catalogs that carry their own metadata.
func (s *Service) catalogItems(ctx context.Context, src config.LiveTVSource, endpoint, contentType string) ([]AddonItem, error) {
	var payload struct {
		Metas         []stremioMeta `json:"metas"`
		MetasDetailed []stremioMeta `json:"metasDetailed"`
	}
	if err := s.getJSON(ctx, endpoint+".json", ua(src.UserAgent), &payload); err != nil {
		return nil, err
	}
	metas := append(append([]stremioMeta(nil), payload.Metas...), payload.MetasDetailed...)
	out := make([]AddonItem, 0, len(metas))
	seen := map[string]bool{}
	for _, meta := range metas {
		id := strings.TrimSpace(meta.ID)
		name := strings.TrimSpace(meta.Name)
		if id == "" || name == "" || seen[id] || len(out) >= 200 {
			continue
		}
		seen[id] = true
		item := AddonItem{
			Type: contentType, ID: id, Name: name, Description: providerText(meta.Description, 500),
			Poster: meta.Poster, Background: meta.Background,
			ReleaseInfo: strings.TrimSpace(meta.ReleaseInfo), Category: strings.TrimSpace(meta.Category),
		}
		if genres := append(append([]string(nil), meta.Genres...), meta.Genre...); len(genres) > 0 {
			item.Group = strings.TrimSpace(genres[0])
		}
		out = append(out, item)
	}
	return out, nil
}

// AddonMeta fetches an item's metadata. It is optional for browsing but gives
// the detail view the addon intends.
func (s *Service) AddonMeta(ctx context.Context, src config.LiveTVSource, contentType, itemID string) (*AddonEvent, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, errors.New("item id is required")
	}
	m, err := s.fetchAddonManifest(ctx, src, false)
	if err != nil {
		return nil, err
	}
	if !m.serves("meta", contentType) {
		return nil, fmt.Errorf("%w: it declares no meta resource for %s", ErrUnsupportedResource, contentType)
	}
	var payload struct {
		Meta stremioMeta `json:"meta"`
	}
	endpoint := stremioBase(src.URL) + "/meta/" + url.PathEscape(contentType) + "/" + url.PathEscape(itemID) + ".json"
	if err := s.getJSON(ctx, endpoint, ua(src.UserAgent), &payload); err != nil {
		return nil, err
	}
	meta := payload.Meta
	if strings.TrimSpace(meta.ID) == "" {
		meta.ID = itemID
	}
	return &AddonEvent{
		Type: contentType, ID: meta.ID, Name: strings.TrimSpace(meta.Name),
		Description: providerText(meta.Description, 1000), Poster: meta.Poster, Background: meta.Background,
		Genres:      append(append([]string(nil), meta.Genres...), meta.Genre...),
		ReleaseInfo: strings.TrimSpace(meta.ReleaseInfo), Category: strings.TrimSpace(meta.Category),
	}, nil
}

// AddonStreams asks the addon for an item's playable feeds. Sports addons
// commonly include paid-plan and "use an external player" entries in the same
// streams array; those are placeholders, not media, and are omitted. The
// remaining URLs are ordinary HLS/MP4 links the existing Live TV proxy plays.
func (s *Service) AddonStreams(ctx context.Context, src config.LiveTVSource, contentType, itemID string) ([]AddonStream, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, errors.New("item id is required")
	}
	m, err := s.fetchAddonManifest(ctx, src, false)
	if err != nil {
		return nil, err
	}
	if !m.serves("stream", contentType) {
		return nil, fmt.Errorf("%w: it declares no stream resource for %s", ErrUnsupportedResource, contentType)
	}
	var payload addonStreamResponse
	endpoint := stremioBase(src.URL) + "/stream/" + url.PathEscape(contentType) + "/" + url.PathEscape(itemID) + ".json"
	if err := s.getJSON(ctx, endpoint, ua(src.UserAgent), &payload); err != nil {
		return nil, err
	}
	out := parseAddonStreams(payload, src.UserAgent)
	if len(out) == 0 {
		return nil, ErrNoStreams
	}
	return out, nil
}

// parseAddonStreams turns the stream resource's answer into playable feeds.
// Placeholders, torrents, malformed links and duplicates are dropped here, so
// browsing, manual playback and automatic playback all start from one list.
// The feeds keep the addon's own order; callers that choose for the viewer sort
// them with RankAddonStreams.
func parseAddonStreams(payload addonStreamResponse, defaultAgent string) []AddonStream {
	out := make([]AddonStream, 0, len(payload.Streams))
	seen := map[string]bool{}
	for _, stream := range payload.Streams {
		link := strings.TrimSpace(stream.URL)
		name := providerText(stream.Name, 200)
		title := providerText(stream.Title, 240)
		if !playableAddonURL(link, name+" "+title) || seen[link] {
			continue
		}
		supplied := map[string]string{}
		for key, value := range stream.BehaviorHints.ProxyHeaders.Request {
			supplied[key] = value
		}
		for key, value := range stream.Headers {
			supplied[key] = value
		}
		headers := SanitizeRequestHeaders(supplied)
		agent := ua(defaultAgent)
		if value := headers["User-Agent"]; value != "" {
			agent = value
		}
		seen[link] = true
		out = append(out, AddonStream{
			Name: name, Title: title, URL: link, UserAgent: agent, Headers: headers,
			// The addon sometimes reports the size separately from the title.
			Quality: addonHeightFromText(name + " " + title + " " + stream.BehaviorHints.VideoSize + " " + stream.BehaviorHints.Filename),
		})
	}
	return out
}

// addonStreamResponse is the stream resource's response shape.
type addonStreamResponse struct {
	Streams []struct {
		URL           string            `json:"url"`
		Name          string            `json:"name"`
		Title         string            `json:"title"`
		Headers       map[string]string `json:"headers"`
		BehaviorHints struct {
			VideoSize    string `json:"videoSize"`
			Filename     string `json:"filename"`
			ProxyHeaders struct {
				Request map[string]string `json:"request"`
			} `json:"proxyHeaders"`
		} `json:"behaviorHints"`
	} `json:"streams"`
}

// playableAddonURL rejects the non-media entries addons use for paid upgrades
// and player tips. It does not require a file extension: providers are free to
// use extensionless media URLs.
func playableAddonURL(raw, label string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "google.com" || strings.HasSuffix(host, ".google.com") {
		return false
	}
	if strings.EqualFold(strings.TrimSuffix(u.Path, "/"), "/health") {
		return false
	}
	label = strings.ToLower(label)
	// These are messages, not feeds: a paid plan, a player tip or an outage
	// notice dressed up as a stream. They are skipped so automatic playback
	// never wastes an attempt on one.
	for _, marker := range []string{
		"upgrade to watch", "upgrade your plan", "premium only", "premium required",
		"requires premium", "subscribe to watch", "sign in to watch", "log in to watch",
		"use an external player", "external player", "streams unavailable",
		"streams temporarily unavailable", "not available in your region",
		"unavailable in your region", "no stream available",
	} {
		if strings.Contains(label, marker) {
			return false
		}
	}
	return true
}

func normalizeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// addonBrowseNote says, in one line, how a valid addon that has no channels is
// used. It is shown beside the source instead of an error.
func addonBrowseNote(caps *config.AddonCapabilities) string {
	if caps == nil {
		return ""
	}
	var parts []string
	for _, model := range caps.Models {
		if model.Catalogs > 0 && model.Model != "channels" {
			parts = append(parts, model.Type)
		}
	}
	switch {
	case len(parts) > 0:
		return "its " + strings.Join(parts, ", ") + " items appear in the Live TV list"
	case !caps.Browsable:
		return "the addon has nothing to list right now"
	}
	return "its catalog items appear in the Live TV list"
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
