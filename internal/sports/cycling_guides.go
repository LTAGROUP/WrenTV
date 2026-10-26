package sports

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Route guides are organizer diagrams, not live rider positions or route
// geometry. Only verified organizer hosts and path shapes are accepted.

type asoGuideSite struct{ host, title, namespace string }

var asoGuideSites = map[string]asoGuideSite{
	"tour":        {"www.letour.fr", "Tour de France", "tdf"},
	"tour-femmes": {"www.letourfemmes.fr", "Tour de France Femmes", "trf"},
	"paris-nice":  {"www.paris-nice.fr", "Paris-Nice", "pnc"},
	"vuelta":      {"www.lavuelta.es", "La Vuelta", "vue"},
}

func giroGuideURL(value string, year, number int) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host == "www.giroditalia.it" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" &&
		strings.HasPrefix(u.Path, fmt.Sprintf("/en/tappe/stage-%d-of-the-giro-ditalia-%d-", number, year)) &&
		!strings.Contains(u.Path, "..") && u.RawPath == ""
}

func giroGuideImage(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "static2.giroditalia.it" || u.User != nil ||
		u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") ||
		!strings.HasPrefix(u.Path, "/wp-content/uploads/") {
		return false
	}
	path := strings.ToLower(u.Path)
	return strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".png") ||
		strings.HasSuffix(path, ".jpeg") || strings.HasSuffix(path, ".webp")
}

func normalizeGiroGuide(doc *html.Node, sourceURL string, year, number int, now time.Time) (CyclingRouteGuide, error) {
	guide := CyclingRouteGuide{State: "unavailable", Source: giroEvidence(now), SourceURL: sourceURL}
	if !giroGuideURL(sourceURL, year, number) || !giroEdition(doc, year) {
		return guide, fmt.Errorf("Giro route edition mismatch")
	}
	canonical := giroNodes(doc, func(n *html.Node) bool { return n.Data == "link" && giroAttr(n, "rel") == "canonical" })
	titles := giroNodes(doc, func(n *html.Node) bool { return n.Data == "title" })
	if len(canonical) != 1 || giroAttr(canonical[0], "href") != sourceURL || len(titles) != 1 ||
		!strings.HasPrefix(giroText(titles[0]), fmt.Sprintf("Stage %d of the Giro", number)) {
		return guide, fmt.Errorf("Giro route stage mismatch")
	}
	for class, target := range map[string]*string{
		"js-tab-planimetria":      &guide.MapURL,
		"js-tab-info-altrimetria": &guide.ProfileURL,
	} {
		panels := giroClass(doc, class)
		if len(panels) != 1 {
			continue
		}
		// The organizer's first slide is the primary diagram. Further circuit or
		// finish diagrams remain available through the complete official guide.
		slides := giroClass(panels[0], "single-slide")
		if len(slides) == 0 {
			continue
		}
		images := giroNodes(slides[0], func(n *html.Node) bool { return n.Data == "img" })
		if len(images) == 1 && giroGuideImage(giroAttr(images[0], "src")) {
			*target = giroAttr(images[0], "src")
		}
	}
	if guide.MapURL == "" && guide.ProfileURL == "" {
		return guide, fmt.Errorf("Giro route images unavailable")
	}
	guide.State = "available"
	return guide, nil
}

func asoGuideImage(value string, site asoGuideSite) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "img.aso.fr" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") {
		return false
	}
	for _, format := range []string{"jpg", "png", "webp"} {
		if strings.HasPrefix(u.Path, "/core_app/img-cycling-"+site.namespace+"-"+format+"/") &&
			len(strings.Split(strings.Trim(u.Path, "/"), "/")) >= 5 {
			return true
		}
	}
	return false
}

