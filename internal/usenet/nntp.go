// Package usenet streams video straight from Usenet: it reads NZBs from
// Newznab indexers, fetches the articles a player asks for from the owner's
// news servers and decodes them as they arrive, so nothing is downloaded
// first. Releases packed in uncompressed RAR volumes play too.
package usenet

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrMissing means no server has the article (expired or taken down).
	ErrMissing = errors.New("article missing")
	// ErrAuth means a news server refused the login.
	ErrAuth = errors.New("news server login failed")
	// ErrTooMany means the account has every connection it may open in use.
	ErrTooMany = errors.New("news server: too many connections")
)

// Server is a news server account.
type Server struct {
	Host     string
	Port     int
	TLS      bool
	User     string
	Pass     string
	MaxConns int
	// Backup servers are asked only for articles the others lack or
	// can't serve: a block account kept for gaps.
	Backup bool
}

func (s Server) addr() string {
	port := s.Port
	if port == 0 {
		port = map[bool]int{true: 563, false: 119}[s.TLS]
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(port))
}

// conn is one logged-in connection to a news server.
type conn struct {
	nc   net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	used time.Time

	// dl guards the deadline: a read in progress pushes it on, a
	// cancelled one pulls it in, and a cancel must win.
	dl     sync.Mutex
	hard   time.Time // the latest the current command may run to
	cut    bool
	stall  time.Duration
	stallT time.Time // when the read deadline was last pushed on
}

// stallFor is how long an article may go without a byte arriving before
// the connection is given up: a server that stops sending mid-article
// would otherwise hold the read (and a connection) for a minute.
var stallFor = 20 * time.Second

func dial(ctx context.Context, s Server) (*conn, error) {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var nc net.Conn
	var err error
	if s.TLS {
		nc, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: s.Host}}).DialContext(ctx, "tcp", s.addr())
	} else {
		nc, err = d.DialContext(ctx, "tcp", s.addr())
	}
	if err != nil {
		return nil, err
	}
	c := &conn{nc: nc, r: bufio.NewReaderSize(nc, 64<<10), w: bufio.NewWriter(nc), used: time.Now()}
	stop := c.deadline(ctx, 20*time.Second)
	defer stop()
	code, msg, err := c.status()
	if err == nil && code != 200 && code != 201 {
		err = fmt.Errorf("news server: %d %s", code, msg)
		if strings.Contains(strings.ToLower(msg), "connections") {
			err = fmt.Errorf("%w: %d %s", ErrTooMany, code, msg)
		}
	}
	if err == nil && s.User != "" {
		err = c.login(s.User, s.Pass)
	}
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

func (c *conn) login(user, pass string) error {
	code, msg, err := c.cmd("AUTHINFO USER " + user)
	if err != nil {
		return err
	}
	if code == 381 {
		code, msg, err = c.cmd("AUTHINFO PASS " + pass)
		if err != nil {
			return err
		}
	}
	if code == 482 || code == 502 && strings.Contains(strings.ToLower(msg), "connections") {
		return fmt.Errorf("%w: %d %s", ErrTooMany, code, msg)
	}
	if code != 281 {
		return fmt.Errorf("%w: %d %s", ErrAuth, code, msg)
	}
	return nil
}

// deadline bounds the connection by ctx and an idle timeout; stop clears it.
// Reads also fail once nothing has arrived for stallFor (see progress).
func (c *conn) deadline(ctx context.Context, d time.Duration) (stop func()) {
	t := time.Now().Add(d)
	if dl, ok := ctx.Deadline(); ok && dl.Before(t) {
		t = dl
	}
	c.dl.Lock()
	c.hard, c.cut, c.stall = t, false, stallFor
	c.stallT = time.Time{}
	_ = c.nc.SetDeadline(c.soon(time.Now()))
	c.dl.Unlock()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.dl.Lock()
			// A command that finished as ctx ended is left alone: the
			// connection may already be back in the pool, or running the
			// next command.
			select {
			case <-done:
			default:
				c.cut = true
				_ = c.nc.SetDeadline(time.Unix(1, 0)) // unblock reads now
			}
			c.dl.Unlock()
		case <-done:
		}
	}()
	return func() {
		c.dl.Lock()
		close(done)
		c.hard, c.cut, c.stall = time.Time{}, false, 0
		_ = c.nc.SetDeadline(time.Time{})
		c.dl.Unlock()
	}
}

