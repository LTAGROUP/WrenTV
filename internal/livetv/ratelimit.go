package livetv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Provider rate limiting. A Stremio addon or a media host can refuse this
// server for a while — HTTP 429, or a 503 that carries Retry-After. That is a
// statement about request volume, not about the manifest, the item or the
// stream, so it is reported as its own error and never mistaken for "this item
// has no streams". Callers stop asking when they see it, and an automatic
// playback attempt ends with the provider's own message instead of walking the
// rest of the feed list.

var (
	// ErrAddonRateLimited: the provider is refusing this server's requests for
	// now. It says nothing about whether the content exists.
	ErrAddonRateLimited = errors.New("the addon is rate limiting this server")
	// addonRetryCap bounds the wait one automatic retry will make. A provider
	// asking for longer than this is answered with the wait it asked for rather
	// than holding a viewer's request open.
	addonRetryCap = 2 * time.Second
	// addonRetryAttempts is how many times one addon request is repeated after a
	// rate limit. One: a burst is worth retrying once, a limit is not.
	addonRetryAttempts = 1
)

// AddonRateLimitError says which answer refused the request and how long the
// provider asked this server to wait. It carries no URL and no credentials.
type AddonRateLimitError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *AddonRateLimitError) Error() string {
	msg := fmt.Sprintf("the addon is rate limiting this server (%d %s)", e.Status, http.StatusText(e.Status))
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf("; it asked to wait %s", e.RetryAfter.Round(time.Second))
	}
	return msg
}

// Unwrap keeps errors.Is(err, ErrAddonRateLimited) working.
func (e *AddonRateLimitError) Unwrap() error { return ErrAddonRateLimited }

// retryWithin reports whether the provider's wait is short enough to sit out
// inside one request.
func (e *AddonRateLimitError) retryWithin(cap time.Duration) bool {
	return e.RetryAfter > 0 && e.RetryAfter <= cap
}

// wait is how long the caller should hold off before trying again.
func (e *AddonRateLimitError) wait(cap time.Duration) time.Duration {
	if e.RetryAfter <= 0 || e.RetryAfter > cap {
		return 0
	}
	return e.RetryAfter
}

// addonRateLimit reports whether a response is the provider refusing this
// server for now, and describes the refusal when it is.
func addonRateLimit(resp *http.Response) *AddonRateLimitError {
	if resp == nil {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
	case http.StatusServiceUnavailable:
		// A 503 with Retry-After is a load shedder saying when to come back; a
		// 503 without one is an ordinary temporary failure.
		if strings.TrimSpace(resp.Header.Get("Retry-After")) == "" {
			return nil
		}
	default:
		return nil
	}
	return &AddonRateLimitError{Status: resp.StatusCode, RetryAfter: retryAfter(resp)}
}

// RateLimited pulls the rate-limit detail out of an error, if that is what it
// is. Callers use it to stop asking and to say when to come back.
func RateLimited(err error) (*AddonRateLimitError, bool) {
	var limit *AddonRateLimitError
	if errors.As(err, &limit) {
		return limit, true
	}
	return nil, false
}

// retryAfter reads the provider's Retry-After header, in either form the HTTP
// specification allows: a number of seconds or an HTTP date.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := time.Until(at); wait > 0 {
			return wait
		}
	}
	return 0
}

// temporaryAddonError reports whether an addon failure may pass on its own: a
// provider that is unreachable, rate limiting, or slower than the wait allowed.
// A manifest the addon cannot supply is not temporary, and neither is an
// invalid answer. It is what tells "the addon is having a bad minute" apart
// from "the addon has nothing to offer", so a temporary failure is never
// remembered as an empty answer.
func temporaryAddonError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrAddonUnreachable) || errors.Is(err, ErrAddonRateLimited) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
