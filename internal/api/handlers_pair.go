package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// TV sign-in. Typing an email and password with a remote is slow, so a TV
// asks for a short code, shows it with a QR code of <server>/link?code=…, and
// polls. Anyone signed in on a phone or browser (the web app's /link page or
// the iOS app's scanner) approves it, and the TV is signed in as them.

const (
	pairTTL     = 10 * time.Minute
	pairMax     = 200 // codes waiting at once
	pairAlpha   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pairCodeLen = 8
)

type pairing struct {
	code    string
	secret  string
	device  string
	expires time.Time
	// Set once approved; handed to the device on its next poll.
	token string
	user  *userResponse
}

type pairings struct {
	mu sync.Mutex
	m  map[string]*pairing
}

func (s *Server) pairings() *pairings {
	s.pairsOnce.Do(func() { s.pairs = &pairings{m: map[string]*pairing{}} })
	return s.pairs
}

// get returns a live pairing; the caller holds the lock.
func (p *pairings) get(code string) *pairing {
	now := time.Now()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
	return p.m[normalizePairCode(code)]
}

// normalizePairCode accepts "abcd-efgh", "ABCD EFGH" and so on.
func normalizePairCode(c string) string {
	c = strings.ToUpper(c)
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(pairAlpha, r) {
			return r
		}
		return -1
	}, c)
}

func newPairCode() string {
	b := make([]byte, pairCodeLen)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairAlpha[int(b[i])%len(pairAlpha)]
	}
	return string(b)
}

func (s *Server) handleCreatePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"device"`
	}
	_ = decode(r, &req)
	device := strings.TrimSpace(req.Device)
	if device == "" {
		device = "TV"
	}
	if len(device) > 60 {
		device = device[:60]
	}
	sec := make([]byte, 24)
	_, _ = rand.Read(sec)
	p := s.pairings()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.get("")
	if len(p.m) >= pairMax {
		writeErr(w, http.StatusTooManyRequests, "too many sign-in codes waiting; try again in a few minutes")
		return
	}
	code := newPairCode()
	for p.m[code] != nil {
		code = newPairCode()
	}
	pr := &pairing{code: code, secret: hex.EncodeToString(sec), device: device, expires: time.Now().Add(pairTTL)}
	p.m[code] = pr
	writeJSON(w, 200, map[string]any{
		"code": code, "secret": pr.secret, "expiresIn": int(pairTTL.Seconds()),
		"path": "/link?code=" + code,
	})
}

// handlePairInfo tells the approving page which device is asking.
func (s *Server) handlePairInfo(w http.ResponseWriter, r *http.Request) {
	p := s.pairings()
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.get(chi.URLParam(r, "code"))
	if pr == nil {
		writeErr(w, 404, "this code has expired; get a new one on the TV")
		return
	}
	writeJSON(w, 200, map[string]any{"device": pr.device, "approved": pr.token != "", "expiresIn": int(time.Until(pr.expires).Seconds())})
}

// handlePollPair is the device waiting for approval; it proves it asked for
// the code with the secret it was given.
func (s *Server) handlePollPair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secret string `json:"secret"`
	}
	_ = decode(r, &req)
	p := s.pairings()
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.get(chi.URLParam(r, "code"))
	if pr == nil || subtle.ConstantTimeCompare([]byte(pr.secret), []byte(req.Secret)) != 1 {
		writeErr(w, 404, "this code has expired")
		return
	}
	if pr.token == "" {
		writeJSON(w, 200, map[string]any{"status": "pending"})
		return
	}
	delete(p.m, pr.code)
	writeJSON(w, 200, map[string]any{"status": "approved", "token": pr.token, "user": pr.user})
}

// handleApprovePair signs the waiting device in as the caller.
func (s *Server) handleApprovePair(w http.ResponseWriter, r *http.Request) {
	u := principal(r)
	p := s.pairings()
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.get(chi.URLParam(r, "code"))
	if pr == nil {
		writeErr(w, 404, "this code has expired; get a new one on the TV")
		return
	}
	if pr.token != "" {
		writeErr(w, http.StatusConflict, "this code was already used")
		return
	}
	tok, err := s.Devices.Issue(u.User, pr.device)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	pr.token = tok
	pr.user = &userResponse{u.User, u.Role}
	writeJSON(w, 200, map[string]any{"device": pr.device})
}