// soon is the read deadline from now: a stall's worth, never past hard.
func (c *conn) soon(now time.Time) time.Time {
	if c.stall <= 0 || c.hard.IsZero() {
		return c.hard
	}
	return minTime(now.Add(c.stall), c.hard)
}

// progress notes that bytes arrived, so the stall deadline moves on. It's
// pushed at most once a second; a deadline per line would be wasted work.
func (c *conn) progress() {
	now := time.Now()
	if now.Sub(c.stallT) < time.Second {
		return
	}
	c.dl.Lock()
	if !c.cut && !c.hard.IsZero() {
		c.stallT = now
		_ = c.nc.SetDeadline(c.soon(now))
	}
	c.dl.Unlock()
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *conn) status() (int, string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	c.progress()
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 3 {
		return 0, "", fmt.Errorf("news server: bad reply %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("news server: bad reply %q", line)
	}
	return code, strings.TrimSpace(line[3:]), nil
}

func (c *conn) cmd(line string) (int, string, error) {
	if _, err := c.w.WriteString(line + "\r\n"); err != nil {
		return 0, "", err
	}
	if err := c.w.Flush(); err != nil {
		return 0, "", err
	}
	return c.status()
}

// body fetches an article's body, dot-unstuffed, lines ending in \n.
func (c *conn) body(id string) ([]byte, error) {
	code, msg, err := c.cmd("BODY <" + id + ">")
	if err != nil {
		return nil, err
	}
	switch code {
	case 222:
	case 430, 423, 451: // 451: taken down (Giganews)
		return nil, ErrMissing
	default:
		return nil, fmt.Errorf("news server: %d %s", code, msg)
	}
	out := make([]byte, 0, 800<<10)
	for {
		line, err := c.r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// A line longer than the buffer: keep reading it.
			out = append(out, line...)
			c.progress()
			continue
		}
		if err != nil {
			return nil, err
		}
		c.progress()
		if len(line) >= 2 && line[0] == '.' {
			if len(line) == 3 && line[1] == '\r' || len(line) == 2 && line[1] == '\n' {
				return out, nil
			}
			line = line[1:]
		}
		out = append(out, line...)
	}
}

// stats asks about many articles at once: the commands are sent together
// and the replies read after, so it costs one round trip, not one each.
func (c *conn) stats(ids []string) (missing []string, err error) {
	for _, id := range ids {
		if _, err := c.w.WriteString("STAT <" + id + ">\r\n"); err != nil {
			return nil, err
		}
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		code, msg, err := c.status()
		if err != nil {
			return nil, err
		}
		switch code {
		case 223:
		case 430, 423, 451: // 451: taken down (Giganews)
			missing = append(missing, id)
		default:
			return nil, fmt.Errorf("news server: %d %s", code, msg)
		}
	}
	return missing, nil
}

// Pool shares a few connections per server among every stream. Servers
// are tried in order, backups last; a later one is asked only for articles
// the earlier ones lack.
type Pool struct {
	servers []*server
	stop    chan struct{}
	once    sync.Once
	// cache keeps decoded articles for every stream; see Cache.
	cache *Cache
}

type server struct {
	Server
	mu sync.Mutex
	// busy counts requests on (or getting) a connection, at most MaxConns;
	// urgent counts urgent requests waiting, which go first.
	busy, urgent int
	// bgBusy counts connections lookups are using; they get half, so
	// playback that starts while a lookup goes on isn't starved.
	bgBusy int
	idle   []*conn
	// open counts connections logged in or logging in (dialing), idle or busy.
	open, dialing int
	// freed is closed, and replaced, when a connection goes idle or away.
	freed chan struct{}
	// limit is what the account turned out to allow when it's fewer than
	// MaxConns; it's forgotten after a while in case a plan changes.
	limit      int
	limitUntil time.Time
	loggedIn   bool
	// downUntil pauses a server that refused logins or connections.
	downUntil time.Time
	lastErr   error
}

