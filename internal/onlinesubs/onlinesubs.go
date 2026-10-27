// Package onlinesubs searches subtitle sites (OpenSubtitles, SubDL) for a
// title by IMDb id and fetches a pick as WebVTT, for files that came without
// the subtitles someone wants.
package onlinesubs

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Query is the title subtitles are wanted for: a movie (Season and Episode
// < 0) or an episode by its show's IMDb id.
type Query struct {
	IMDBID    string
	Season    int
	Episode   int
	Languages []string // ISO 639-1, most wanted first; empty is any
}

func (q Query) episode() bool { return q.Season >= 0 && q.Episode >= 0 }

// Result is one subtitle a provider has. ID is what Fetch takes back.
type Result struct {
	Provider        string `json:"provider"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Language        string `json:"language"`
	Downloads       int    `json:"downloads,omitempty"`
	HearingImpaired bool   `json:"hearingImpaired,omitempty"`
	Machine         bool   `json:"machineTranslated,omitempty"`
}

type provider interface {
	name() string
	search(ctx context.Context, q Query) ([]Result, error)
	download(ctx context.Context, id string, q Query) ([]byte, error)
}

// ErrNone is returned when no provider has a key.
var ErrNone = errors.New("no subtitle providers are set up (Settings → Playback)")

// Service searches every provider that has a key. Cfg is read per call, so
// a key saved in Settings applies at once.
type Service struct {
	Cfg       func() config.SubtitlesConfig
	UserAgent string

	http *http.Client
	mu   sync.Mutex
	os   *openSubtitles // keeps its sign-in between calls
}

func New(cfg func() config.SubtitlesConfig, userAgent string) *Service {
	return &Service{Cfg: cfg, UserAgent: userAgent, http: &http.Client{Timeout: 20 * time.Second}}
}

func (s *Service) providers() []provider {
	if s == nil || s.Cfg == nil {
		return nil
	}
	c := s.Cfg()
	var out []provider
	if c.OpenSubtitles.APIKey != "" {
		s.mu.Lock()
		if s.os == nil || s.os.cfg != c.OpenSubtitles {
			s.os = &openSubtitles{cfg: c.OpenSubtitles, http: s.http, ua: s.UserAgent}
		}
		out = append(out, s.os)
		s.mu.Unlock()
	}
	if c.SubDL.APIKey != "" {
		out = append(out, &subDL{key: c.SubDL.APIKey, http: s.http, ua: s.UserAgent})
	}
	return out
}

// Enabled reports whether any provider has a key.
func (s *Service) Enabled() bool { return len(s.providers()) > 0 }

// Search asks every provider at once. Results come in the order of the
// wanted languages, then most downloaded; one provider failing only drops
// its results.
func (s *Service) Search(ctx context.Context, q Query) ([]Result, error) {
	ps := s.providers()
	if len(ps) == 0 {
		return nil, ErrNone
	}
	if !strings.HasPrefix(q.IMDBID, "tt") {
		return nil, errors.New("this title has no IMDb id to search by")
	}
	type answer struct {
		rs  []Result
		err error
	}
	answers := make([]answer, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs, err := p.search(ctx, q)
			answers[i] = answer{rs, err}
		}()
	}
	wg.Wait()
	var out []Result
	var firstErr error
	failed := 0
	for _, a := range answers {
		if a.err != nil {
			failed++
			firstErr = cmpErr(firstErr, a.err)
			continue
		}
		out = append(out, a.rs...)
	}
	if failed == len(ps) {
		return nil, firstErr
	}
	rank := map[string]int{}
	for i, l := range q.Languages {
		if _, ok := rank[l]; !ok {
			rank[l] = i
		}
	}
	langRank := func(l string) int {
		if r, ok := rank[l]; ok {
			return r
		}
		return len(rank)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := langRank(a.Language), langRank(b.Language); ra != rb {
			return ra < rb
		}
		if a.Machine != b.Machine {
			return !a.Machine
		}
		return a.Downloads > b.Downloads
	})
	return out, nil
}

func cmpErr(have, err error) error {
	if have != nil {
		return have
	}
	return err
}

// Fetch downloads one search result as WebVTT.
func (s *Service) Fetch(ctx context.Context, providerName, id string, q Query) ([]byte, error) {
	for _, p := range s.providers() {
		if p.name() == providerName {
			b, err := p.download(ctx, id, q)
			if err != nil {
				return nil, err
			}
			return ToVTT(b)
		}
	}
	return nil, errors.New("unknown subtitle provider")
}
