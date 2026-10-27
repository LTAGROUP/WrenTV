package onlinesubs

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	srtTime = regexp.MustCompile(`^\s*(\d{1,2}:\d{2}:\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2})[,.](\d{1,3})`)
	assTags = regexp.MustCompile(`\{[^}]*\}`)
)

// ToVTT turns a downloaded subtitle (WebVTT, SRT or ASS/SSA, in UTF-8,
// UTF-16 or Windows-1252) into UTF-8 WebVTT.
func ToVTT(b []byte) ([]byte, error) {
	text := decodeText(b)
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "WEBVTT"):
		return []byte(text), nil
	case strings.Contains(trimmed, "[Events]"):
		return assToVTT(text)
	case strings.Contains(trimmed, "-->"):
		return srtToVTT(text), nil
	}
	return nil, errors.New("unrecognised subtitle format")
}

func decodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			if le {
				u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
			} else {
				u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
			}
		}
		return string(utf16.Decode(u))
	case utf8.Valid(b):
		return string(b)
	}
	// Older subtitles are mostly Windows-1252.
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x80 && c < 0xA0 {
			sb.WriteRune(cp1252[c-0x80])
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

var cp1252 = [32]rune{
	'€', '�', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '�', 'Ž', '�',
	'�', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '�', 'ž', 'Ÿ',
}

func srtToVTT(text string) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := srtTime.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		out.WriteString(pad(m[1]) + "." + ms(m[2]) + " --> " + pad(m[3]) + "." + ms(m[4]) + "\n")
		// The cue's text runs to the next blank line (or the next cue, in a
		// file that leaves the blank out).
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !srtTime.MatchString(lines[i+1]) {
			i++
			if isIndex(lines[i]) && i+1 < len(lines) && srtTime.MatchString(lines[i+1]) {
				break
			}
			out.WriteString(lines[i] + "\n")
		}
		out.WriteString("\n")
	}
	return []byte(out.String())
}

func isIndex(l string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(l))
	return err == nil
}

func pad(hms string) string {
	if len(hms) == 7 { // 1:02:03
		return "0" + hms
	}
	return hms
}

func ms(s string) string {
	for len(s) < 3 {
		s += "0"
	}
	return s
}

// assToVTT keeps an ASS/SSA script's dialogue text, dropping its styling.
func assToVTT(text string) ([]byte, error) {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	fields := []string{}
	events := false
	n := 0
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			events = strings.EqualFold(l, "[Events]")
			continue
		}
		if !events {
			continue
		}
		if f, ok := strings.CutPrefix(l, "Format:"); ok {
			fields = strings.Split(f, ",")
			for i := range fields {
				fields[i] = strings.ToLower(strings.TrimSpace(fields[i]))
			}
			continue
		}
		d, ok := strings.CutPrefix(l, "Dialogue:")
		if !ok || len(fields) == 0 {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(d), ",", len(fields))
		if len(parts) < len(fields) {
			continue
		}
		var start, end, body string
		for i, f := range fields {
			switch f {
			case "start":
				start = parts[i]
			case "end":
				end = parts[i]
			case "text":
				body = parts[i]
			}
		}
		st, ok1 := assTime(start)
		et, ok2 := assTime(end)
		body = strings.TrimSpace(strings.NewReplacer(`\N`, "\n", `\n`, "\n", `\h`, " ").Replace(assTags.ReplaceAllString(body, "")))
		if !ok1 || !ok2 || body == "" {
			continue
		}
		out.WriteString(st + " --> " + et + "\n" + body + "\n\n")
		n++
	}
	if n == 0 {
		return nil, errors.New("the subtitle file has no dialogue")
	}
	return []byte(out.String()), nil
}

// assTime turns "0:01:02.50" into "00:01:02.500".
func assTime(s string) (string, bool) {
	hms, frac, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok || strings.Count(hms, ":") != 2 {
		return "", false
	}
	return pad(hms) + "." + ms(frac), true
}
