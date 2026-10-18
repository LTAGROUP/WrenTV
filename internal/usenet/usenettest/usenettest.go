// Package usenettest runs a fake news server for tests outside the usenet
// package.
package usenettest

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"html"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Server is a news server holding posted articles. It accepts any
// username with the password "secret".
type Server struct {
	Host string
	Port int

	mu       sync.Mutex
	articles map[string][]byte
	files    []string // NZB <file> elements, in post order
}

// New starts a server that stops when the test ends.
func New(t *testing.T) *Server {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &Server{Host: host, articles: map[string][]byte{}}
	s.Port, _ = strconv.Atoi(port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// Post splits a file into yEnc articles and adds it to the NZB.
func (s *Server) Post(name string, data []byte, partSize int) {
	total := (len(data) + partSize - 1) / partSize
	var segs strings.Builder
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < total; i++ {
		begin := i * partSize
		chunk := data[begin:min(begin+partSize, len(data))]
		id := fmt.Sprintf("%s.%d@test", strings.ReplaceAll(name, " ", "_"), i+1)
		s.articles[id] = yenc(name, int64(len(data)), int64(begin), i+1, total, chunk)
		fmt.Fprintf(&segs, `<segment bytes="%d" number="%d">%s</segment>`, len(chunk), i+1, html.EscapeString(id))
	}
	s.files = append(s.files, fmt.Sprintf(`<file poster="test" date="0" subject="%s"><groups><group>alt.binaries.test</group></groups><segments>%s</segments></file>`,
		html.EscapeString(fmt.Sprintf(`[1/1] - "%s" yEnc (1/%d)`, name, total)), segs.String()))
}

// Remove drops an article, as a takedown does.
func (s *Server) Remove(name string, part int) {
	s.mu.Lock()
	delete(s.articles, fmt.Sprintf("%s.%d@test", strings.ReplaceAll(name, " ", "_"), part))
	s.mu.Unlock()
}

// NZB is the document for everything posted.
func (s *Server) NZB() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + strings.Join(s.files, "") + `</nzb>`)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	fmt.Fprint(w, "200 hello\r\n")
	w.Flush()
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		cmd := strings.ToUpper(f[0])
		switch {
		case cmd == "AUTHINFO" && len(f) == 3 && strings.EqualFold(f[1], "USER"):
			fmt.Fprint(w, "381 more\r\n")
		case cmd == "AUTHINFO" && len(f) == 3 && strings.EqualFold(f[1], "PASS"):
			if f[2] == "secret" {
				authed = true
				fmt.Fprint(w, "281 ok\r\n")
			} else {
				fmt.Fprint(w, "481 no\r\n")
			}
		case !authed:
			fmt.Fprint(w, "480 login\r\n")
		case (cmd == "BODY" || cmd == "STAT") && len(f) == 2:
			id := strings.Trim(f[1], "<>")
			s.mu.Lock()
			body, ok := s.articles[id]
			s.mu.Unlock()
			switch {
			case !ok:
				fmt.Fprint(w, "430 no such article\r\n")
			case cmd == "STAT":
				fmt.Fprintf(w, "223 0 <%s>\r\n", id)
			default:
				fmt.Fprintf(w, "222 0 <%s>\r\n", id)
				for _, l := range bytes.SplitAfter(body, []byte("\r\n")) {
					if len(l) == 0 {
						continue
					}
					if l[0] == '.' {
						w.WriteByte('.')
					}
					w.Write(l)
				}
				fmt.Fprint(w, ".\r\n")
			}
		case cmd == "QUIT":
			fmt.Fprint(w, "205 bye\r\n")
			w.Flush()
			return
		default:
			fmt.Fprint(w, "500 what\r\n")
		}
		w.Flush()
	}
}

func yenc(name string, size, begin int64, part, total int, data []byte) []byte {
	var b bytes.Buffer
	if total > 1 {
		fmt.Fprintf(&b, "=ybegin part=%d total=%d line=128 size=%d name=%s\r\n", part, total, size, name)
		fmt.Fprintf(&b, "=ypart begin=%d end=%d\r\n", begin+1, begin+int64(len(data)))
	} else {
		fmt.Fprintf(&b, "=ybegin line=128 size=%d name=%s\r\n", size, name)
	}
	col := 0
	for i, c := range data {
		e := c + 42
		if e == 0 || e == '\n' || e == '\r' || e == '=' || (col == 0 && (e == '.' || e == ' ' || e == '\t')) ||
			(e == ' ' || e == '\t') && (col == 127 || i == len(data)-1) {
			b.WriteByte('=')
			e += 64
			col++
		}
		b.WriteByte(e)
		col++
		if col >= 128 {
			b.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "=yend size=%d part=%d pcrc32=%08x\r\n", len(data), part, crc32.ChecksumIEEE(data))
	return b.Bytes()
}
