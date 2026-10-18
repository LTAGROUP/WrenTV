package usenet

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// NZB lists the files of a post and the articles each is split into.
type NZB struct {
	Files []NZBFile
}

type NZBFile struct {
	Subject  string
	Groups   []string
	Segments []Segment // in order
}

type Segment struct {
	Bytes  int64 // encoded size, roughly the decoded size
	Number int
	ID     string // message id, without the angle brackets
}

// Bytes is roughly how big the file is.
func (f NZBFile) Bytes() int64 {
	var n int64
	for _, s := range f.Segments {
		n += s.Bytes
	}
	return n
}

var subjectNameRE = regexp.MustCompile(`"([^"]+)"`)

// Name is the file name posted in the subject, if any.
func (f NZBFile) Name() string {
	if m := subjectNameRE.FindStringSubmatch(f.Subject); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func ParseNZB(data []byte) (*NZB, error) {
	var doc struct {
		Files []struct {
			Subject  string   `xml:"subject,attr"`
			Groups   []string `xml:"groups>group"`
			Segments []struct {
				Bytes  int64  `xml:"bytes,attr"`
				Number int    `xml:"number,attr"`
				ID     string `xml:",chardata"`
			} `xml:"segments>segment"`
		} `xml:"file"`
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("NZB: %w", err)
	}
	n := &NZB{}
	for _, f := range doc.Files {
		nf := NZBFile{Subject: f.Subject, Groups: f.Groups}
		seen := map[int]bool{}
		for _, s := range f.Segments {
			id := strings.Trim(strings.TrimSpace(s.ID), "<>")
			if id == "" || seen[s.Number] {
				continue
			}
			seen[s.Number] = true
			nf.Segments = append(nf.Segments, Segment{Bytes: s.Bytes, Number: s.Number, ID: id})
		}
		slices.SortFunc(nf.Segments, func(a, b Segment) int { return a.Number - b.Number })
		if len(nf.Segments) > 0 {
			n.Files = append(n.Files, nf)
		}
	}
	if len(n.Files) == 0 {
		return nil, errors.New("NZB has no files")
	}
	return n, nil
}

// ---------- yEnc ----------

// part is one decoded article.
type part struct {
	Name  string
	Size  int64 // of the whole file
	Begin int64 // offset of the first byte in the file
	Data  []byte
}

var errNotYEnc = errors.New("article isn't yEnc encoded")

// decodeYEnc decodes an article body (lines ending in \n or \r\n).
func decodeYEnc(body []byte) (*part, error) {
	p := &part{Begin: -1}
	i := bytes.Index(body, []byte("=ybegin "))
	if i < 0 {
		return nil, errNotYEnc
	}
	body = body[i:]
	line, body := cutLine(body)
	attrs := yAttrs(line)
	p.Size, _ = strconv.ParseInt(attrs["size"], 10, 64)
	p.Name = attrs["name"]
	if bytes.HasPrefix(body, []byte("=ypart ")) {
		line, body = cutLine(body)
		a := yAttrs(line)
		b, _ := strconv.ParseInt(a["begin"], 10, 64)
		p.Begin = b - 1
	} else {
		p.Begin = 0
	}
	out := make([]byte, 0, len(body))
	var crc string
	for len(body) > 0 {
		line, body = cutLine(body)
		if bytes.HasPrefix(line, []byte("=yend")) {
			a := yAttrs(line)
			crc = a["pcrc32"]
			if crc == "" && p.Begin == 0 {
				crc = a["crc32"]
			}
			break
		}
		for j := 0; j < len(line); j++ {
			c := line[j]
			if c == '=' && j+1 < len(line) {
				j++
				c = line[j] - 64
			}
			out = append(out, c-42)
		}
	}
	if crc != "" {
		if want, err := strconv.ParseUint(crc, 16, 32); err == nil && uint32(want) != crc32.ChecksumIEEE(out) {
			return nil, errors.New("article is damaged (checksum mismatch)")
		}
	}
	p.Data = out
	return p, nil
}

func cutLine(b []byte) (line, rest []byte) {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return bytes.TrimRight(b, "\r"), nil
	}
	return bytes.TrimRight(b[:i], "\r"), b[i+1:]
}

// yAttrs reads key=value pairs; name= runs to the end of the line.
func yAttrs(line []byte) map[string]string {
	out := map[string]string{}
	s := string(line)
	if i := strings.Index(s, " name="); i >= 0 {
		out["name"] = strings.TrimSpace(s[i+6:])
		s = s[:i]
	}
	for _, f := range strings.Fields(s) {
		if k, v, ok := strings.Cut(f, "="); ok {
			out[k] = v
		}
	}
	return out
}

// ---------- names ----------

var (
	videoExts   = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mov", ".webm", ".wmv", ".mpg"}
	rarPartRE   = regexp.MustCompile(`(?i)\.part0*(\d+)\.rar$`)
	rarOldRE    = regexp.MustCompile(`(?i)\.r(\d{2,3})$`)
	rarNumRE    = regexp.MustCompile(`(?i)\.rar\.(\d{2,3})$`)
	junkExtRE   = regexp.MustCompile(`(?i)\.(par2|nfo|sfv|nzb|txt|jpg|png|srr|srs|url|exe)$|\.vol\d+\+\d+`)
	sampleRE    = regexp.MustCompile(`(?i)(^|[._ -])sample([._ -]|$)`)
	archive7zRE = regexp.MustCompile(`(?i)\.7z(\.\d+)?$|\.zip$`)
)

func isVideo(name string) bool {
	return slices.Contains(videoExts, strings.ToLower(path.Ext(name)))
}

// rarVolume reports a RAR volume's position: .rar comes first, then .r00…
func rarVolume(name string) (int, bool) {
	if m := rarPartRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := rarOldRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n + 1, true
	}
	if m := rarNumRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if strings.EqualFold(path.Ext(name), ".rar") {
		return 0, true
	}
	return 0, false
}

// rarSet is the name a volume shares with the rest of its archive.
func rarSet(name string) string {
	for _, re := range []*regexp.Regexp{rarPartRE, rarOldRE, rarNumRE} {
		if loc := re.FindStringIndex(name); loc != nil {
			return strings.ToLower(name[:loc[0]])
		}
	}
	return strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
}
