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
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrMissing means no server has the article (expired or taken down).
	ErrMissing = errors.New("article missing")
	// ErrAuth means a news server refused the login.
	ErrAuth = errors.New("news server login failed")
)

// Server is a news server account.
type Server struct {
	Host     string
	Port     int
	TLS      bool
	User     string
	Pass     string
	MaxConns int
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
}

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
	if code != 281 {
		return fmt.Errorf("%w: %d %s", ErrAuth, code, msg)
	}
	return nil
}

// deadline bounds the connection by ctx and an idle timeout; stop clears it.
func (c *conn) deadline(ctx context.Context, d time.Duration) (stop func()) {
	t := time.Now().Add(d)
	if dl, ok := ctx.Deadline(); ok && dl.Before(t) {
		t = dl
	}
	_ = c.nc.SetDeadline(t)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.nc.SetDeadline(time.Unix(1, 0)) // unblock reads now
		case <-done:
		}
	}()
	return func() { close(done); _ = c.nc.SetDeadline(time.Time{}) }
}

func (c *conn) status() (int, string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
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
	case 430, 423:
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
			continue
		}
		if err != nil {
			return nil, err
		}
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
		case 430, 423:
			missing = append(missing, id)
		default:
			return nil, fmt.Errorf("news server: %d %s", code, msg)
		}
	}
	return missing, nil
}

// Pool shares a few connections per server among every stream. Servers
// are tried in order; a later one is asked only for articles the earlier
// ones lack.
type Pool struct {
	servers []*server
}

type server struct {
	Server
	slots chan struct{}
	mu    sync.Mutex
	idle  []*conn
	// downUntil pauses a server that refused logins or connections.
	downUntil time.Time
	lastErr   error
}

func NewPool(servers []Server) *Pool {
	p := &Pool{}
	for _, s := range servers {
		if s.Host == "" {
			continue
		}
		s.MaxConns = max(s.MaxConns, 1)
		p.servers = append(p.servers, &server{Server: s, slots: make(chan struct{}, s.MaxConns)})
	}
	return p
}

func (p *Pool) Empty() bool { return p == nil || len(p.servers) == 0 }

// Close drops the idle connections.
func (p *Pool) Close() {
	for _, s := range p.servers {
		s.mu.Lock()
		for _, c := range s.idle {
			c.nc.Close()
		}
		s.idle = nil
		s.mu.Unlock()
	}
}

// with runs f on a connection to s, reconnecting once if a kept one died.
func (s *server) with(ctx context.Context, f func(*conn) error) error {
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.slots }()
	for attempt := 0; ; attempt++ {
		c, fresh, err := s.get(ctx)
		if err != nil {
			return err
		}
		stop := c.deadline(ctx, 60*time.Second)
		err = f(c)
		stop()
		if err == nil || errors.Is(err, ErrMissing) {
			c.used = time.Now()
			s.mu.Lock()
			s.idle = append(s.idle, c)
			s.mu.Unlock()
			return err
		}
		c.nc.Close()
		if fresh || attempt > 0 || ctx.Err() != nil {
			return err
		}
	}
}

func (s *server) get(ctx context.Context) (*conn, bool, error) {
	s.mu.Lock()
	for len(s.idle) > 0 {
		c := s.idle[len(s.idle)-1]
		s.idle = s.idle[:len(s.idle)-1]
		// Servers drop connections left idle; don't trust old ones.
		if time.Since(c.used) < 50*time.Second {
			s.mu.Unlock()
			return c, false, nil
		}
		c.nc.Close()
	}
	if time.Now().Before(s.downUntil) {
		err := s.lastErr
		s.mu.Unlock()
		return nil, false, err
	}
	s.mu.Unlock()
	c, err := dial(ctx, s.Server)
	if err != nil && ctx.Err() == nil {
		s.mu.Lock()
		s.downUntil, s.lastErr = time.Now().Add(30*time.Second), fmt.Errorf("%s: %w", s.Host, err)
		s.mu.Unlock()
	}
	return c, true, err
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
	for _, s := range p.servers {
		var missing []string
		err := s.with(ctx, func(c *conn) (err error) {
			missing, err = c.stats(remaining)
			return err
		})
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
