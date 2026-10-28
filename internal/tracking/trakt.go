package tracking

import (
	"context"
	"errors"
	"math"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/trakt"
)

// traktTracker is Trakt for the shared sign-in and import code.
type traktTracker struct{ c *trakt.Client }

func (traktTracker) id() string    { return db.Trakt }
func (traktTracker) label() string { return "Trakt" }
func (t traktTracker) on() bool    { return t.c.Enabled() }

func (t traktTracker) start(ctx context.Context) (device, error) {
	d, err := t.c.StartDevice(ctx)
	return device{code: d.DeviceCode, userCode: d.UserCode, url: d.URL, urlFull: d.URL, expiresIn: d.ExpiresIn, interval: d.Interval}, err
}

func (t traktTracker) poll(ctx context.Context, d device) (token, error) {
	tok, err := t.c.PollDevice(ctx, d.code)
	switch {
	case errors.Is(err, trakt.ErrPending):
		return token{}, errPending
	case errors.Is(err, trakt.ErrSlowDown):
		return token{}, errSlowDown
	case errors.Is(err, trakt.ErrExpired):
		return token{}, errExpired
	}
	return token{tok.Access, tok.Refresh, tok.Expires}, err
}

func (t traktTracker) renew(ctx context.Context, refresh string) (token, error) {
	tok, err := t.c.Refresh(ctx, refresh)
	return token{tok.Access, tok.Refresh, tok.Expires}, err
}

func (t traktTracker) username(ctx context.Context, access string) (string, error) {
	return t.c.Username(ctx, access)
}

func (traktTracker) rejected(err error) bool { return errors.Is(err, trakt.ErrUnauthorized) }

func traktRef(m *trakt.Media) extRef {
	if m == nil {
		return extRef{}
	}
	return extRef{tmdb: m.IDs.TMDB, imdb: m.IDs.IMDB, title: m.Title, year: m.Year}
}

func (t traktTracker) history(ctx context.Context, call caller) (history, error) {
	var h history
	var movies []trakt.WatchedMovie
	if err := call(func(access string) (err error) {
		movies, err = t.c.WatchedMovies(ctx, access)
		return err
	}); err != nil {
		return h, err
	}
	for _, w := range movies {
		h.movies = append(h.movies, watchedTitle{ref: traktRef(&w.Movie), at: when(w.LastWatchedAt)})
	}
	var shows []trakt.WatchedShow
	if err := call(func(access string) (err error) {
		shows, err = t.c.WatchedShows(ctx, access)
		return err
	}); err != nil {
		return h, err
	}
	for _, w := range shows {
		wt := watchedTitle{ref: traktRef(&w.Show), at: when(w.LastWatchedAt)}
		for _, se := range w.Seasons {
			for _, ep := range se.Episodes {
				wt.eps = append(wt.eps, watchedEp{se.Number, ep.Number, when(ep.LastWatchedAt)})
			}
		}
		h.shows = append(h.shows, wt)
	}
	return h, nil
}

