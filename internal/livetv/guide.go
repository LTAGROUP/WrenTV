package livetv

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Program is one programme in the guide. Times are Unix seconds.
type Program struct {
	Title       string `json:"title"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`
	Description string `json:"description,omitempty"`
	Episode     string `json:"episode,omitempty"`
	// Icon is the programme's artwork, when the guide has it.
	Icon       string   `json:"icon,omitempty"`
	Categories []string `json:"categories,omitempty"`
	// New is a first airing; Live is happening as it airs.
	New  bool `json:"new,omitempty"`
	Live bool `json:"live,omitempty"`
}

// Guide is programmes by channel ID.
type Guide map[string][]Program

// ParseXMLTV reads an XMLTV guide (gzipped or not), keeping programmes for
// [channels] between [from] and [to]. Guide channels are matched to playlist
// channels by tvg-id, else by name. The guide's channel icons go in
// [logos], by playlist channel ID, for channels the playlist gave none.
func ParseXMLTV(r io.Reader, channels []*Channel, from, to time.Time, into Guide, logos map[string]string) error {
	br := bufio.NewReaderSize(r, 64<<10)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer gz.Close()
		br = bufio.NewReaderSize(gz, 64<<10)
	}

	byTvg := map[string][]string{}
	byName := map[string][]string{}
	for _, c := range channels {
		if c.TvgID != "" {
			k := strings.ToLower(c.TvgID)
			byTvg[k] = append(byTvg[k], c.ID)
		}
		for _, n := range []string{c.Name, c.TvgName} {
			if k := normName(n); k != "" && !contains(byName[k], c.ID) {
				byName[k] = append(byName[k], c.ID)
			}
		}
	}
	names := map[string][]string{} // guide channel id -> its display names
	icons := map[string]string{}   // guide channel id -> its icon
	resolved := map[string][]string{}
	resolve := func(gid string) []string {
		if ids, ok := resolved[gid]; ok {
			return ids
		}
		ids := byTvg[strings.ToLower(gid)]
		if len(ids) == 0 {
			for _, n := range append(names[gid], gid) {
				if ids = byName[normName(n)]; len(ids) > 0 {
					break
				}
			}
		}
		resolved[gid] = ids
		return ids
	}

	dec := xml.NewDecoder(br)
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	lo, hi := from.Unix(), to.Unix()
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "channel":
			var ch struct {
				ID    string   `xml:"id,attr"`
				Names []string `xml:"display-name"`
				Icons []struct {
					Src string `xml:"src,attr"`
				} `xml:"icon"`
			}
			if err := dec.DecodeElement(&ch, &se); err == nil {
				names[ch.ID] = ch.Names
				if len(ch.Icons) > 0 && strings.HasPrefix(ch.Icons[0].Src, "http") {
					icons[ch.ID] = ch.Icons[0].Src
				}
			}
		case "programme":
			var gid, start, stop string
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "channel":
					gid = a.Value
				case "start":
					start = a.Value
				case "stop":
					stop = a.Value
				}
			}
			ids := resolve(gid)
			s, e := parseXMLTVTime(start), parseXMLTVTime(stop)
			if len(ids) == 0 || s == 0 || e <= lo || s >= hi {
				_ = dec.Skip()
				continue
			}
			var p struct {
				Titles   []string `xml:"title"`
				SubTitle string   `xml:"sub-title"`
				Desc     []string `xml:"desc"`
				Episodes []struct {
					System string `xml:"system,attr"`
					Value  string `xml:",chardata"`
				} `xml:"episode-num"`
				Icons []struct {
					Src string `xml:"src,attr"`
				} `xml:"icon"`
				Categories []string  `xml:"category"`
				New        *struct{} `xml:"new"`
				Live       *struct{} `xml:"live"`
				Premiere   *struct{} `xml:"premiere"`
			}
			if err := dec.DecodeElement(&p, &se); err != nil {
				continue
			}
			prog := Program{Start: s, End: e}
			if len(p.Titles) > 0 {
				prog.Title = strings.TrimSpace(p.Titles[0])
			}
			if len(p.Desc) > 0 {
				prog.Description = strings.TrimSpace(p.Desc[0])
			}
			prog.Episode = strings.TrimSpace(p.SubTitle)
			for _, ep := range p.Episodes {
				if ep.System == "onscreen" && strings.TrimSpace(ep.Value) != "" {
					prog.Episode = strings.TrimSpace(strings.TrimSpace(ep.Value) + " " + prog.Episode)
				}
			}
			if prog.Title == "" {
				continue
			}
			if len(p.Icons) > 0 && strings.HasPrefix(p.Icons[0].Src, "http") {
				prog.Icon = p.Icons[0].Src
			}
			for _, c := range p.Categories {
				if c = strings.TrimSpace(c); c != "" && !contains(prog.Categories, c) {
					prog.Categories = append(prog.Categories, c)
				}
			}
			prog.New = p.New != nil || p.Premiere != nil
			prog.Live = p.Live != nil
			for _, id := range ids {
				into[id] = append(into[id], prog)
				if icon := icons[gid]; icon != "" && logos != nil && logos[id] == "" {
					logos[id] = icon
				}
			}
		}
	}
	return nil
}

// Tidy sorts each channel's programmes and drops overlaps from merged guides.
func (g Guide) Tidy() {
	for id, ps := range g {
		sort.Slice(ps, func(i, j int) bool { return ps[i].Start < ps[j].Start })
		out := ps[:0]
		for _, p := range ps {
			if n := len(out); n > 0 && p.Start < out[n-1].End {
				continue
			}
			out = append(out, p)
		}
		g[id] = out
	}
}

// NowNext is what's on [id] at [t], and what follows.
func (g Guide) NowNext(id string, t int64) (now, next *Program) {
	ps := g[id]
	i := sort.Search(len(ps), func(i int) bool { return ps[i].End > t })
	if i < len(ps) && ps[i].Start <= t {
		now = &ps[i]
		i++
	}
	if i < len(ps) {
		next = &ps[i]
	}
	return now, next
}

// parseXMLTVTime reads "20261016200000 +0100" (UTC when the offset is missing).
func parseXMLTVTime(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) < 14 {
		return 0
	}
	t, err := time.Parse("20060102150405", s[:14])
	if err != nil {
		return 0
	}
	if off := strings.TrimSpace(s[14:]); len(off) == 5 && (off[0] == '+' || off[0] == '-') {
		if z, err := time.Parse("-0700", off); err == nil {
			_, secs := z.Zone()
			t = t.Add(-time.Duration(secs) * time.Second)
		}
	}
	return t.Unix()
}

var qualityWords = map[string]bool{"uhd": true, "fhd": true, "hd": true, "sd": true, "4k": true, "hevc": true, "backup": true, "h265": true}

// normName matches "BBC One HD" to "bbc one": lower case, letters and
// digits only, without quality words.
func normName(s string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !qualityWords[w] {
			b.WriteString(w)
		}
	}
	return b.String()
}
