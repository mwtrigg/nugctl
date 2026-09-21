package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Category classifies a client error for command-level messaging.
type Category int

const (
	CatUnknown      Category = iota
	CatUnauthorized          // 401/403
	CatNotFound              // 404
	CatNotSupported          // 405/501 — endpoint not implemented by this feed
	CatRateLimited           // 429
	CatServerError           // 5xx
	CatNetwork               // DNS, connection refused, timeout
)

// ClientError is returned by all Client methods instead of raw HTTP errors.
type ClientError struct {
	Category   Category
	StatusCode int    // 0 for network errors
	Message    string // server response body or network error detail
	URL        string
	// RetryAfter is the response's Retry-After header (RFC 9110 §10.2.3),
	// parsed as either delta-seconds or an HTTP-date, if present. Zero
	// means the header was absent or unparseable.
	RetryAfter time.Duration
}

func (e *ClientError) Error() string {
	switch e.Category {
	case CatUnauthorized:
		return fmt.Sprintf("authentication failed (HTTP %d) — verify your API key or run: nugctl auth login", e.StatusCode)
	case CatNotFound:
		return fmt.Sprintf("not found (HTTP 404): %s", e.URL)
	case CatNotSupported:
		return fmt.Sprintf("endpoint not supported by this feed (HTTP %d) — feature may require NuGet.org or Azure Artifacts", e.StatusCode)
	case CatServerError:
		return fmt.Sprintf("server error (HTTP %d): %s", e.StatusCode, e.Message)
	case CatNetwork:
		return fmt.Sprintf("network error reaching %s: %s", e.URL, e.Message)
	default:
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
}

// IsUnauthorized reports whether err is a CatUnauthorized ClientError.
func IsUnauthorized(err error) bool { return hasCategory(err, CatUnauthorized) }

// IsNotFound reports whether err is a CatNotFound ClientError.
func IsNotFound(err error) bool { return hasCategory(err, CatNotFound) }

// IsNotSupported reports whether err is a CatNotSupported ClientError.
func IsNotSupported(err error) bool { return hasCategory(err, CatNotSupported) }

// Retryable reports whether err is a rate-limit (429) or service-unavailable
// (503) response — the two statuses a client-side retry loop should treat
// as transient rather than fatal — and returns the server's requested
// Retry-After delay, if it sent one (zero otherwise).
func Retryable(err error) (retryAfter time.Duration, ok bool) {
	var ce *ClientError
	if !errors.As(err, &ce) {
		return 0, false
	}
	if ce.StatusCode == http.StatusTooManyRequests || ce.StatusCode == http.StatusServiceUnavailable {
		return ce.RetryAfter, true
	}
	return 0, false
}

func hasCategory(err error, c Category) bool {
	var ce *ClientError
	return errors.As(err, &ce) && ce.Category == c
}

func categorize(statusCode int, body, rawURL string) *ClientError {
	cat := CatUnknown
	switch {
	case statusCode == 401 || statusCode == 403:
		cat = CatUnauthorized
	case statusCode == 404:
		cat = CatNotFound
	case statusCode == 405 || statusCode == 501:
		cat = CatNotSupported
	case statusCode == 429:
		cat = CatRateLimited
	case statusCode >= 500:
		cat = CatServerError
	}
	return &ClientError{Category: cat, StatusCode: statusCode, Message: body, URL: rawURL}
}

// categorizeResp is like categorize but also captures the response's
// Retry-After header, so retry logic can honor a feed's requested delay.
func categorizeResp(resp *http.Response, body, rawURL string) *ClientError {
	ce := categorize(resp.StatusCode, body, rawURL)
	ce.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	return ce
}

// parseRetryAfter parses a Retry-After header value per RFC 9110 §10.2.3:
// either delta-seconds ("120") or an HTTP-date. Returns 0 if v is empty,
// unparseable, or already in the past.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func networkError(rawURL string, err error) *ClientError {
	msg := err.Error()
	// strip noisy Go URL wrapping: `Get "https://...": dial tcp ...`
	if ue := new(url.Error); errors.As(err, &ue) {
		msg = ue.Err.Error()
	}
	return &ClientError{Category: CatNetwork, URL: rawURL, Message: msg}
}
