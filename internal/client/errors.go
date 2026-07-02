package client

import (
	"errors"
	"fmt"
	"net/url"
)

// Category classifies a client error for command-level messaging.
type Category int

const (
	CatUnknown      Category = iota
	CatUnauthorized          // 401/403
	CatNotFound              // 404
	CatNotSupported          // 405/501 — endpoint not implemented by this feed
	CatServerError           // 5xx
	CatNetwork               // DNS, connection refused, timeout
)

// ClientError is returned by all Client methods instead of raw HTTP errors.
type ClientError struct {
	Category   Category
	StatusCode int    // 0 for network errors
	Message    string // server response body or network error detail
	URL        string
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
	case statusCode >= 500:
		cat = CatServerError
	}
	return &ClientError{Category: cat, StatusCode: statusCode, Message: body, URL: rawURL}
}

func networkError(rawURL string, err error) *ClientError {
	msg := err.Error()
	// strip noisy Go URL wrapping: `Get "https://...": dial tcp ...`
	if ue := new(url.Error); errors.As(err, &ue) {
		msg = ue.Err.Error()
	}
	return &ClientError{Category: CatNetwork, URL: rawURL, Message: msg}
}
