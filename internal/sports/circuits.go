package sports

import (
	_ "embed"
	"encoding/json"
	"math"
)

// Reference circuit geometry is licensed community data, not live tracking.
//
//go:embed circuits/2026.geojson
var circuitGeometry []byte

var referenceCircuits = loadCircuitGeometry(circuitGeometry)

// circuitEventLinks maps the provider event ID to the verified circuit
// identity. A title mismatch leaves the event without geometry rather than
// guessing a circuit from a sponsor name.
var circuitEventLinks = map[string]struct{ title, circuit string }{
	"600057427": {"Qatar Airways Australian Grand Prix", "au-1953"},
	"600057428": {"Heineken Chinese Grand Prix", "cn-2004"},
	"600057429": {"Aramco Japanese Grand Prix", "jp-1962"},
	"600057430": {"Gulf Air Bahrain Grand Prix", "bh-2002"},
	"600057431": {"STC Saudi Arabian Grand Prix", "sa-2021"},
	"600057432": {"Crypto.com Miami Grand Prix", "us-2022"},
	"600057433": {"Lenovo Canadian Grand Prix", "ca-1978"},
	"600057434": {"Monaco Grand Prix", "mc-1929"},
	"600057435": {"MSC Cruises Barcelona-Catalunya Grand Prix", "es-1991"},
	"600057436": {"Lenovo Austrian Grand Prix", "at-1969"},
	"600057437": {"Pirelli British Grand Prix", "gb-1948"},
	"600057439": {"Moët & Chandon Belgian Grand Prix", "be-1925"},
	"600057440": {"AWS Hungarian Grand Prix", "hu-1986"},
	"600057441": {"Heineken Dutch Grand Prix", "nl-1948"},
	"600057442": {"Pirelli Italian Grand Prix", "it-1922"},
	"600057443": {"Tag Heuer Spanish Grand Prix", "es-2026"},
	"600057444": {"Qatar Airways Azerbaijan Grand Prix", "az-2016"},
	"600057445": {"Singapore Airlines Singapore Grand Prix", "sg-2008"},
	"600057446": {"MSC Cruises United States Grand Prix", "us-2012"},
	"600057447": {"Mexico City Grand Prix", "mx-1962"},
	"600057448": {"MSC Cruises São Paulo Grand Prix", "br-1940"},
	"600057449": {"Heineken Las Vegas Grand Prix", "us-2023"},
	"600057450": {"Qatar Airways Qatar Grand Prix", "qa-2004"},
	"600057451": {"Etihad Airways Abu Dhabi Grand Prix", "ae-2009"},
}

func loadCircuitGeometry(data []byte) map[string]*SportsCircuit {
	var collection struct {
		Features []struct {
			Properties struct {
				ID       string `json:"id"`
				Name     string `json:"Name"`
				Location string `json:"Location"`
				Length   int    `json:"length"`
			} `json:"properties"`
			Geometry struct {
				Type        string       `json:"type"`
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	circuits := map[string]*SportsCircuit{}
	if json.Unmarshal(data, &collection) != nil {
		return circuits
	}
	for _, f := range collection.Features {
		if f.Properties.ID == "" || f.Properties.Name == "" || f.Geometry.Type != "LineString" ||
			len(f.Geometry.Coordinates) < 3 || len(f.Geometry.Coordinates) > 2000 {
			continue
		}
		valid := true
		for _, point := range f.Geometry.Coordinates {
			if math.Abs(point[0]) > 180 || math.Abs(point[1]) > 90 {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		circuits[f.Properties.ID] = &SportsCircuit{
			ID: f.Properties.ID, Name: f.Properties.Name, Location: f.Properties.Location,
			ReferenceLengthMeters: f.Properties.Length, Coordinates: f.Geometry.Coordinates,
			SourceURL:   "https://github.com/bacinger/f1-circuits/blob/394d8fbe70ef2c0b0c8d23ff7bee61fa09606055/circuits/" + f.Properties.ID + ".geojson",
			Attribution: "Circuit geometry © 2019–2025 Tomislav Bacinger · MIT",
		}
	}
	return circuits
}

// attachRaceCircuit only attaches geometry the reference edition actually
// covers and only when the provider title matches the verified name.
func attachRaceCircuit(event *SportsEvent) {
	if event.League != "f1" || event.StartTime.Year() != 2026 {
		return
	}
	link, ok := circuitEventLinks[event.ProviderEventID]
	if !ok || event.Title != link.title {
		return
	}
	event.Circuit = referenceCircuits[link.circuit]
}
