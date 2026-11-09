package streaming

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/config"
)

// ExcludeMatcher compiles the owner's exclude list. Each entry is a whole
// word ("TS" doesn't match "DTS-HD"), a phrase whose words may be split by
// spaces, dots, underscores or dashes ("korean dub" matches "Korean.Dub"),
// or a regular expression written /like this/ (flags after the last slash,
// e.g. /hevc/i). Words and phrases ignore case.
func ExcludeMatcher(words []string) (func(string) bool, error) {
	res, err := wordPatterns(words)
	if err != nil || len(res) == 0 {
		return nil, err
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

// PreferMatcher compiles the owner's preferred words (written like
// ExcludeMatcher's) into a score: the first entry a name has counts for the
// most, so the list reads in order of preference.
func PreferMatcher(words []string) (func(string) int, error) {
	res, err := wordPatterns(words)
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return func(s string) int {
		for i, re := range res {
			if re.MatchString(s) {
				return len(res) - i
			}
		}
		return 0
	}, nil
}

// wordPatterns compiles a list of words, phrases and /regular expressions/.
func wordPatterns(words []string) ([]*regexp.Regexp, error) {
	var res []*regexp.Regexp
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if re, ok, err := slashRegexp(w); ok {
			if err != nil {
				return nil, fmt.Errorf("%q: %w", w, err)
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
	return res, nil
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

// rank is Rank without releases the owner excluded, and with their size
// floor and preferred words.
func (s *Service) rank(rels []Release, quality string, maxSize int64, season int) []Release {
	cfg := s.cfg()
	if ex, _ := ExcludeMatcher(cfg.ExcludeWords); ex != nil {
		kept := make([]Release, 0, len(rels))
		for _, r := range rels {
			if !ex(r.Name) && !ex(r.FileName) && !ex(r.Text) {
				kept = append(kept, r)
			}
		}
		rels = kept
	}
	prefer, _ := PreferMatcher(cfg.PreferWords)
	return RankWith(rels, quality, RankOptions{MaxSize: maxSize, MinSize: int64(cfg.MinSizeGB[quality] * (1 << 30)), Season: season,
		Prefer: prefer, Indexer: indexerOrder(cfg.Indexers)})
}

// indexerOrder ranks indexers as the owner listed them: the first is tried
// first. A name it doesn't know comes last.
func indexerOrder(ixs []config.IndexerConfig) func(string) int {
	if len(ixs) < 2 {
		return nil
	}
	return func(name string) int {
		for i, ix := range ixs {
			if ix.Name == name {
				return i
			}
		}
		return len(ixs)
	}
}
