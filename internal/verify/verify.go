// Package verify implements nugctl's conformance-oracle checks for NuGet v3
// feed implementations (Barn, BaGetter): read-only structural checks,
// negative-path checks, and an optional push round-trip.
package verify

import "github.com/mwtrigg/nugctl/internal/client"

type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusSkip Status = "skip"
)

// Check is one assertion's outcome.
type Check struct {
	Name     string `json:"name"`
	Category string `json:"category"` // "readonly" | "negative" | "push"
	Status   Status `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Err      string `json:"err,omitempty"`
}

// Report is the full result of a verify run.
type Report struct {
	FeedURL     string  `json:"feedUrl"`
	Checks      []Check `json:"checks"`
	Aborted     bool    `json:"aborted"`
	AbortReason string  `json:"abortReason,omitempty"`
}

// Add appends a check to the report.
func (r *Report) Add(c Check) {
	r.Checks = append(r.Checks, c)
}

// ExitCode implements the CI exit contract: 2 on abort, 1 on any failed
// check, 0 otherwise. Warn/skip never affect the exit code.
func (r *Report) ExitCode() int {
	if r.Aborted {
		return 2
	}
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return 1
		}
	}
	return 0
}

// Counts returns the number of checks in each status, for summary lines.
func (r *Report) Counts() (pass, fail, warn, skip int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusWarn:
			warn++
		case StatusSkip:
			skip++
		}
	}
	return
}

// Options controls which checks Run executes.
type Options struct {
	Push        bool   // also run the push round-trip
	Package     string // target package for integrity checks; "" = auto-discover
	ForceDelete bool   // append force=true to the push round-trip's cleanup delete; only valid with Push
}

// Run executes all applicable checks against c and returns the full report.
// It never returns an error — every failure mode is represented in the
// returned Report (including Report.Aborted for fatal setup failures).
func Run(c *client.Client, opts Options) *Report {
	r := &Report{FeedURL: c.BaseURL}
	runReadonly(c, opts, r)
	if r.Aborted {
		return r
	}
	runNegative(c, r)
	if opts.Push {
		runPush(c, opts, r)
	}
	return r
}
