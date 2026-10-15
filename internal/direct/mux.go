package direct

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"
)

// Split serves HTTP and HTTPS on one listener: each connection's first byte
// decides (0x16 starts a TLS handshake). Owners forward a single port, and the
// existing http:// addresses keep working.
func Split(ln net.Listener) (plain, secure net.Listener) {
	m := &mux{ln: ln, plain: make(chan net.Conn), secure: make(chan net.Conn), done: make(chan struct{})}
	go m.run()
	return &muxListener{m, m.plain}, &muxListener{m, m.secure}
}

type mux struct {
	ln            net.Listener
	plain, secure chan net.Conn
	done          chan struct{}
	once          sync.Once
	err           error
}

func (m *mux) run() {
	for {
		c, err := m.ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			m.err = err
			m.close()
			return
		}
		go m.route(c)
	}
}

func (m *mux) route(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	ch := m.plain
	if b[0] == 0x16 {
		ch = m.secure
	}
	select {
	case ch <- &peekedConn{Conn: c, r: br}:
	case <-m.done:
		c.Close()
	}
}

func (m *mux) close() {
	m.once.Do(func() {
		close(m.done)
		m.ln.Close()
	})
}

type muxListener struct {
	m  *mux
	ch chan net.Conn
}

func (l *muxListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.m.done:
		if l.m.err != nil {
			return nil, l.m.err
		}
		return nil, net.ErrClosed
	}
}

func (l *muxListener) Close() error   { l.m.close(); return nil }
func (l *muxListener) Addr() net.Addr { return l.m.ln.Addr() }

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