// learnFor is how long a discovered connection limit is kept.
const learnFor = 10 * time.Minute

// wrongLoginFor is how long a server that refused our details is left out.
const wrongLoginFor = 10 * time.Minute

// capacity is how many connections may be open; s.mu is held.
func (s *server) capacity() int {
	if s.limit > 0 && time.Now().Before(s.limitUntil) {
		return s.limit
	}
	return s.MaxConns
}

// release notes that a connection went idle or away; s.mu is held.
func (s *server) release() {
	close(s.freed)
	s.freed = make(chan struct{})
}

// drop closes a connection that won't be used again; s.mu is held.
func (s *server) drop(c *conn, polite bool) {
	if polite {
		c.quit()
	} else {
		c.nc.Close()
	}
	s.open--
	s.release()
}

func NewPool(servers []Server) *Pool {
	p := &Pool{}
	for _, backup := range []bool{false, true} {
		for _, s := range servers {
			if s.Host == "" || s.Backup != backup {
				continue
			}
			s.MaxConns = max(s.MaxConns, 1)
			p.servers = append(p.servers, &server{Server: s, freed: make(chan struct{})})
		}
	}
	p.stop = make(chan struct{})
	go p.reap()
	return p
}

// idleFor is how long an unused connection is kept. Logged-in connections
// count against the account's limit, so they aren't held for nothing.
const idleFor = 45 * time.Second

func (p *Pool) reap() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
		}
		for _, s := range p.servers {
			s.mu.Lock()
			keep := s.idle[:0]
			for _, c := range s.idle {
				if time.Since(c.used) < idleFor {
					keep = append(keep, c)
				} else {
					s.drop(c, true)
				}
			}
			clear(s.idle[len(keep):])
			s.idle = keep
			s.mu.Unlock()
		}
	}
}

type bgKey struct{}

// Background marks work that can wait, such as looking a post over before
// it's offered: it gets at most half the connections, so playback has the rest.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, bgKey{}, true)
}

type urgentKey struct{}

// Urgent marks a fetch a reader is stuck waiting on: it takes the next
// free connection ahead of read-ahead.
func Urgent(ctx context.Context) context.Context {
	u := new(atomic.Bool)
	u.Store(true)
	return context.WithValue(ctx, urgentKey{}, u)
}

// urgentOnce marks a fetch that turns urgent when u is set: read-ahead
// that a reader comes to wait on before it has a connection.
func urgentOnce(ctx context.Context, u *atomic.Bool) context.Context {
	return context.WithValue(ctx, urgentKey{}, u)
}

func isUrgent(ctx context.Context) bool {
	u, _ := ctx.Value(urgentKey{}).(*atomic.Bool)
	return u != nil && u.Load()
}

type abortKey struct{}

// abortable lets a finishing read be cut off after all, when another
// request for the same article has already brought it.
func abortable(ctx, abort context.Context) context.Context {
	return context.WithValue(ctx, abortKey{}, abort)
}

// stopped reports whether ctx is done or its read was aborted.
func stopped(ctx context.Context) bool {
	abort, _ := ctx.Value(abortKey{}).(context.Context)
	return ctx.Err() != nil || abort != nil && abort.Err() != nil
}

type finishKey struct{}

// finishing marks reads that may be dropped while they wait for a
// connection, but once on one finish anyway: cutting an article off
// mid-transfer costs the connection and a new login.
func finishing(ctx context.Context) context.Context {
	return context.WithValue(ctx, finishKey{}, true)
}

// quit logs out politely, so the server frees the slot at once.
func (c *conn) quit() {
	c.nc.SetDeadline(time.Now().Add(2 * time.Second))
	c.w.WriteString("QUIT\r\n")
	c.w.Flush()
	c.nc.Close()
}

func (p *Pool) Empty() bool { return p == nil || len(p.servers) == 0 }

// WithCache shares c's articles with every stream read through p.
func (p *Pool) WithCache(c *Cache) *Pool {
	p.cache = c
	return p
}

// Close drops the idle connections and stops reaping.
func (p *Pool) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.stop) })
	for _, s := range p.servers {
		s.mu.Lock()
		for _, c := range s.idle {
			s.drop(c, true)
		}
		s.idle = nil
		s.mu.Unlock()
	}
}

