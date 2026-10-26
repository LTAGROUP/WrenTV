package sports

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// League describes one supported competition: its provider, application sport
// and the capabilities clients may rely on. Catalog entries are versioned with
// provider evidence in coverage_catalog.json; a directory entry is not
// automatically a supported league.
type League struct {
	Provider             string   `json:"provider,omitempty"`
	ApplicationSport     string   `json:"applicationSport,omitempty"`
	College              bool     `json:"college,omitempty"`
	Adapter              string   `json:"adapter,omitempty"`
	ImplementationStatus string   `json:"implementationStatus,omitempty"`
	Capabilities         []string `json:"capabilities,omitempty"`
	Aliases              []string `json:"aliases,omitempty"`
	CoverageNote         string   `json:"coverageNote,omitempty"`

	ID            string `json:"-"`
	Name          string `json:"-"`
	Sport         string `json:"-"`
	Slug          string `json:"-"`
	Category      string `json:"-"`
	EventKind     string `json:"-"`
	SupportsTeams bool   `json:"-"`
}

// LeagueCatalog is every league this service knows how to fetch. Not every
// entry is enabled; configuration selects a subset. Only leagues whose API
// contract can actually surface a hub card belong here.
var LeagueCatalog = extendLeagueCatalog([]League{
	{ID: "pga", Name: "PGA Tour", Sport: "golf", Slug: "pga", Category: "golf", EventKind: "tournament"},
	{ID: "boxing", Name: "Boxing", Sport: "boxing", Category: "boxing", EventKind: "fight-card"},
	{ID: "cricket-8048", Name: "Indian Premier League", Sport: "cricket", Slug: "8048", Category: "cricket", EventKind: "matchup", SupportsTeams: true},
	{ID: "nfl", Name: "NFL", Sport: "football", Slug: "nfl", Category: "football", EventKind: "matchup", SupportsTeams: true},
	{ID: "college-football", Name: "NCAAF", Sport: "football", Slug: "college-football", Category: "football", EventKind: "matchup", SupportsTeams: true},
	{ID: "nba", Name: "NBA", Sport: "basketball", Slug: "nba", Category: "basketball", EventKind: "matchup", SupportsTeams: true},
	{ID: "mens-college-basketball", Name: "NCAAM", Sport: "basketball", Slug: "mens-college-basketball", Category: "basketball", EventKind: "matchup", SupportsTeams: true},
	{ID: "womens-college-basketball", Name: "NCAAW", Sport: "basketball", Slug: "womens-college-basketball", Category: "basketball", EventKind: "matchup", SupportsTeams: true},
	{ID: "wnba", Name: "WNBA", Sport: "basketball", Slug: "wnba", Category: "basketball", EventKind: "matchup", SupportsTeams: true},
	{ID: "mlb", Name: "MLB", Sport: "baseball", Slug: "mlb", Category: "baseball", EventKind: "matchup", SupportsTeams: true},
	{ID: "nhl", Name: "NHL", Sport: "hockey", Slug: "nhl", Category: "hockey", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-fifa.world", Name: "FIFA World Cup", Sport: "soccer", Slug: "fifa.world", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-fifa.wwc", Name: "FIFA Women's World Cup", Sport: "soccer", Slug: "fifa.wwc", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-eng.1", Name: "Premier League", Sport: "soccer", Slug: "eng.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-eng.2", Name: "Championship", Sport: "soccer", Slug: "eng.2", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-esp.1", Name: "La Liga", Sport: "soccer", Slug: "esp.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-ger.1", Name: "Bundesliga", Sport: "soccer", Slug: "ger.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-ita.1", Name: "Serie A", Sport: "soccer", Slug: "ita.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-fra.1", Name: "Ligue 1", Sport: "soccer", Slug: "fra.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-usa.1", Name: "MLS", Sport: "soccer", Slug: "usa.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-usa.nwsl", Name: "NWSL", Sport: "soccer", Slug: "usa.nwsl", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-usa.nwsl.cup", Name: "NWSL Challenge Cup", Sport: "soccer", Slug: "usa.nwsl.cup", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-uefa.champions", Name: "Champions League", Sport: "soccer", Slug: "uefa.champions", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-uefa.europa", Name: "Europa League", Sport: "soccer", Slug: "uefa.europa", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-uefa.europa.conf", Name: "Conference League", Sport: "soccer", Slug: "uefa.europa.conf", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-mex.1", Name: "Liga MX", Sport: "soccer", Slug: "mex.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-ned.1", Name: "Eredivisie", Sport: "soccer", Slug: "ned.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "soccer-por.1", Name: "Primeira Liga", Sport: "soccer", Slug: "por.1", Category: "soccer", EventKind: "matchup", SupportsTeams: true},
	{ID: "ufc", Name: "UFC", Sport: "mma", Slug: "ufc", Category: "mma", EventKind: "fight-card"},
	{ID: "atp", Name: "ATP Tour", Sport: "tennis", Slug: "atp", Category: "tennis", EventKind: "matchup"},
	{ID: "wta", Name: "WTA Tour", Sport: "tennis", Slug: "wta", Category: "tennis", EventKind: "matchup"},
	{ID: "f1", Name: "Formula 1", Sport: "racing", Slug: "f1", Category: "racing", EventKind: "race"},
	{ID: "nascar", Name: "NASCAR Cup", Sport: "racing", Slug: "nascar-premier", Category: "racing", EventKind: "race"},
	{ID: "motogp", Name: "MotoGP", Sport: "racing", Category: "racing", EventKind: "race"},
	{ID: "indycar", Name: "IndyCar", Sport: "racing", Slug: "irl", Category: "racing", EventKind: "race"},
	{ID: "rugby-180659", Name: "Six Nations", Sport: "rugby", Slug: "180659", Category: "rugby", EventKind: "matchup", SupportsTeams: true},
	{ID: "rugby-164205", Name: "Rugby World Cup", Sport: "rugby", Slug: "164205", Category: "rugby", EventKind: "matchup", SupportsTeams: true},
	{ID: "rugby-267979", Name: "Premiership", Sport: "rugby", Slug: "267979", Category: "rugby", EventKind: "matchup", SupportsTeams: true},
	{ID: "rugby-242041", Name: "Super Rugby", Sport: "rugby", Slug: "242041", Category: "rugby", EventKind: "matchup", SupportsTeams: true},
	{ID: "rugby-270559", Name: "Top 14", Sport: "rugby", Slug: "270559", Category: "rugby", EventKind: "matchup", SupportsTeams: true},
	{ID: "rugby-league-3", Name: "NRL", Sport: "rugby-league", Slug: "3", Category: "rugby-league", EventKind: "matchup", SupportsTeams: true},
	{ID: "aso:tour", Name: "Tour de France", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:vuelta", Name: "La Vuelta", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:tour-femmes", Name: "Tour de France Femmes", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:paris-nice", Name: "Paris-Nice", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:vuelta-femenina", Name: "La Vuelta Femenina", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:paris-roubaix", Name: "Paris-Roubaix", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:paris-roubaix-femmes", Name: "Paris-Roubaix Femmes", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:liege-bastogne-liege", Name: "Liège-Bastogne-Liège", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:liege-bastogne-liege-femmes", Name: "Liège-Bastogne-Liège Femmes", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:fleche-wallonne", Name: "La Flèche Wallonne", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "aso:fleche-wallonne-femmes", Name: "La Flèche Wallonne Femmes", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "rcs:giro", Name: "Giro d’Italia", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "cro:cro-race", Name: "CRO Race", Sport: "cycling", Category: "cycling", EventKind: "race"},
	{ID: "uci:road-worlds", Name: "UCI Road World Championships", Sport: "cycling", Category: "cycling", EventKind: "race"},
})

// defaultLeagueIDs is every active catalog league, all enabled by default.
var defaultLeagueIDs = func() []string {
	ids := make([]string, 0, len(LeagueCatalog))
	for _, league := range LeagueCatalog {
		if league.active() {
			ids = append(ids, league.ID)
		}
	}
	return ids
}()

func applicationSport(sport, slug string) string {
	if slug == "college-softball" || slug == "lls" {
		return "softball"
	}
	switch sport {
	case "football":
		return "american-football"
	case "hockey":
		return "ice-hockey"
	case "rugby":
		return "rugby-union"
	case "racing":
		return "motorsport"
	}
	return sport
}

func (l League) active() bool {
	return l.ImplementationStatus == "" ||
		((l.ImplementationStatus == "validated" || l.ImplementationStatus == "limited") && l.hasCapability("schedule"))
}

func (l League) hasCapability(name string) bool {
	for _, c := range l.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

func wantedAll(ids map[string]struct{}) bool { _, ok := ids["*"]; return ok }

//go:embed coverage_catalog.json
var coverageCatalogJSON []byte

type coverageDescriptor struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Provider             string   `json:"provider"`
	ProviderSport        string   `json:"providerSport"`
	ProviderLeague       string   `json:"providerLeague"`
	ApplicationSport     string   `json:"applicationSport"`
	College              bool     `json:"college"`
	Adapter              string   `json:"adapter"`
	ImplementationStatus string   `json:"implementationStatus"`
	Capabilities         []string `json:"capabilities"`
	Aliases              []string `json:"aliases"`
	CoverageNote         string   `json:"coverageNote"`
}

func extendLeagueCatalog(legacy []League) []League {
	var additions []coverageDescriptor
	if err := json.Unmarshal(coverageCatalogJSON, &additions); err != nil {
		panic(fmt.Sprintf("invalid embedded coverage catalog: %v", err))
	}
	seen := map[string]bool{}
	for i := range legacy {
		l := &legacy[i]
		seen[l.ID] = true
		l.ApplicationSport = applicationSport(l.Sport, l.Slug)
		l.College = strings.Contains(l.Slug, "college")
		l.Provider = "espn"
		l.Adapter = l.EventKind
		l.ImplementationStatus = "validated"
		l.Capabilities = []string{"schedule"}
		switch {
		case l.ID == "motogp":
			l.Provider = "organizer"
			l.Capabilities = append(l.Capabilities, "live-score", "final-result")
		case l.Sport == "cycling":
			l.Provider = "organizer"
			l.ImplementationStatus = "limited"
		case l.ID == "boxing":
			l.Provider = "thesportsdb"
			l.ImplementationStatus = "limited"
		default:
			l.Capabilities = append(l.Capabilities, "live-score", "final-result")
		}
		if l.SupportsTeams {
			l.Capabilities = append(l.Capabilities, "team-catalog")
		}
	}
	for _, entry := range additions {
		if entry.ID == "" || seen[entry.ID] || entry.ProviderSport == "" || entry.ProviderLeague == "" {
			panic("invalid or duplicate coverage descriptor")
		}
		if entry.ID != "espn:"+entry.ProviderSport+":"+entry.ProviderLeague {
			panic("noncanonical coverage descriptor")
		}
		seen[entry.ID] = true
		app := entry.ApplicationSport
		if app == "" {
			app = applicationSport(entry.ProviderSport, entry.ProviderLeague)
		}
		kind := "matchup"
		switch entry.ProviderSport {
		case "golf":
			if entry.ProviderLeague != "tgl" {
				kind = "tournament"
			}
		case "mma":
			kind = "fight-card"
		case "racing":
			kind = "race"
		}
		l := League{
			ID: entry.ID, Name: entry.Name, Sport: entry.ProviderSport, Slug: entry.ProviderLeague,
			Category: entry.ProviderSport, EventKind: kind, Provider: entry.Provider,
			ApplicationSport: app, College: entry.College, Adapter: entry.Adapter,
			ImplementationStatus: entry.ImplementationStatus, Capabilities: entry.Capabilities,
			Aliases: entry.Aliases, CoverageNote: entry.CoverageNote,
		}
		l.SupportsTeams = l.hasCapability("team-catalog") || l.hasCapability("team-identities")
		legacy = append(legacy, l)
	}
	return legacy
}

func (l League) descriptor(enabled bool) SportsLeague {
	return SportsLeague{
		ID: l.ID, Name: l.Name, Sport: l.Sport, Category: l.Category, EventKind: l.EventKind,
		SupportsTeams: l.SupportsTeams, Enabled: enabled, Provider: l.Provider,
		ProviderSport: l.Sport, ProviderLeague: l.Slug, ApplicationSport: l.ApplicationSport,
		College: l.College, Adapter: l.Adapter, ImplementationStatus: l.ImplementationStatus,
		Capabilities: l.Capabilities, Aliases: l.Aliases, CoverageNote: l.CoverageNote,
	}
}

func providerEventID(game SportsGame) string {
	if game.ProviderEventID != "" {
		return game.ProviderEventID
	}
	id := game.ID
	// Dynamic ESPN IDs carry the canonical league prefix; the summary API
	// expects the bare provider event ID.
	if strings.HasPrefix(game.League, "espn:") && strings.HasPrefix(id, game.League+":") {
		id = strings.TrimPrefix(id, game.League+":")
	}
	return id
}

// supportsHubLeague reports whether a dated scoreboard may carry the league.
// Races and cycling use the event contract, never a synthesized matchup.
func supportsHubLeague(id string) bool {
	for _, league := range LeagueCatalog {
		if league.ID == id {
			return league.EventKind == "matchup" || league.EventKind == "fight-card" || league.EventKind == "tournament"
		}
	}
	return false
}
