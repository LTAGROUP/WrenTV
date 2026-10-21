// Package tracking reports playback to trackers: Simkl (scrobbling, marking
// watched, importing history) and playback webhooks for anything else.
//
// Players only post their position every ten seconds or so while playing,
// so the player events are inferred: the first report starts a play, a
// report that didn't move or a gap of PauseAfter pauses it, the next moving
// report resumes it, and closing the session (or StopAfter of silence)
// stops it. Reaching the user's watched percentage scrobbles it.
package tracking

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/simkl"
	"github.com/LTAGROUP/wrentv/internal/webhook"
)

// Webhook events.
const (
	EventStart   = "playback.start"
	EventPause   = "playback.pause"
	EventResume  = "playback.resume"
	EventStop    = "playback.stop"
	EventWatched = "playback.watched" // reached the user's watched percentage
	EventMarked  = "item.watched"     // marked watched or unwatched by hand
)

var (
	PauseAfter = 30 * time.Second
	StopAfter  = 10 * time.Minute
)

type Service struct {
	DB     *db.DB
	Simkl  *simkl.Client
	Hooks  *webhook.Sender
	Server func() webhook.Server
	// AddTitle adds a movie or show by TMDB id to the streaming library and
	// returns its item id, so imported history the library lacks can show
	// in Continue Watching. Nil (or an error) when streaming is off.
	AddTitle func(ctx context.Context, kind string, tmdbID int) (string, error)

	mu      sync.Mutex
	plays   map[string]*play
	links   map[string]*pendingLink
	imports map[string]bool
	queues  map[string]chan func()
	now     func() time.Time
	// pollEvery overrides Simkl's sign-in poll interval (tests).
	pollEvery time.Duration
}

func New(d *db.DB, sk *simkl.Client, hooks *webhook.Sender, server func() webhook.Server) *Service {
	return &Service{DB: d, Simkl: sk, Hooks: hooks, Server: server, plays: map[string]*play{},
		links: map[string]*pendingLink{}, imports: map[string]bool{}, queues: map[string]chan func(){}, now: time.Now}
}

// Start runs the watchdog that pauses and stops plays that went quiet.
func (s *Service) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep()
			}
		}
	}()
}

type play struct {
	user      *db.User
	item      *db.Item
	show      *db.Item
	client    string
	pos, dur  float64
	at        time.Time
	paused    bool
	watched   bool // scrobbled this play
	watchedAt float64
}

func key(userID, itemID string) string { return userID + "|" + itemID }

// Progress takes a position report. watchedAt is the user's watched fraction
// and justWatched says this report marked the item watched.
func (s *Service) Progress(ctx context.Context, u *db.User, it *db.Item, pos, dur, watchedAt float64, justWatched bool, client string) {
	if it == nil || (it.Kind != "movie" && it.Kind != "episode") {
		return
	}
	now := s.now()
	s.mu.Lock()
	k := key(u.ID, it.ID)
	p := s.plays[k]
	var events []string
	if p == nil {
		p = &play{user: u, item: it, client: client, pos: pos, dur: dur, at: now, watchedAt: watchedAt}
		s.plays[k] = p
		s.mu.Unlock()
		if it.Kind == "episode" && it.ShowID != "" {
			p.show, _ = s.DB.GetItem(ctx, it.ShowID)
		}
		s.mu.Lock()
		events = append(events, EventStart)
	} else {
		moved := pos - p.pos
		gap := now.Sub(p.at)
		switch {
		case p.paused && math.Abs(moved) >= 1:
			p.paused = false
			events = append(events, EventResume)
		case !p.paused && gap >= 3*time.Second && math.Abs(moved) < 0.5:
			p.paused = true
			events = append(events, EventPause)
		}
		p.pos, p.dur, p.at, p.watchedAt = pos, dur, now, watchedAt
		if client != "" {
			p.client = client
		}
	}
	if justWatched && !p.watched {
		p.watched = true
		events = append(events, EventWatched)
	}
	snap := *p
	s.mu.Unlock()
	for _, e := range events {
		s.emit(e, &snap)
	}
}

// Stopped ends a play: the player closed its session.
func (s *Service) Stopped(userID, itemID string) {
	s.mu.Lock()
	k := key(userID, itemID)
	p := s.plays[k]
	delete(s.plays, k)
	s.mu.Unlock()
	if p != nil {
		s.emit(EventStop, p)
	}
}