// with runs f on a connection to s, reconnecting once if a kept one died.
func (s *server) with(ctx context.Context, f func(*conn) error) error {
	if ctx.Value(bgKey{}) != nil {
		for {
			s.mu.Lock()
			if s.bgBusy < max(s.capacity()/2, 1) {
				s.bgBusy++
				s.mu.Unlock()
				break
			}
			ch := s.freed
			s.mu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		defer func() {
			s.mu.Lock()
			s.bgBusy--
			s.release()
			s.mu.Unlock()
		}()
	}
	abort, _ := ctx.Value(abortKey{}).(context.Context)
	if abort != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer context.AfterFunc(abort, cancel)()
		defer cancel()
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer func() {
		s.mu.Lock()
		s.busy--
		s.release()
		s.mu.Unlock()
	}()
	for attempt := 0; ; attempt++ {
		c, fresh, err := s.get(ctx)
		if err != nil {
			return err
		}
		run := ctx
		if ctx.Value(finishKey{}) != nil {
			run = context.WithoutCancel(ctx)
			if abort != nil {
				var cancel context.CancelFunc
				run, cancel = context.WithCancel(run)
				stopAF := context.AfterFunc(abort, cancel)
				defer func() { stopAF(); cancel() }()
			}
		}
		stop := c.deadline(run, 60*time.Second)
		err = f(c)
		stop()
		if err == nil || errors.Is(err, ErrMissing) {
			c.used = time.Now()
			s.mu.Lock()
			s.idle = append(s.idle, c)
			s.release()
			s.mu.Unlock()
			return err
		}
		s.mu.Lock()
		s.drop(c, false)
		s.mu.Unlock()
		// abort is checked itself: the cancel it sets off runs a moment
		// later, and a retry then would fetch an article already brought.
		if fresh || attempt > 0 || ctx.Err() != nil || abort != nil && abort.Err() != nil {
			return err
		}
	}
}

// acquire takes one of the server's MaxConns request slots. Urgent
// requests take the next one free; others wait while any are waiting.
func (s *server) acquire(ctx context.Context) error {
	waiting := false
	defer func() {
		if waiting {
			s.mu.Lock()
			s.urgent--
			s.release()
			s.mu.Unlock()
		}
	}()
	for {
		urgent := isUrgent(ctx) // read-ahead can turn urgent while it waits
		s.mu.Lock()
		if s.busy < s.MaxConns && (urgent || s.urgent == 0) {
			s.busy++
			s.mu.Unlock()
			return nil
		}
		if urgent && !waiting {
			waiting = true
			s.urgent++
		}
		ch := s.freed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// get returns an idle connection or logs in a new one. When the account
// is at its limit it waits for one of ours to come free; the limit is
// learned from the refusals, since few owners know their plan's number.
func (s *server) get(ctx context.Context) (*conn, bool, error) {
	wait := 500 * time.Millisecond
	for {
		s.mu.Lock()
		for len(s.idle) > 0 {
			c := s.idle[len(s.idle)-1]
			s.idle = s.idle[:len(s.idle)-1]
			// Servers drop connections left idle; don't trust old ones.
			if time.Since(c.used) < idleFor+10*time.Second {
				s.mu.Unlock()
				return c, false, nil
			}
			s.drop(c, false)
		}
		if time.Now().Before(s.downUntil) {
			err := s.lastErr
			s.mu.Unlock()
			return nil, false, err
		}
		if s.open < max(s.capacity(), 1) {
			s.open++
			s.dialing++
			s.mu.Unlock()
			// A login that's begun is finished even if ctx ends (a reader
			// seeking away): cutting it off wastes one of the account's
			// connections until the server notices.
			c, err := dial(context.WithoutCancel(ctx), s.Server)
			s.mu.Lock()
			s.dialing--
			if err == nil {
				s.loggedIn = true
				if ctx.Err() != nil {
					c.used = time.Now()
					s.idle = append(s.idle, c)
					s.release()
					s.mu.Unlock()
					return nil, false, ctx.Err()
				}
				s.mu.Unlock()
				return c, true, nil
			}
			s.open--
			s.release()
			// Some servers (Giganews) answer one connection too many with
			// 481, the bad-password code; with these details working
			// already, it means the same as 482.
			// Only our working connections count: others still logging in
			// with the same wrong details prove nothing.
			ours := s.open - s.dialing
			tooMany := errors.Is(err, ErrTooMany) || (errors.Is(err, ErrAuth) &&
				(s.loggedIn || ours > 0 || strings.Contains(strings.ToLower(err.Error()), "connection")))
			switch {
			case ctx.Err() != nil:
				s.mu.Unlock()
				return nil, true, ctx.Err()
			case !tooMany:
				// Details that never worked won't start to: set the server
				// aside until they're changed (which makes a new pool), so
				// checks don't each wait on its slow refusals.
				aside := 30 * time.Second
				if errors.Is(err, ErrAuth) {
					aside = wrongLoginFor
				}
				err = fmt.Errorf("%s: %w", s.Host, err)
				s.downUntil, s.lastErr = time.Now().Add(aside), err
				s.mu.Unlock()
				return nil, true, err
			case ours > 0:
				// Ours are all the account allows: use them.
				s.limit, s.limitUntil = ours, time.Now().Add(learnFor)
			default:
				// Another app (or a just-restarted server) holds them; they
				// free up soon.
				s.mu.Unlock()
				if wait > 8*time.Second {
					return nil, true, fmt.Errorf("%w: %v", ErrTooMany, err)
				}
				select {
				case <-ctx.Done():
					return nil, true, ctx.Err()
				case <-time.After(wait):
				}
				wait *= 2
				continue
			}
		}
		ch := s.freed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// Body fetches an article from the first server that has it.
func (p *Pool) Body(ctx context.Context, id string) ([]byte, error) {
	if p.Empty() {
		return nil, errors.New("no news server is set up")
	}
	var last error = ErrMissing
	for _, s := range p.servers {
		var b []byte
		err := s.with(ctx, func(c *conn) (err error) {
			b, err = c.body(id)
			return err
		})
		if err == nil {
			return b, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if stopped(ctx) {
			return nil, err
		}
		if !errors.Is(err, ErrMissing) || errors.Is(last, ErrMissing) {
			last = err
		}
	}
	return nil, last
}

// Stat reports whether every one of the articles is on some server.
func (p *Pool) Stat(ctx context.Context, ids []string) (bool, error) {
	if p.Empty() {
		return false, errors.New("no news server is set up")
	}
	remaining := ids
	var lastErr error
	ask := func(s *server, ids []string) (missing []string, err error) {
		t := time.Now()
		err = s.with(ctx, func(c *conn) (err error) {
			missing, err = c.stats(ids)
			return err
		})
		slog.Debug("usenet stat", "server", s.Host, "asked", len(ids), "missing", len(missing), "err", err, "took", time.Since(t).Round(time.Millisecond))
		return missing, err
	}
	for i, s := range p.servers {
		if i == 1 && len(remaining) > 1 && !p.anyHas(ctx, ask, remaining[0]) {
			// A taken-down post is gone from every server: one of its
			// gaps asked about first settles that, before the backups
			// are asked about all of them.
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
		missing, err := ask(s, remaining)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			lastErr = err
			continue
		}
		if remaining = missing; len(remaining) == 0 {
			return true, nil
		}
	}
	if lastErr != nil && len(remaining) == len(ids) {
		return false, lastErr
	}
	return false, nil
}

// anyHas reports whether a server after the first has the article. One
// that can't be asked counts as having it, so only a sure miss says no.
func (p *Pool) anyHas(ctx context.Context, ask func(*server, []string) ([]string, error), id string) bool {
	for _, s := range p.servers[1:] {
		missing, err := ask(s, []string{id})
		if err != nil || len(missing) == 0 {
			return true
		}
	}
	return false
}

// Check logs in to every server, for the settings page.
func (p *Pool) Check(ctx context.Context) error {
	if p.Empty() {
		return errors.New("no news server is set up")
	}
	for _, s := range p.servers {
		if err := s.with(ctx, func(*conn) error { return nil }); err != nil {
			return fmt.Errorf("%s: %w", s.Host, err)
		}
	}
	return nil
}
