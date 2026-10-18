package streaming

import (
	"fmt"
	"regexp"
	"strings"
)

// ExcludeMatcher compiles the owner's exclude list. Each entry is a whole
// word ("TS" doesn't match "DTS-HD"), a phrase whose words may be split by
// spaces, dots, underscores or dashes ("korean dub" matches "Korean.Dub"),
// or a regular expression written /like this/ (flags after the last slash,
// e.g. /hevc/i). Words and phrases ignore case.
func ExcludeMatcher(words []string) (func(string) bool, error) {
	var res []*regexp.Regexp
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if re, ok, err := slashRegexp(w); ok {
			if err != nil {
				return nil, fmt.Errorf("exclude %q: %w", w, err)
			}
			res = append(res, re)
			continue
		}
		parts := strings.FieldsFunc(w, isNameSep)
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		if len(parts) == 0 {
			continue
		}
		// Letters and digits on either side mean it's part of a longer word.
		res = append(res, regexp.MustCompile(`(?i)(?:^|[^\pL\pN])`+strings.Join(parts, `[\s._-]+`)+`(?:$|[^\pL\pN])`))
	}
	if len(res) == 0 {
		return nil, nil
	}
	return func(s string) bool {
		for _, re := range res {
			if re.MatchString(s) {
				return true
			}
		}
		return false
	}, nil
}

func isNameSep(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' || r == '\t' }

// slashRegexp reads "/expr/flags".
func slashRegexp(w string) (*regexp.Regexp, bool, error) {
	end := strings.LastIndex(w, "/")
	if len(w) < 3 || w[0] != '/' || end < 2 {
		return nil, false, nil
	}
	expr, flags := w[1:end], w[end+1:]
	for _, f := range flags {
		if !strings.ContainsRune("imsU", f) {
			return nil, true, fmt.Errorf("unknown flag %q (use i, m, s or U)", f)
		}
	}
	if flags != "" {
		expr = "(?" + flags + ")" + expr
	}
	re, err := regexp.Compile(expr)
	return re, true, err
}

// rank is Rank without releases the owner excluded.
func (s *Service) rank(rels []Release, quality string, maxSize int64, season int) []Release {
	if ex, _ := ExcludeMatcher(s.cfg().ExcludeWords); ex != nil {
		kept := make([]Release, 0, len(rels))
		for _, r := range rels {
			if !ex(r.Name) && !ex(r.FileName) && !ex(r.Text) {
				kept = append(kept, r)
			}
		}
		rels = kept
	}
	return Rank(rels, quality, maxSize, season)
}