func (s *Service) sweep() {
	now := s.now()
	var pause, stop []*play
	s.mu.Lock()
	for k, p := range s.plays {
		switch quiet := now.Sub(p.at); {
		case quiet >= StopAfter:
			delete(s.plays, k)
			stop = append(stop, p)
		case quiet >= PauseAfter && !p.paused:
			p.paused = true
			snap := *p
			pause = append(pause, &snap)
		}
	}
	s.mu.Unlock()
	for _, p := range pause {
		s.emit(EventPause, p)
	}
	for _, p := range stop {
		s.emit(EventStop, p)
	}
}

func (p *play) percent() float64 {
	if p.dur <= 0 {
		return 0
	}
	return math.Round(math.Min(p.pos/p.dur, 1)*1000) / 10
}

// ---------- events ----------

// ItemRef identifies the title in webhook payloads.
type ItemRef struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Year       int    `json:"year,omitempty"`
	TMDBID     int    `json:"tmdbId,omitempty"`
	IMDBID     string `json:"imdbId,omitempty"`
	ShowID     string `json:"showId,omitempty"`
	ShowTitle  string `json:"showTitle,omitempty"`
	ShowTMDBID int    `json:"showTmdbId,omitempty"`
	ShowIMDBID string `json:"showImdbId,omitempty"`
	Season     int    `json:"season,omitempty"`
	Episode    int    `json:"episode,omitempty"`
}

type UserRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PlaybackEvent is the data of the playback.* webhooks.
type PlaybackEvent struct {
	User     UserRef `json:"user"`
	Item     ItemRef `json:"item"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Progress float64 `json:"progress"` // percent
	Client   string  `json:"client,omitempty"`
}

func userRef(u *db.User) UserRef {
	name := u.DisplayName
	for _, v := range []string{u.Username, u.Email, u.ID} {
		if name == "" {
			name = v
		}
	}
	return UserRef{ID: u.ID, Name: name}
}

func itemRef(it, show *db.Item) ItemRef {
	r := ItemRef{ID: it.ID, Kind: it.Kind, Title: it.Title, Year: it.Year, TMDBID: it.TMDBID, IMDBID: it.IMDBID}
	if it.Kind == "episode" || it.Kind == "season" {
		r.ShowID, r.Season, r.Episode = it.ShowID, it.SeasonNumber, it.EpisodeNumber
		if show != nil {
			r.ShowTitle, r.ShowTMDBID, r.ShowIMDBID = show.Title, show.TMDBID, show.IMDBID
			if r.Year == 0 {
				r.Year = show.Year
			}
		}
	}
	return r
}

func (s *Service) emit(event string, p *play) {
	s.send(event, PlaybackEvent{User: userRef(p.user), Item: itemRef(p.item, p.show), Position: p.pos,
		Duration: p.dur, Progress: p.percent(), Client: p.client})
	if s.Simkl.Enabled() {
		s.scrobble(event, p)
	}
}

func (s *Service) send(event string, data any) {
	if s.Hooks == nil {
		return
	}
	env := webhook.Envelope{Event: event, ID: db.NewID(), Data: data}
	if s.Server != nil {
		env.Server = s.Server()
	}
	s.Hooks.Send(env)
}

// Marked records a title marked watched or unwatched by hand.
func (s *Service) Marked(ctx context.Context, u *db.User, it *db.Item, watched bool) {
	var show *db.Item
	if it.ShowID != "" {
		show, _ = s.DB.GetItem(ctx, it.ShowID)
	}
	s.send(EventMarked, struct {
		User    UserRef `json:"user"`
		Item    ItemRef `json:"item"`
		Watched bool    `json:"watched"`
	}{userRef(u), itemRef(it, show), watched})
	if !s.Simkl.Enabled() {
		return
	}
	h, ok := historyFor(it, show)
	if !ok {
		return
	}
	s.withSimkl(u.ID, func(ctx context.Context, access string) error {
		if watched {
			return s.Simkl.AddHistory(ctx, access, h)
		}
		return s.Simkl.RemoveHistory(ctx, access, h)
	})
}

// ---------- Simkl ----------

func media(it *db.Item) *simkl.Media {
	ids := map[string]any{}
	if it.TMDBID > 0 {
		ids["tmdb"] = strconv.Itoa(it.TMDBID)
	}
	if it.IMDBID != "" {
		ids["imdb"] = it.IMDBID
	}
	return &simkl.Media{Title: it.Title, Year: it.Year, IDs: ids}
}

func scrobbleBody(p *play) (simkl.Scrobble, bool) {
	b := simkl.Scrobble{Progress: p.percent()}
	switch p.item.Kind {
	case "movie":
		b.Movie = media(p.item)
		return b, len(b.Movie.IDs) > 0 || b.Movie.Title != ""
	case "episode":
		if p.show == nil {
			return b, false
		}
		b.Show = media(p.show)
		b.Episode = &simkl.Episode{Season: p.item.SeasonNumber, Number: p.item.EpisodeNumber}
		return b, true
	}
	return b, false
}

func historyFor(it, show *db.Item) (simkl.History, bool) {
	var h simkl.History
	switch it.Kind {
	case "movie":
		h.Movies = []simkl.HistoryItem{{Media: *media(it)}}
	case "show":
		h.Shows = []simkl.HistoryItem{{Media: *media(it)}}
	case "season", "episode":
		if show == nil {
			return h, false
		}
		season := simkl.HistorySeason{Number: it.SeasonNumber}
		if it.Kind == "episode" {
			season.Episodes = []simkl.HistoryEpisode{{Number: it.EpisodeNumber}}
		}
		h.Shows = []simkl.HistoryItem{{Media: *media(show), Seasons: []simkl.HistorySeason{season}}}
	default:
		return h, false
	}
	return h, true
}

// scrobble maps a player event onto Simkl's start / pause / stop. Simkl
// marks the title watched on a stop at or past min_progress, which is the
// user's own watched percentage.
func (s *Service) scrobble(event string, p *play) {
	body, ok := scrobbleBody(p)
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
		action = "stop"
		body.Progress = math.Max(body.Progress, math.Ceil(p.watchedAt*100))
	case EventStop:
		if p.watched {
			return
		}
		action = "stop"
	default:
		return
	}
	minProgress := int(math.Round(p.watchedAt * 100))
	s.withSimkl(p.user.ID, func(ctx context.Context, access string) error {
		return s.Simkl.Scrobble(ctx, access, action, minProgress, body)
	})
}

// withSimkl runs fn with the user's token, one call at a time per user
// (Simkl locks each user for a moment per scrobble), refreshing the token
// when it's close to expiring or rejected.
func (s *Service) withSimkl(userID string, fn func(ctx context.Context, access string) error) {
	s.enqueue(userID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		a, err := s.DB.GetSimkl(ctx, userID)
		if err != nil || a == nil || !a.Scrobble {
			return
		}
		if a.RefreshToken != "" && a.ExpiresAt > 0 && a.ExpiresAt-s.now().Unix() < 24*3600 {
			if err := s.refresh(ctx, a); err != nil {
				slog.Warn("simkl refresh", "user", userID, "err", err)
			}
		}
		err = fn(ctx, a.AccessToken)
		if errors.Is(err, simkl.ErrUnauthorized) && a.RefreshToken != "" {
			if s.refresh(ctx, a) == nil {
				err = fn(ctx, a.AccessToken)
			}
		}
		if err != nil {
			slog.Warn("simkl", "user", userID, "err", err)
		}
	})
}

func (s *Service) refresh(ctx context.Context, a *db.SimklAccount) error {
	t, err := s.Simkl.Refresh(ctx, a.RefreshToken)
	if err != nil {
		return err
	}
	a.AccessToken, a.ExpiresAt = t.Access, t.Expires
	if t.Refresh != "" {
		a.RefreshToken = t.Refresh
	}
	return s.DB.SetSimklTokens(ctx, a.UserID, a.AccessToken, a.RefreshToken, a.ExpiresAt)
}

func (s *Service) enqueue(userID string, fn func()) {
	s.mu.Lock()
	q := s.queues[userID]
	if q == nil {
		q = make(chan func(), 32)
		s.queues[userID] = q
		go func() {
			for {
				select {
				case f := <-q:
					f()
				case <-time.After(time.Minute):
					s.mu.Lock()
					if len(q) == 0 {
						delete(s.queues, userID)
						s.mu.Unlock()
						return
					}
					s.mu.Unlock()
				}
			}
		}()
	}
	select { // under the lock, so the worker can't retire q meanwhile
	case q <- fn:
	default:
		slog.Warn("simkl queue full; dropping event", "user", userID)
	}
	s.mu.Unlock()
}