func (t traktTracker) paused(ctx context.Context, call caller) ([]pausedTitle, error) {
	var got []trakt.Playback
	if err := call(func(access string) (err error) {
		got, err = t.c.Playbacks(ctx, access)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]pausedTitle, 0, len(got))
	for _, pb := range got {
		p := pausedTitle{percent: pb.Progress, at: when(pb.PausedAt)}
		switch {
		case pb.Movie != nil:
			p.movie, p.ref = true, traktRef(pb.Movie)
		case pb.Show != nil && pb.Episode != nil:
			p.ref, p.season, p.number = traktRef(pb.Show), pb.Episode.Season, pb.Episode.Number
		default:
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// ---------- scrobbling ----------

// traktWatched is the progress at which Trakt counts a stop as watched.
const traktWatched = 80

func traktMedia(it *db.Item) *trakt.Media {
	return &trakt.Media{Title: it.Title, Year: it.Year, IDs: trakt.IDs{TMDB: it.TMDBID, IMDB: it.IMDBID}}
}

func traktScrobbleBody(p *play) (trakt.Scrobble, bool) {
	b := trakt.Scrobble{Progress: p.percent()}
	switch p.item.Kind {
	case "movie":
		b.Movie = traktMedia(p.item)
		return b, b.Movie.IDs.TMDB > 0 || b.Movie.IDs.IMDB != ""
	case "episode":
		if p.show == nil || (p.show.TMDBID <= 0 && p.show.IMDBID == "") {
			return b, false
		}
		b.Show = traktMedia(p.show)
		b.Episode = &trakt.Episode{Season: p.item.SeasonNumber, Number: p.item.EpisodeNumber}
		return b, true
	}
	return b, false
}

func traktHistoryFor(it, show *db.Item) (trakt.History, bool) {
	var h trakt.History
	switch it.Kind {
	case "movie":
		h.Movies = []trakt.HistoryItem{{Media: *traktMedia(it)}}
	case "show":
		h.Shows = []trakt.HistoryItem{{Media: *traktMedia(it)}}
	case "season", "episode":
		if show == nil {
			return h, false
		}
		season := trakt.HistorySeason{Number: it.SeasonNumber}
		if it.Kind == "episode" {
			season.Episodes = []trakt.HistoryEpisode{{Number: it.EpisodeNumber}}
		}
		h.Shows = []trakt.HistoryItem{{Media: *traktMedia(show), Seasons: []trakt.HistorySeason{season}}}
	default:
		return h, false
	}
	return h, true
}

// scrobbleTrakt maps a player event onto Trakt's start / pause / stop.
// Trakt marks a title watched on a stop at 80% or more, whatever the user's
// own watched percentage: a play the user counts as watched below that is
// added to their history instead, and one they don't count as watched
// stops just short of it so it stays a paused playback.
func (s *Service) scrobbleTrakt(event string, p *play) {
	body, ok := traktScrobbleBody(p)
	if !ok {
		return
	}
	var action string
	switch event {
	case EventStart, EventResume:
		if p.watched {
			return
		}
		action = "start"
	case EventPause:
		if p.watched {
			return
		}
		action = "pause"
	case EventWatched:
		body.Progress = math.Max(body.Progress, math.Ceil(p.watchedAt*100))
		if body.Progress < traktWatched {
			if h, ok := traktHistoryFor(p.item, p.show); ok {
				s.withAccount(db.Trakt, p.user.ID, func(ctx context.Context, access string) error {
					return s.Trakt.AddHistory(ctx, access, h)
				})
			}
			return
		}
		action = "stop"
	case EventStop:
		if p.watched {
			return
		}
		action = "stop"
		body.Progress = math.Min(body.Progress, traktWatched-0.1)
	default:
		return
	}
	s.withAccount(db.Trakt, p.user.ID, func(ctx context.Context, access string) error {
		return s.Trakt.Scrobble(ctx, access, action, body)
	})
}

// markTrakt adds a title marked watched by hand to the user's Trakt
// history, or removes it.
func (s *Service) markTrakt(u *db.User, it, show *db.Item, watched bool) {
	h, ok := traktHistoryFor(it, show)
	if !ok {
		return
	}
	s.withAccount(db.Trakt, u.ID, func(ctx context.Context, access string) error {
		if watched {
			return s.Trakt.AddHistory(ctx, access, h)
		}
		return s.Trakt.RemoveHistory(ctx, access, h)
	})
}

// TraktWatchlist lists a user's Trakt watchlist, refreshing the token when
// it's rejected.
func (s *Service) TraktWatchlist(ctx context.Context, userID string) ([]trakt.ListItem, error) {
	if !s.Trakt.Enabled() {
		return nil, errors.New("Trakt needs a client ID and secret (Settings)")
	}
	a, err := s.DB.GetTracker(ctx, db.Trakt, userID)
	if err != nil || a == nil {
		return nil, errors.New("link a Trakt account first (Account settings)")
	}
	l, err := s.Trakt.Watchlist(ctx, a.AccessToken)
	if errors.Is(err, trakt.ErrUnauthorized) && a.RefreshToken != "" && s.renew(ctx, traktTracker{s.Trakt}, a) == nil {
		l, err = s.Trakt.Watchlist(ctx, a.AccessToken)
	}
	return l, err
}
