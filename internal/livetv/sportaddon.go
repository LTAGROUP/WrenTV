package livetv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Sports-event addons are one content model among several. A Stremio addon
// with sport catalogs lists events (matchups, race weekends, tournament
// sessions); each event's streams are requested by event id. This file keeps
// the sports vocabulary the viewer UI uses and delegates the protocol work to
// the generic addon resources, so a sports addon is browsed exactly like any
// other catalog type without being imported as IPTV channels or XMLTV.

var (
	ErrNotSportAddon = errors.New("this addon has no sport catalogs to browse")
	// The generic errors keep their sports names for callers that predate the
	// generic path; they are the same values, so errors.Is matches both.
	ErrSportCatalogMissing = ErrUnknownCatalog
	ErrNoSportStreams      = ErrNoStreams
)

// SportSource is a configured addon as the Live TV browser sees it.
type SportSource struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Enabled      bool                      `json:"enabled"`
	Capabilities *config.AddonCapabilities `json:"capabilities,omitempty"`
}

// AddonCatalogView is one browsable shelf. It names its content type so the
// same UI can browse sport events, films or any other declared catalog.
type AddonCatalogView struct {
	Type        string   `json:"type"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Searchable  bool     `json:"searchable"`
	Browsable   bool     `json:"browsable"`
	Unsupported []string `json:"unsupported,omitempty"`
}

// SportCatalog keeps the sports route's response name.
type SportCatalog = AddonCatalogView

// AddonItem is one entry in a catalog. It is safe to send to a viewer: it
// carries no stream URL.
type AddonItem struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Poster      string `json:"poster,omitempty"`
	Background  string `json:"background,omitempty"`
	Group       string `json:"group,omitempty"`
	ReleaseInfo string `json:"releaseInfo,omitempty"`
	Category    string `json:"category,omitempty"`
}

// SportItem keeps the sports route's response name.
type SportItem = AddonItem

// AddonEvent is the addon's metadata for one selected item.
type AddonEvent struct {
	Type        string   `json:"type"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Poster      string   `json:"poster,omitempty"`
	Background  string   `json:"background,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	ReleaseInfo string   `json:"releaseInfo,omitempty"`
	Category    string   `json:"category,omitempty"`
}

// SportEvent keeps the sports route's response name.
type SportEvent = AddonEvent

// AddonStream is a playable feed resolved from an addon. URL, UserAgent and
// Headers stay server-side; the API exchanges them for an opaque token.
type AddonStream struct {
	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
	// Quality is the nominal height the addon's own labels advertise, 0 when it
	// says nothing. It is never measured: ranking uses the addon's claim and a
	// real probe confirms playback separately.
	Quality   int               `json:"-"`
	URL       string            `json:"-"`
	UserAgent string            `json:"-"`
	Headers   map[string]string `json:"-"`
}

// SportStream keeps the sports tests' name.
type SportStream = AddonStream

// IsAddonKind reports whether a source is a Stremio addon. "sport" is the
// legacy name for a sports addon and remains accepted; both are addons whose
// capabilities come from the manifest.
func IsAddonKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "stremio", "sport":
		return true
	}
	return false
}

// AddonSources lists configured addons, including disabled ones so the
// settings page keeps their identity while they are turned off, and including
// their last discovered capabilities.
func (s *Service) AddonSources() []SportSource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SportSource, 0)
	for _, src := range s.cfg.LiveTV.Sources {
		if !IsAddonKind(src.Kind) {
			continue
		}
		out = append(out, SportSource{ID: src.ID, Name: src.Name, Enabled: src.Enabled, Capabilities: src.Capabilities})
	}
	return out
}

// SportSources keeps the sports route's name: the addons that have something
// to browse besides TV channels.
func (s *Service) SportSources() []SportSource {
	out := make([]SportSource, 0)
	for _, src := range s.AddonSources() {
		if addonHasBrowsableContent(src) {
			out = append(out, src)
		}
	}
	return out
}

// addonHasBrowsableContent decides whether an addon belongs in the catalog
// browser rather than only in the channel list. Without discovered
// capabilities the source is listed optimistically: the Live TV browser then
// reports what it finds instead of hiding a valid addon.
func addonHasBrowsableContent(src SportSource) bool {
	if src.Capabilities == nil {
		return true
	}
	return src.Capabilities.HasBrowsableModel() || !src.Capabilities.HasTVChannels()
}

// AddonCatalogViews exposes declared catalogs with their browsing constraints.
func AddonCatalogViews(caps *config.AddonCapabilities, contentType string) []AddonCatalogView {
	out := make([]AddonCatalogView, 0)
	if caps == nil {
		return out
	}
	for _, cat := range catalogsOf(caps, contentType) {
		out = append(out, AddonCatalogView{
			Type: cat.Type, ID: cat.ID, Name: cat.Name, Searchable: cat.Searchable,
			Browsable: cat.Browsable, Unsupported: cat.Unsupported,
		})
	}
	return out
}

// SportCatalogs returns the event shelves declared by an addon. It is a
// manifest-only operation; no event or stream is fetched.
func (s *Service) SportCatalogs(ctx context.Context, src config.LiveTVSource) ([]SportCatalog, error) {
	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		return nil, err
	}
	out := AddonCatalogViews(caps, "sport")
	if len(out) == 0 {
		return nil, sportTypeError(caps)
	}
	return out, nil
}

// sportTypeError explains that a valid addon simply has no sport catalogs.
func sportTypeError(caps *config.AddonCapabilities) error {
	if caps == nil || len(caps.Types) == 0 {
		return ErrNotSportAddon
	}
	return fmt.Errorf("%w (it only offers %s catalogs)", ErrNotSportAddon, strings.Join(caps.Types, ", "))
}

// SportItems lists one event shelf.
func (s *Service) SportItems(ctx context.Context, src config.LiveTVSource, catalogID, query string, skip int) ([]SportItem, error) {
	return s.AddonItems(ctx, src, "sport", catalogID, query, skip)
}

// SportMeta fetches the addon's metadata for a selected event.
func (s *Service) SportMeta(ctx context.Context, src config.LiveTVSource, metaID string) (*SportEvent, error) {
	return s.AddonMeta(ctx, src, "sport", metaID)
}

// SportStreams asks the addon for the event's playable feeds.
func (s *Service) SportStreams(ctx context.Context, src config.LiveTVSource, metaID string) ([]SportStream, error) {
	return s.AddonStreams(ctx, src, "sport", metaID)
}

// CheckSportAddon validates the manifest and counts the first sport catalog's
// events. An empty catalog is valid (nothing is on right now), but a valid
// manifest with no sport catalogs is reported as such.
func (s *Service) CheckSportAddon(ctx context.Context, src config.LiveTVSource) (catalogs, events int, err error) {
	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		return 0, 0, err
	}
	list := AddonCatalogViews(caps, "sport")
	if len(list) == 0 {
		return 0, 0, sportTypeError(caps)
	}
	items, err := s.SportItems(ctx, src, list[0].ID, "", 0)
	if err != nil {
		return len(list), 0, err
	}
	return len(list), len(items), nil
}
