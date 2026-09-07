package verify

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/mwtrigg/nugctl/internal/client"
)

const (
	pollInterval = 1 * time.Second
	// pollTimeout is deliberately well above a plausible index-regen debounce
	// window on the target feed (e.g. Barn's default 30s quiet period before
	// regenerating) — setting the oracle's timeout equal to the target's
	// debounce default would make every push round-trip a coin flip.
	pollTimeout = 90 * time.Second
)

func runPush(c *client.Client, opts Options, r *Report) {
	const cat = "push"
	id := fmt.Sprintf("nugctl-verify-%d", time.Now().Unix())
	version := "1.0.0"

	data, err := buildMinimalNupkg(id, version)
	if err != nil {
		r.Add(Check{Name: "build minimal package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "build minimal package", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s version=%s", id, version)})

	if err := c.PushBytes(fmt.Sprintf("%s.%s.nupkg", id, version), data); err != nil {
		r.Add(Check{Name: "push package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "push package", Category: cat, Status: StatusPass})

	if !pollUntil(func() bool { return packageVisible(c, id, version) }) {
		r.Add(Check{Name: "package appears in search/registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("not visible after %s", pollTimeout)})
		return
	}
	r.Add(Check{Name: "package appears in search/registration", Category: cat, Status: StatusPass})

	downloaded, err := c.PullBytes(id, version)
	if err != nil {
		r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusFail, Err: err.Error()})
	} else {
		want := sha512.Sum512(data)
		got := sha512.Sum512(downloaded)
		if base64.StdEncoding.EncodeToString(want[:]) != base64.StdEncoding.EncodeToString(got[:]) {
			r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusFail, Detail: "SHA-512 of downloaded bytes does not match what was pushed"})
		} else {
			r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusPass})
		}
	}

	if err := c.DeleteWithOptions(id, version, client.DeleteOptions{Force: opts.ForceDelete}); err != nil {
		r.Add(Check{Name: "unlist/delete package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "unlist/delete package", Category: cat, Status: StatusPass})

	if !pollUntil(func() bool { return !packageInSearch(c, id) }) {
		r.Add(Check{Name: "package disappears from default search", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("still visible after %s", pollTimeout)})
	} else {
		r.Add(Check{Name: "package disappears from default search", Category: cat, Status: StatusPass})
	}

	_, err = c.PullBytes(id, version)
	switch {
	case err == nil:
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusWarn, Detail: "package still downloadable by exact version after delete — feed unlists rather than hard-deletes; test package left behind"})
	case client.IsNotFound(err):
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusPass, Detail: "package no longer downloadable — feed hard-deletes"})
	default:
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusFail, Detail: "unexpected error re-downloading after delete", Err: err.Error()})
	}
}

func packageVisible(c *client.Client, id, version string) bool {
	if !packageInSearch(c, id) {
		return false
	}
	entry, err := c.RegistrationVersion(id, version)
	return err == nil && entry != nil && entry.Version == version
}

func packageInSearch(c *client.Client, id string) bool {
	res, err := c.Search(id, 0, 20, true)
	if err != nil {
		return false
	}
	for _, p := range res.Data {
		if p.ID == id {
			return true
		}
	}
	return false
}

// pollUntil polls cond at pollInterval until it returns true or pollTimeout
// elapses. Returns whether cond became true in time.
func pollUntil(cond func() bool) bool {
	deadline := time.Now().Add(pollTimeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}
