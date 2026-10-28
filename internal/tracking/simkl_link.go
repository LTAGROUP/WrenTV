package tracking

import (
	"cmp"
	"context"
	"errors"
	"strconv"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/simkl"
)

// simklTracker is Simkl for the shared sign-in and import code.
type simklTracker struct{ c *simkl.Client }

func (simklTracker) id() string    { return db.Simkl }
func (simklTracker) label() string { return "Simkl" }
func (t simklTracker) on() bool    { return t.c.Enabled() }

func (t simklTracker) start(ctx context.Context) (device, error) {
	d, err := t.c.StartDevice(ctx)
	if d.URL == "" {
		d.URL = "https://simkl.com/pin"
	}
	return device{code: d.DeviceCode, userCode: d.UserCode, url: d.URL, urlFull: d.URLFull, expiresIn: d.ExpiresIn, interval: d.Interval}, err
}

func (t simklTracker) poll(ctx context.Context, d device) (token, error) {
	tok, err := t.c.PollDevice(ctx, d.code)
	switch {
	case errors.Is(err, simkl.ErrPending):
		return token{}, errPending
	case errors.Is(err, simkl.ErrSlowDown):
		return token{}, errSlowDown
	case errors.Is(err, simkl.ErrExpired):
		return token{}, errExpired
	}
	return token{tok.Access, tok.Refresh, tok.Expires}, err
}

func (t simklTracker) renew(ctx context.Context, refresh string) (token, error) {
	tok, err := t.c.Refresh(ctx, refresh)
	return token{tok.Access, tok.Refresh, tok.Expires}, err
}

func (t simklTracker) username(ctx context.Context, access string) (string, error) {
	return t.c.Username(ctx, access)
}

func (simklTracker) rejected(err error) bool { return errors.Is(err, simkl.ErrUnauthorized) }

func simklRef(md *simkl.Media) extRef {
	if md == nil {
		return extRef{}
	}
	id, _ := strconv.Atoi(md.ID("tmdb"))
	return extRef{tmdb: id, imdb: md.ID("imdb"), title: md.Title, year: md.Year}
}

func (t simklTracker) history(ctx context.Context, call caller) (history, error) {
	var h history
	fetch := func(kind, status string) (out simkl.AllItems, err error) {
		err = call(func(access string) (err error) {
			out, err = t.c.AllItems(ctx, access, kind, status)
			return err
		})
		return out, err
	}
	got, err := fetch("movies", "completed")
	if err != nil {
		return h, err
	}
	for _, li := range got.Movies {
		if li.Movie != nil {
			h.movies = append(h.movies, watchedTitle{ref: simklRef(li.Movie), at: when(li.LastWatchedAt)})
		}
	}
	// Episodes watched, in every list a show can be in.
	for _, kind := range []string{"shows", "anime"} {
		for _, status := range []string{"watching", "completed", "hold", "dropped"} {
			got, err := fetch(kind, status)
			if err != nil {
				return h, err
			}
			for _, li := range append(got.Shows, got.Anime...) {
				if li.Show == nil {
					continue
				}
				w := watchedTitle{ref: simklRef(li.Show), at: when(li.LastWatchedAt), completed: status == "completed"}
				for _, se := range li.Seasons {
					for _, ep := range se.Episodes {
						w.eps = append(w.eps, watchedEp{se.Number, ep.Number, when(ep.WatchedAt)})
					}
				}
				h.shows = append(h.shows, w)
			}
		}
	}
	return h, nil
}

func (t simklTracker) paused(ctx context.Context, call caller) ([]pausedTitle, error) {
	var got []simkl.Playback
	if err := call(func(access string) (err error) {
		got, err = t.c.Playbacks(ctx, access)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]pausedTitle, 0, len(got))
	for _, pb := range got {
		p := pausedTitle{percent: pb.Progress, at: when(pb.When())}
		if pb.Movie != nil {
			p.movie, p.ref = true, simklRef(pb.Movie)
		} else if show := cmp.Or(pb.Show, pb.Anime); show != nil {
			p.ref = simklRef(show)
			p.season, p.number = pb.SeasonEpisode()
		} else {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// SimklWatchlist lists one status ("plantowatch", "watching", "completed"…)
// of a user's Simkl watchlist across movies, shows and anime, refreshing
// the token when it's rejected.
func (s *Service) SimklWatchlist(ctx context.Context, userID, status string) (simkl.AllItems, error) {
	var out simkl.AllItems
	if !s.Simkl.Enabled() {
		return out, errors.New("Simkl needs a client ID (Settings)")
	}
	a, err := s.DB.GetSimkl(ctx, userID)
	if err != nil || a == nil {
		return out, errors.New("link a Simkl account first (Account settings)")
	}
	t := simklTracker{s.Simkl}
	for _, kind := range []string{"movies", "shows", "anime"} {
		l, err := s.Simkl.Watchlist(ctx, a.AccessToken, kind, status)
		if errors.Is(err, simkl.ErrUnauthorized) && a.RefreshToken != "" && s.renew(ctx, t, a) == nil {
			l, err = s.Simkl.Watchlist(ctx, a.AccessToken, kind, status)
		}
		if err != nil {
			return out, err
		}
		out.Movies = append(out.Movies, l.Movies...)
		out.Shows = append(out.Shows, l.Shows...)
		out.Anime = append(out.Anime, l.Anime...)
	}
	return out, nil
}
