package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
)

// creditView is a title on a person's page, with who they played (or their
// job, for crew).
type creditView struct {
	titleView
	Role string `json:"role,omitempty"`
}

type creditRow struct {
	Title   string       `json:"title"`
	Credits []creditView `json:"credits"`
}

type personView struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Biography    string `json:"biography,omitempty"`
	Birthday     string `json:"birthday,omitempty"`
	Deathday     string `json:"deathday,omitempty"`
	PlaceOfBirth string `json:"placeOfBirth,omitempty"`
	ProfilePath  string `json:"profilePath,omitempty"`
	// KnownFor is TMDB's department for them: "Acting", "Directing"…
	KnownFor     string `json:"knownFor,omitempty"`
	IMDBID       string `json:"imdbId,omitempty"`
	BackdropPath string `json:"backdropPath,omitempty"`
	// Rows: Known for, In your library, Movies, TV Shows.
	Rows []creditRow `json:"rows"`
}

// TV genres that are appearances, not roles: news and talk shows.
var appearanceGenres = []int{10763, 10767}

const maxCreditRow = 80

// handlePerson returns an actor or crew member's details and filmography,
// with each title linked to the library when the server has it.
func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad person id")
		return
	}
	if !s.TMDB.Enabled() {
		writeErr(w, 404, "person details need TMDB")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	p, err := s.TMDB.Person(ctx, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		writeErr(w, 404, "person not found")
		return
	}
	if err != nil {
		writeErr(w, 502, "person unavailable: "+err.Error())
		return
	}
	out := personView{ID: p.ID, Name: p.Name, Biography: strings.TrimSpace(p.Biography), Birthday: p.Birthday, Deathday: p.Deathday,
		PlaceOfBirth: p.PlaceOfBirth, ProfilePath: p.ProfilePath, KnownFor: p.KnownForDepartment, IMDBID: p.ExternalIDs.IMDBID, Rows: []creditRow{}}

	// One entry per title: cast roles, plus crew jobs in the department
	// they're known for (a director's films).
	type entry struct {
		c     tmdb.Credit
		roles []string
	}
	var order []string
	byKey := map[string]*entry{}
	add := func(c tmdb.Credit, role string) {
		if c.MediaType != "movie" && c.MediaType != "tv" {
			return
		}
		if c.MediaType == "tv" && slices.ContainsFunc(c.GenreIDs, func(g int) bool { return slices.Contains(appearanceGenres, g) }) {
			return
		}
		k := c.MediaType + ":" + strconv.Itoa(c.ID)
		e := byKey[k]
		if e == nil {
			e = &entry{c: c}
			byKey[k] = e
			order = append(order, k)
		}
		if role = strings.TrimSpace(role); role != "" && !slices.Contains(e.roles, role) {
			e.roles = append(e.roles, role)
		}
	}
	for _, c := range p.CombinedCredits.Cast {
		add(c, c.Character)
	}
	if p.KnownForDepartment != "" && p.KnownForDepartment != "Acting" {
		for _, c := range p.CombinedCredits.Crew {
			if c.Department == p.KnownForDepartment {
				add(c, c.Job)
			}
		}
	}
	views := make([]titleView, 0, len(order))
	for _, k := range order {
		views = append(views, viewOf(byKey[k].c.Title, ""))
	}
	views = s.linkLibrary(ctx, r, views, false)
	credits := make([]creditView, len(views))
	for i, v := range views {
		credits[i] = creditView{titleView: v, Role: strings.Join(byKey[order[i]].roles, " / ")}
	}

	date := func(i int) string {
		c := byKey[order[i]].c
		if c.ReleaseDate != "" {
			return c.ReleaseDate
		}
		return c.FirstAirDate
	}
	idx := make([]int, len(credits))
	for i := range idx {
		idx[i] = i
	}
	pick := func(keep func(i int) bool, less func(a, b int) bool, limit int) []creditView {
		var is []int
		for _, i := range idx {
			if keep(i) {
				is = append(is, i)
			}
		}
		sort.SliceStable(is, func(a, b int) bool { return less(is[a], is[b]) })
		if len(is) > limit {
			is = is[:limit]
		}
		row := make([]creditView, len(is))
		for n, i := range is {
			row[n] = credits[i]
		}
		return row
	}
	votes := func(a, b int) bool { return byKey[order[a]].c.VoteCount > byKey[order[b]].c.VoteCount }
	// Newest first; titles without a date yet (announced) go last.
	newest := func(a, b int) bool {
		x, y := date(a), date(b)
		if (x == "") != (y == "") {
			return x != ""
		}
		return x > y
	}
	addRow := func(title string, row []creditView) {
		if len(row) > 0 {
			out.Rows = append(out.Rows, creditRow{Title: title, Credits: row})
		}
	}
	addRow("Known for", pick(func(i int) bool { return credits[i].PosterPath != "" && byKey[order[i]].c.VoteCount > 0 }, votes, 12))
	addRow("In your library", pick(func(i int) bool { return credits[i].Item != nil }, newest, maxCreditRow))
	addRow("Movies", pick(func(i int) bool { return credits[i].MediaType == "movie" }, newest, maxCreditRow))
	addRow("TV Shows", pick(func(i int) bool { return credits[i].MediaType == "show" }, newest, maxCreditRow))
	if len(out.Rows) > 0 && len(out.Rows[0].Credits) > 0 {
		for _, c := range out.Rows[0].Credits {
			if c.BackdropPath != "" {
				out.BackdropPath = c.BackdropPath
				break
			}
		}
	}
	writeJSON(w, 200, out)
}
