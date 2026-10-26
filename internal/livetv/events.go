package livetv

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// An addon can list events (HighFly's sport catalogs and anything else the
// manifest marks as an event model). Those items are Live TV entries like a
// channel: they appear in the same list, are filtered by the same source
// picker and start through the same player. They are deliberately not channels:
// no channel number, no playlist row and no XMLTV programme is invented for an
// item the manifest never described as a station.

const (
	// addonEventsPerCatalog bounds one catalog. The Stremio protocol answers
	// 100 metas per page and Wren TV never asks for a second page here, so a
	// misbehaving addon cannot grow the Live TV payload without bound.
	addonEventsPerCatalog = 200
	// addonEventsPerSource bounds what one addon contributes.
	addonEventsPerSource = 500
)

// EventEntry is one selectable event from an addon catalog. It carries no
// stream URL: the viewer's player asks the server for the feed, and the addon's
// links never leave it.
type EventEntry struct {
	// ID identifies the entry in the viewer's list. It stays stable across
	// refreshes because it is derived from the source, content type and item
	// id rather than an arrival order.
	ID     string `json:"id"`
	ItemID string `json:"itemId"`
	Type   string `json:"type"`
	// Source is the configured provider id, the same field a channel carries,
	// so one source filter covers channels and events alike.
	Source      string `json:"source"`
	SourceName  string `json:"sourceName,omitempty"`
	Name        string `json:"name"`
	Group       string `json:"group,omitempty"`
	Logo        string `json:"logo,omitempty"`
	Description string `json:"description,omitempty"`
	ReleaseInfo string `json:"releaseInfo,omitempty"`
}

// addonEventEntries lists the items an addon publishes in its event catalogs.
// Only catalogs the manifest declares as browsable, and whose content type it
// serves the stream resource for, are read: an event Wren TV cannot play is not
// offered as a selectable entry. A catalog that failed is reported with its own
// error — so one broken shelf does not hide the rest, and the caller can tell a
// temporary provider failure (a rate limit, an addon that is briefly down) from
// a catalogue that genuinely has nothing in it.
func (s *Service) addonEventEntries(ctx context.Context, src config.LiveTVSource, caps *config.AddonCapabilities) ([]EventEntry, []error) {
	if caps == nil {
		return nil, nil
	}
	name := strings.TrimSpace(src.Name)
	if name == "" {
		name = src.ID
	}
	var out []EventEntry
	var problems []error
	seen := map[string]bool{}
	for _, model := range caps.Models {
		if model.Model != "events" || model.Catalogs == 0 || !model.Stream {
			continue
		}
		for _, cat := range catalogsOf(caps, model.Type) {
			if !cat.Browsable || len(cat.Unsupported) > 0 {
				continue
			}
			if len(out) >= addonEventsPerSource {
				return out, problems
			}
			// The catalog is read without extras: an event shelf that needs a
			// search or a genre Wren TV did not choose would be a guess, not a
			// list of what the addon currently offers.
			// catalogItems adds the .json suffix itself.
			endpoint := stremioBase(src.URL) + "/catalog/" + url.PathEscape(cat.Type) + "/" + url.PathEscape(cat.ID)
			items, err := s.catalogItems(ctx, src, endpoint, cat.Type)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", cat.Name, err))
				continue
			}
			added := 0
			for _, item := range items {
				if len(out) >= addonEventsPerSource || added >= addonEventsPerCatalog {
					break
				}
				key := item.Type + "\x00" + item.ID
				if item.ID == "" || item.Name == "" || seen[key] {
					continue
				}
				seen[key] = true
				added++
				group := strings.TrimSpace(item.Group)
				if group == "" {
					group = strings.TrimSpace(cat.Name)
				}
				out = append(out, EventEntry{
					ID: shortHash(src.ID + "|" + item.Type + "|" + item.ID),
					// The item id is kept apart from the entry id: the player
					// sends it back to the addon, the list uses the entry id.
					ItemID: item.ID, Type: item.Type,
					Source: src.ID, SourceName: name,
					Name: item.Name, Group: group, Logo: firstNonEmpty(item.Poster, item.Background),
					Description: item.Description, ReleaseInfo: item.ReleaseInfo,
				})
			}
		}
	}
	return out, problems
}

// AddonEventEntries is the admin check's view of the events a manifest would
// contribute. Like the refresh, it reads only event catalogs the addon can
// list and play.
func (s *Service) AddonEventEntries(ctx context.Context, src config.LiveTVSource) ([]EventEntry, error) {
	caps, err := s.AddonCapabilities(ctx, src)
	if err != nil {
		return nil, err
	}
	entries, _ := s.addonEventEntries(ctx, src, caps)
	return entries, nil
}

// addonEventsTemporary reports whether every failure this refresh hit while
// reading a source's event catalogs may pass on its own. It is what separates
// "the provider is having a bad minute" from "the provider has nothing", so a
// rate limit or an outage never reads as an empty list.
func addonEventsTemporary(problems []error) bool {
	if len(problems) == 0 {
		return false
	}
	for _, problem := range problems {
		if !temporaryAddonError(problem) {
			return false
		}
	}
	return true
}

// EventsFor lists the addon events for one source, or every source when source
// is empty. Events are rebuilt by each refresh, so a catalog that stopped
// offering an item stops offering it here too.
func (s *Service) EventsFor(source string) []EventEntry {
	source = strings.TrimSpace(source)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EventEntry, 0, len(s.events))
	for _, event := range s.events {
		if source != "" && event.Source != source {
			continue
		}
		out = append(out, event)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
