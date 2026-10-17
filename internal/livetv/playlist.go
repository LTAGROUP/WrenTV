// Package livetv is live TV from IPTV playlists: channels from M3U playlists
// (or Xtream Codes logins), what's on from XMLTV guides, and streams passed
// through the server.
package livetv

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// Channel is one playable channel.
type Channel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number,omitempty"`
	Logo   string `json:"logo,omitempty"`
	Group  string `json:"group,omitempty"`

	TvgID     string `json:"-"`
	TvgName   string `json:"-"`
	URL       string `json:"-"`
	UserAgent string `json:"-"`
	Source    string `json:"-"`
	// numbered: the playlist gave the number (tvg-chno), rather than its place in the list.
	numbered bool
}

// Playlist is a parsed M3U file.
type Playlist struct {
	Channels []*Channel
	// GuideURLs come from the header's url-tvg / x-tvg-url.
	GuideURLs []string
}

var attrRe = regexp.MustCompile(`([A-Za-z0-9_-]+)="([^"]*)"`)

// ParseM3U reads an extended M3U playlist. Entries that are films or series
// (Xtream playlists mix them in) are left out: live TV is channels.
func ParseM3U(r io.Reader, source string) (*Playlist, error) {
	pl := &Playlist{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var cur *Channel
	seen := map[string]int{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTM3U"):
			for _, m := range attrRe.FindAllStringSubmatch(line, -1) {
				if k := strings.ToLower(m[1]); k == "url-tvg" || k == "x-tvg-url" {
					for _, u := range strings.Split(m[2], ",") {
						if u = strings.TrimSpace(u); u != "" && !contains(pl.GuideURLs, u) {
							pl.GuideURLs = append(pl.GuideURLs, u)
						}
					}
				}
			}
		case strings.HasPrefix(line, "#EXTINF"):
			cur = parseExtinf(line)
		case strings.HasPrefix(line, "#EXTGRP:"):
			if cur != nil && cur.Group == "" {
				cur.Group = strings.TrimSpace(strings.TrimPrefix(line, "#EXTGRP:"))
			}
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			opt := strings.TrimPrefix(line, "#EXTVLCOPT:")
			if cur != nil && strings.HasPrefix(strings.ToLower(opt), "http-user-agent=") {
				cur.UserAgent = strings.TrimSpace(opt[len("http-user-agent="):])
			}
		case strings.HasPrefix(line, "#"):
		default:
			if cur == nil {
				cur = &Channel{}
			}
			cur.URL = line
			if !isVOD(line) {
				if cur.Name == "" {
					cur.Name = path.Base(strings.SplitN(line, "?", 2)[0])
				}
				cur.Source = source
				key := source + "|" + cur.TvgID + "|" + cur.Name
				cur.ID = shortHash(key)
				// The same channel twice (an HD and SD feed with one name): keep both, apart.
				if n := seen[key]; n > 0 {
					cur.ID = shortHash(key + "|" + itoa(n))
				}
				seen[key]++
				pl.Channels = append(pl.Channels, cur)
			}
			cur = nil
		}
	}
	return pl, sc.Err()
}

func parseExtinf(line string) *Channel {
	c := &Channel{}
	// The name follows the first comma outside quotes.
	inQuote := false
	split := -1
	for i, r := range line {
		if r == '"' {
			inQuote = !inQuote
		} else if r == ',' && !inQuote {
			split = i
			break
		}
	}
	attrs := line
	if split >= 0 {
		attrs = line[:split]
		c.Name = strings.TrimSpace(line[split+1:])
	}
	for _, m := range attrRe.FindAllStringSubmatch(attrs, -1) {
		v := strings.TrimSpace(m[2])
		switch strings.ToLower(m[1]) {
		case "tvg-id":
			c.TvgID = v
		case "tvg-name":
			c.TvgName = v
		case "tvg-logo":
			c.Logo = v
		case "group-title":
			c.Group = v
		case "tvg-chno", "channel-number":
			if v != "" {
				c.Number = v
				c.numbered = true
			}
		}
	}
	if c.Name == "" {
		c.Name = c.TvgName
	}
	return c
}

// isVOD spots films and series in a playlist: Xtream's /movie/ and /series/
// paths, and files rather than streams.
func isVOD(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	if strings.Contains(p, "/movie/") || strings.Contains(p, "/series/") {
		return true
	}
	switch path.Ext(p) {
	case ".mp4", ".mkv", ".avi", ".mov", ".wmv", ".m4v":
		return true
	}
	return false
}

// PlaylistURL is where a source's playlist is.
func PlaylistURL(s config.LiveTVSource) string {
	if s.Kind != "xtream" {
		return strings.TrimSpace(s.URL)
	}
	q := url.Values{"username": {s.Username}, "password": {s.Password}, "type": {"m3u_plus"}, "output": {"ts"}}
	return xtreamBase(s.URL) + "/get.php?" + q.Encode()
}

// GuideURLs is where a source's guide is: the owner's, else the Xtream
// server's, else what the playlist names.
func GuideURLs(s config.LiveTVSource, pl *Playlist) []string {
	if u := strings.TrimSpace(s.EPGURL); u != "" {
		return []string{u}
	}
	if s.Kind == "xtream" {
		q := url.Values{"username": {s.Username}, "password": {s.Password}}
		return []string{xtreamBase(s.URL) + "/xmltv.php?" + q.Encode()}
	}
	if pl != nil {
		return pl.GuideURLs
	}
	return nil
}

func xtreamBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	// People paste the full get.php or player_api.php link; keep the server.
	return u.Scheme + "://" + u.Host
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:6])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
