package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// errBadLink is a play link that answered, but not with a playable file:
// an addon's error page, a placeholder, or a file too small to be the video.
var errBadLink = errors.New("play link")

// openLink follows an addon's play link to the file it redirects to and
// returns that URL and the file's size. Addons like AIOStreams add the
// release to the debrid account when the link is opened, so the link is
// opened again whenever the stream starts rather than its target being kept.
func openLink(ctx context.Context, link string) (string, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", addonUA)
	resp, err := upstream.Do(req)
	if err != nil {
		return "", 0, redactURL(err)
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 1)
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%w: %s", errBadLink, resp.Status)
	}
	// An addon's error or "downloading" notice comes back as a page, or as
	// a short placeholder video that doesn't support ranges.
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") {
		return "", 0, fmt.Errorf("%w: not a video (%s)", errBadLink, ct)
	}
	size := resp.ContentLength
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if _, total, ok := strings.Cut(cr, "/"); ok {
			size, _ = strconv.ParseInt(total, 10, 64)
		}
	} else if resp.StatusCode == http.StatusOK {
		return "", 0, fmt.Errorf("%w: the server doesn't support seeking", errBadLink)
	}
	if size < 50<<20 {
		return "", 0, fmt.Errorf("%w: file too small (%d bytes)", errBadLink, size)
	}
	return resp.Request.URL.String(), size, nil
}

// linkFileName names a link stream's file after the URL it redirected to
// when the addon didn't say.
func linkFileName(name, target string) string {
	if name != "" {
		return name
	}
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	b := path.Base(target)
	if u, err := url.PathUnescape(b); err == nil {
		b = u
	}
	if extOf(b) != "" {
		return b
	}
	return "video.mkv"
}
