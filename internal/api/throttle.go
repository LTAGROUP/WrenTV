package api

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sign-in cooldowns for local accounts. Wrong passwords are counted per
// username from one address, per address, and per username from anywhere;
// past a few, the next try has to wait, longer each time, up to 15 minutes.
// A correct password clears the count for that username from that address.
const (
	throttlePairFree = 5  // wrong tries for one username from one address before waiting
	throttleWideFree = 20 // wrong tries from one address, or for one username from anywhere
	throttleBase     = 30 * time.Second
	throttleMax      = 15 * time.Minute
	throttleForget   = time.Hour
	throttleEntries  = 20000
)

type attempts struct {
	fails int
	until time.Time
	last  time.Time
}

type loginThrottle struct {
	mu  sync.Mutex
	m   map[string]*attempts
	now func() time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{m: map[string]*attempts{}, now: time.Now}
}

type throttleKey struct {
	key  string
	free int
}

func throttleKeys(username, ip string) []throttleKey {
	u := strings.ToLower(strings.TrimSpace(username))
	if len(u) > 64 {
		u = u[:64]
	}
	return []throttleKey{
		{"pair:" + ip + "|" + u, throttlePairFree},
		{"ip:" + ip, throttleWideFree},
		{"user:" + u, throttleWideFree},
	}
}

// wait is how long this sign-in must still wait, or 0.
func (t *loginThrottle) wait(username, ip string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var w time.Duration
	for _, k := range throttleKeys(username, ip) {
		if a := t.m[k.key]; a != nil && a.until.After(now) {
			w = max(w, a.until.Sub(now))
		}
	}
	return w
}

// failed counts a wrong password.
func (t *loginThrottle) failed(username, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if len(t.m) >= throttleEntries {
		for k, a := range t.m {
			if now.Sub(a.last) > throttleForget {
				delete(t.m, k)
			}
		}
		if len(t.m) >= throttleEntries {
			t.m = map[string]*attempts{}
		}
	}
	for _, k := range throttleKeys(username, ip) {
		a := t.m[k.key]
		if a == nil || now.Sub(a.last) > throttleForget {
			a = &attempts{}
			t.m[k.key] = a
		}
		a.fails++
		a.last = now
		if over := a.fails - k.free; over >= 0 {
			d := throttleMax
			if over < 6 {
				d = min(throttleBase<<over, throttleMax)
			}
			a.until = now.Add(d)
		}
	}
}

// succeeded clears the count for this username from this address.
func (t *loginThrottle) succeeded(username, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, throttleKeys(username, ip)[0].key)
}

func waitMessage(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("Too many sign-in attempts. Try again in %d seconds.", int(d.Seconds())+1)
	}
	return fmt.Sprintf("Too many sign-in attempts. Try again in %d minutes.", int(d.Minutes())+1)
}