func normalizeASOGuide(doc *html.Node, site asoGuideSite, year, number int, now time.Time) (CyclingRouteGuide, error) {
	sourceURL := fmt.Sprintf("https://%s/en/stage-%d", site.host, number)
	guide := CyclingRouteGuide{State: "unavailable", Source: cyclingSource(now, 0), SourceURL: sourceURL}
	years := giroNodes(doc, func(n *html.Node) bool { return n.Data == "meta" && giroAttr(n, "name") == "year" })
	urls := giroNodes(doc, func(n *html.Node) bool { return n.Data == "meta" && giroAttr(n, "property") == "og:url" })
	titles := giroNodes(doc, func(n *html.Node) bool { return n.Data == "title" })
	if len(years) != 1 || giroAttr(years[0], "content") != strconv.Itoa(year) ||
		len(urls) != 1 || giroAttr(urls[0], "content") != sourceURL ||
		len(titles) != 1 || !strings.HasPrefix(giroText(titles[0]), fmt.Sprintf("Stage %d - ", number)) ||
		!strings.HasSuffix(giroText(titles[0]), fmt.Sprintf(" - %s %d", site.title, year)) {
		return guide, fmt.Errorf("ASO guide identity mismatch")
	}
	for id, target := range map[string]*string{"profil": &guide.ProfileURL, "map": &guide.MapURL} {
		panels := giroNodes(doc, func(n *html.Node) bool { return giroAttr(n, "id") == id })
		if len(panels) != 1 {
			continue
		}
		candidates := map[string]bool{}
		for _, img := range giroClass(panels[0], "sporting__content__img") {
			value := giroAttr(img, "data-src")
			if img.Data == "img" && asoGuideImage(value, site) {
				candidates[value] = true
			}
		}
		if id == "map" && site.namespace == "tdf" {
			for _, a := range giroNodes(panels[0], func(n *html.Node) bool { return n.Data == "a" }) {
				value := giroAttr(a, "href")
				if asoGuideImage(value, site) && giroText(a) == "Download the map" {
					candidates[value] = true
				}
			}
		}
		if len(candidates) == 1 {
			for value := range candidates {
				*target = value
			}
		}
	}
	if guide.MapURL == "" && guide.ProfileURL == "" {
		guide.Reason = "Static route images are not available. Open the official stage guide for the organizer’s route view."
		return guide, nil
	}
	guide.State = "available"
	return guide, nil
}

func (s *Service) asoGuideHTML(ctx context.Context, site asoGuideSite, number int) (*html.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s/en/stage-%d", site.host, number), nil)
	if err != nil {
		return nil, err
	}
	client := *s.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.URL.Scheme != "https" || next.URL.Host != site.host || next.URL.User != nil {
			return fmt.Errorf("untrusted ASO redirect")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ASO guide status %d", resp.StatusCode)
	}
	const limit = 3 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("ASO guide exceeds size limit")
	}
	return html.Parse(strings.NewReader(string(body)))
}

// enrichCyclingGuide is called under cycling.detailMu; a guide failure cannot
// replace classifications.
func (s *Service) enrichCyclingGuide(ctx context.Context, stage *CyclingStage, year, number int, now time.Time) {
	if stage.RouteGuide == nil {
		return
	}
	cache := &s.cycling
	if cache.guides == nil {
		cache.guides = map[string]cyclingGuideCache{}
	}
	old, exists := cache.guides[stage.ID]
	if exists && now.Before(old.expires) {
		guide := old.guide
		stage.RouteGuide = &guide
		return
	}
	sourceURL := stage.RouteGuide.SourceURL
	guide := CyclingRouteGuide{
		State: "unavailable", Source: giroEvidence(now), SourceURL: sourceURL,
		Reason: "The organizer route guide could not be verified. Open the official stage guide for route information.",
	}
	var err error
	parts := strings.Split(stage.ID, ":")
	if len(parts) != 4 {
		return
	}
	site, aso := asoGuideSites[parts[1]]
	if !aso && (!strings.HasPrefix(stage.ID, "rcs:giro:") || !giroGuideURL(sourceURL, year, number)) {
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if aso {
		guide.Source = cyclingSource(now, 0)
		var doc *html.Node
		doc, err = s.asoGuideHTML(requestCtx, site, number)
		if err == nil {
			guide, err = normalizeASOGuide(doc, site, year, number, now)
		}
	} else {
		var doc *html.Node
		doc, err = s.giroHTML(requestCtx, strings.TrimPrefix(sourceURL, "https://www.giroditalia.it/en/"))
		if err == nil {
			guide, err = normalizeGiroGuide(doc, sourceURL, year, number, now)
		}
	}
	ttl := 30 * time.Minute
	if err != nil {
		ttl = 30 * time.Second
		if exists && (old.guide.MapURL != "" || old.guide.ProfileURL != "") {
			guide = old.guide
			guide.State = "stale"
		}
		guide.Reason = "The organizer route guide could not be refreshed. Open the official stage guide for current route information."
	}
	if len(cache.guides) >= 40 {
		oldest := ""
		var expiry time.Time
		for key, item := range cache.guides {
			if oldest == "" || item.expires.Before(expiry) {
				oldest = key
				expiry = item.expires
			}
		}
		delete(cache.guides, oldest)
	}
	cache.guides[stage.ID] = cyclingGuideCache{guide: guide, expires: now.Add(ttl)}
	stage.RouteGuide = &guide
}
