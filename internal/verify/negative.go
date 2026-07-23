package verify

import (
	"fmt"
	"math/rand"

	"github.com/mwtrigg/nugctl/internal/client"
)

func runNegative(c *client.Client, r *Report) {
	const cat = "negative"
	nonexistentID := fmt.Sprintf("nugctl-verify-does-not-exist-%d", rand.Int63())

	_, err := c.Registration(nonexistentID)
	switch {
	case err == nil:
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusFail, Detail: "expected an error, got HTTP 200"})
	case client.IsNotFound(err):
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusPass})
	default:
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusFail, Detail: "expected a 404, got a different error", Err: err.Error()})
	}

	if c.APIKey == "" && c.BasicAuthUser == "" {
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusSkip, Detail: "profile has no credentials configured"})
		return
	}

	bad := client.New(c.BaseURL, "definitely-not-a-real-api-key", c.Verbose, c.Insecure, true)
	if c.BasicAuthUser != "" {
		bad.BasicAuthUser = c.BasicAuthUser
		bad.BasicAuthPass = "definitely-not-the-real-password"
	}
	delErr := bad.Delete(nonexistentID, "1.0.0")
	switch {
	case delErr == nil:
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusFail, Detail: "expected an auth error, got HTTP 2xx"})
	case client.IsUnauthorized(delErr):
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusPass})
	case client.IsNotFound(delErr):
		// The v3 spec doesn't mandate whether a server checks existence or
		// auth first. A server that checks existence first will 404 here
		// with the bad credentials never evaluated — that's not proof the
		// server accepts bad credentials, just that this probe can't tell.
		// Treating it as a hard fail would false-fail legitimate servers.
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusWarn, Detail: "got 404 instead of 401/403 — server may check package existence before enforcing auth; this probe can't confirm auth is actually enforced"})
	default:
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusFail, Detail: "expected 401/403, got a different error", Err: delErr.Error()})
	}
}
