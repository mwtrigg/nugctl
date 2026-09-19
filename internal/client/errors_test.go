package client

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"delta seconds", "120", 120 * time.Second},
		{"zero seconds", "0", 0},
		{"negative seconds treated as absent", "-5", 0},
		{"garbage treated as absent", "not-a-value", 0},
		{"http-date in the past treated as absent", "Sun, 06 Nov 1994 08:49:37 GMT", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDateInFuture(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(httpTimeFormat)
	got := parseRetryAfter(future)
	if got <= 0 || got > 91*time.Second {
		t.Errorf("parseRetryAfter(%q) = %v, want roughly 90s", future, got)
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantOK       bool
		checkWait    bool
		wantWaitOver time.Duration
	}{
		{"429 is retryable", &ClientError{Category: CatRateLimited, StatusCode: 429}, true, false, 0},
		{"503 is retryable", &ClientError{Category: CatServerError, StatusCode: 503}, true, false, 0},
		{"500 is not retryable", &ClientError{Category: CatServerError, StatusCode: 500}, false, false, 0},
		{"404 is not retryable", &ClientError{Category: CatNotFound, StatusCode: 404}, false, false, 0},
		{"non-ClientError is not retryable", errFake("boom"), false, false, 0},
		{"429 with Retry-After carries it through", &ClientError{Category: CatRateLimited, StatusCode: 429, RetryAfter: 30 * time.Second}, true, true, 29 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after, ok := Retryable(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("Retryable() ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.checkWait && after <= tc.wantWaitOver {
				t.Errorf("Retryable() retryAfter = %v, want > %v", after, tc.wantWaitOver)
			}
		})
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// httpTimeFormat mirrors net/http's internal TimeFormat (not exported) for
// building a valid HTTP-date test fixture.
const httpTimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"
