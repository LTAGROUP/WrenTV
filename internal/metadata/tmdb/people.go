package tmdb

import (
	"context"
	"net/url"
	"strconv"
)

// Credit is one movie or show in a person's combined credits: a role
// (Character) for cast, a Job for crew.
type Credit struct {
	Title
	Character    string `json:"character"`
	Job          string `json:"job"`
	Department   string `json:"department"`
	EpisodeCount int    `json:"episode_count"`
}

// Person is an actor, director or other crew member with their credits.
type Person struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Biography          string   `json:"biography"`
	Birthday           string   `json:"birthday"`
	Deathday           string   `json:"deathday"`
	PlaceOfBirth       string   `json:"place_of_birth"`
	ProfilePath        string   `json:"profile_path"`
	KnownForDepartment string   `json:"known_for_department"`
	AlsoKnownAs        []string `json:"also_known_as"`
	CombinedCredits    struct {
		Cast []Credit `json:"cast"`
		Crew []Credit `json:"crew"`
	} `json:"combined_credits"`
	ExternalIDs struct {
		IMDBID string `json:"imdb_id"`
	} `json:"external_ids"`
}

// Person fetches a person's details and every movie and show they're in.
func (c *Client) Person(ctx context.Context, id int) (*Person, error) {
	var p Person
	err := c.get(ctx, "/person/"+strconv.Itoa(id), url.Values{"append_to_response": {"combined_credits,external_ids"}}, extrasTTL, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
