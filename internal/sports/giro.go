package sports

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// htmlDoc aliases the organizer document so provider files share one type.
type htmlDoc = html.Node

func giroEvidence(now time.Time) cyclingEvidence {
	return cyclingEvidence{Provider: "RCS Sport / Giro d'Italia", ObservedAt: now}
}

func giroAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func giroHasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(giroAttr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func giroNodes(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if match(node) {
			found = append(found, node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func giroClass(n *html.Node, class string) []*html.Node {
	return giroNodes(n, func(n *html.Node) bool { return giroHasClass(n, class) })
}

func giroText(n *html.Node) string {
	var b strings.Builder
	for _, text := range giroNodes(n, func(n *html.Node) bool { return n.Type == html.TextNode }) {
		b.WriteString(text.Data)
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func giroField(n *html.Node, class string) string {
	nodes := giroClass(n, class)
	if len(nodes) != 1 {
		return ""
	}
	return giroText(nodes[0])
}

func giroEdition(doc *html.Node, year int) bool {
	titles := giroNodes(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "title" })
	return len(titles) == 1 && strings.Contains(giroText(titles[0]), strconv.Itoa(year))
}

func (s *Service) giroHTML(ctx context.Context, path string) (*html.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.giroditalia.it/en/"+path, nil)
	if err != nil {
		return nil, err
	}
	client := *s.client
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.URL.Scheme != "https" || next.URL.Host != "www.giroditalia.it" || next.URL.User != nil {
			return fmt.Errorf("untrusted Giro redirect")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Giro provider status %d", resp.StatusCode)
	}
	const limit = 3 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("Giro response exceeds limit")
	}
	return html.Parse(strings.NewReader(string(body)))
}

var giroDatePattern = regexp.MustCompile(`\b\d{2}/\d{2}/\d{4}\b`)
var giroDurationPattern = regexp.MustCompile(`^\d{1,3}:\d{1,2}(?::\d{2})?(?:[.,]\d{1,3})?$`)

func normalizeGiroSchedule(doc *html.Node, year int, now time.Time) (CyclingRace, error) {
	race := CyclingRace{
		ID: fmt.Sprintf("rcs:giro:%d", year), Name: "Giro d'Italia", Category: "men",
		RaceKind: "stage-race", Source: giroEvidence(now),
		SourceURL: "https://www.giroditalia.it/en/the-route/", Stages: []CyclingStage{},
	}
	if !giroEdition(doc, year) {
		return race, fmt.Errorf("Giro edition mismatch")
	}
	seen := map[int]bool{}
	tables := giroClass(doc, "list-tappe")
	if len(tables) != 1 {
		return race, fmt.Errorf("Giro schedule table missing or ambiguous")
	}
	for _, row := range giroClass(tables[0], "single-tappa") {
		id := giroAttr(row, "id")
		if !strings.HasPrefix(id, "tappa-") {
			continue
		}
		number, err := strconv.Atoi(strings.TrimPrefix(id, "tappa-"))
		if err != nil || number < 1 || number > 40 {
			continue
		}
		if seen[number] {
			return race, fmt.Errorf("duplicate Giro stage")
		}
		seen[number] = true
		date, err := time.Parse("02/01/2006", giroDatePattern.FindString(giroField(row, "col-3")))
		if err != nil || date.Year() != year {
			return race, fmt.Errorf("invalid Giro stage date")
		}
		departure, arrival := giroField(row, "partenza-value"), giroField(row, "arrivo-value")
		length, err := strconv.ParseFloat(strings.ReplaceAll(giroField(row, "distanza-value"), ",", "."), 64)
		links := giroNodes(row, func(n *html.Node) bool {
			return n.Data == "a" && strings.HasPrefix(giroAttr(n, "href"),
				fmt.Sprintf("https://www.giroditalia.it/en/tappe/stage-%d-of-the-giro-ditalia-%d-", number, year))
		})
		if departure == "" || arrival == "" || err != nil || length <= 0 || length >= 1000 || len(links) != 1 {
			return race, fmt.Errorf("incomplete Giro stage")
		}
		status := "unknown"
		if date.Format("2006-01-02") > now.UTC().Format("2006-01-02") {
			status = "scheduled"
		}
		stage := CyclingStage{
			ID: race.ID + ":" + strconv.Itoa(number), Name: fmt.Sprintf("Stage %d", number),
			Date: date.Format("2006-01-02"), Departure: departure, Arrival: arrival,
			Distance: strconv.FormatFloat(length, 'f', -1, 64) + " km", Status: status,
			Results:                    cyclingPending(race.Source, "Select this stage to load published results."),
			GeneralClassification:      cyclingPending(race.Source, "Select this stage to check the published overall classification."),
			GeneralClassificationLabel: fmt.Sprintf("After stage %d", number),
		}
		stage.RouteGuide = &CyclingRouteGuide{
			State: "pending", Source: race.Source, SourceURL: giroAttr(links[0], "href"),
			Reason: "Select this stage to load the organizer route guide.",
		}
		// Only translated labels verified in the organizer's route table are mapped.
		stage.Terrain = map[string]string{
			"pianeggiante": "Flat", "collinare": "Hilly", "montagna": "Mountain", "crono": "Individual time trial",
		}[giroAttr(row, "data-tipologia")]
		race.Stages = append(race.Stages, stage)
	}
	sort.Slice(race.Stages, func(i, j int) bool { return race.Stages[i].IDNumber() < race.Stages[j].IDNumber() })
	if len(race.Stages) != 21 {
		return race, fmt.Errorf("Giro schedule incomplete: %d stages", len(race.Stages))
	}
	for i, stage := range race.Stages {
		if stage.IDNumber() != i+1 {
			return race, fmt.Errorf("Giro schedule not contiguous")
		}
	}
	race.StartDate = race.Stages[0].Date
	race.EndDate = race.Stages[len(race.Stages)-1].Date
	return race, nil
}

func (stage CyclingStage) IDNumber() int {
	parts := strings.Split(stage.ID, ":")
	number, _ := strconv.Atoi(parts[len(parts)-1])
	return number
}

func normalizeGiroClassification(doc *html.Node, class, stageID string, year int, now time.Time) (cyclingResults, error) {
	result := cyclingResults{State: "pending", Source: giroEvidence(now), Reason: "No classification has been published."}
	if !giroEdition(doc, year) {
		return result, fmt.Errorf("Giro result edition mismatch")
	}
	panels := giroClass(doc, class)
	if len(panels) != 1 {
		return result, fmt.Errorf("Giro classification panel missing or ambiguous")
	}
	header := giroClass(panels[0], "header-table")
	if len(header) != 1 || giroField(header[0], "team") != "Team" ||
		giroField(header[0], "tempo") != "Time" || giroField(header[0], "distacco") != "Gap" {
		return result, fmt.Errorf("Giro result columns changed")
	}
	seen := map[string]bool{}
	for _, row := range giroClass(panels[0], "line-table") {
		name, surname := giroField(row, "name"), giroField(row, "surname")
		riders := giroNodes(row, func(n *html.Node) bool {
			return n.Data == "a" && strings.HasPrefix(giroAttr(n, "data-destination"), "Rider/")
		})
		if len(riders) != 1 || name == "" || surname == "" {
			return result, fmt.Errorf("invalid Giro rider row")
		}
		rider := giroAttr(riders[0], "data-destination")
		if seen[rider] {
			return result, fmt.Errorf("duplicate Giro rider")
		}
		seen[rider] = true
		rank, err := strconv.Atoi(giroField(row, "position"))
		if err != nil || rank < 1 {
			return result, fmt.Errorf("unsupported Giro rank")
		}
		elapsed, gap := giroField(row, "tempo"), giroField(row, "distacco")
		if !giroDurationPattern.MatchString(elapsed) || !giroDurationPattern.MatchString(gap) {
			return result, fmt.Errorf("unsupported Giro time")
		}
		result.Data = append(result.Data, cyclingResult{
			ID: stageID + ":" + rider, Name: name + " " + surname, Team: giroField(row, "team"),
			Rank: rank, Time: elapsed, Gap: "+" + gap,
		})
	}
	if len(result.Data) > 0 {
		result.State = "available"
		result.Reason = ""
	}
	return result, nil
}

func (s *Service) enrichGiroStage(ctx context.Context, stage *CyclingStage, year, number int, now time.Time) error {
	doc, err := s.giroHTML(ctx, fmt.Sprintf("classifiche/di-tappa/%d/", number))
	if err != nil {
		return err
	}
	if giroField(doc, "js-n-stage") != strconv.Itoa(number) {
		return fmt.Errorf("Giro selected stage mismatch")
	}
	stage.Results, err = normalizeGiroClassification(doc, "js-tab-classifica-ORARR", stage.ID, year, now)
	if err != nil {
		return err
	}
	general, err := s.giroHTML(ctx, "classifiche/")
	stage.GeneralClassification = cyclingResults{
		State: "unavailable", Source: giroEvidence(now),
		Reason: "The organizer publishes only its current overall classification; this stage's overall classification is unavailable.",
	}
	if err != nil {
		stage.GeneralClassification.Reason = "The Giro overall classification could not be reached."
		return nil
	}
	// The overall page links its current stage. Never attach today's GC to a past stage.
	current := giroNodes(general, func(n *html.Node) bool {
		return n.Data == "a" && giroHasClass(n, "single-tab-controller") && giroAttr(n, "data-tab") == "classifiche-di-tappa"
	})
	if len(current) != 1 || strings.TrimRight(giroAttr(current[0], "href"), "/") !=
		fmt.Sprintf("https://www.giroditalia.it/en/classifiche/di-tappa/%d", number) {
		return nil
	}
	result, err := normalizeGiroClassification(general, "js-tab-classifica-CLGEN", stage.ID, year, now)
	if err != nil {
		stage.GeneralClassification.Reason = "The Giro overall classification format could not be verified."
		return nil
	}
	stage.GeneralClassification = result
	return nil
}
