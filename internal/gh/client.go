package gh

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Doer is the one seam between Statline and the GitHub GraphQL API; tests
// substitute a fake returning recorded fixtures.
type Doer interface {
	DoWithContext(ctx context.Context, query string, variables map[string]interface{}, response interface{}) error
}

// NewClient builds an authenticated GraphQL client for github.com.
func NewClient() (Doer, error) {
	token, err := Token()
	if err != nil {
		return nil, err
	}
	return api.NewGraphQLClient(api.ClientOptions{
		Host:      host,
		AuthToken: token,
		Timeout:   60 * time.Second,
	})
}

// Retryability is the retry loop's reading of an API error.
type Retryability struct {
	// Retry reports whether re-issuing the same request can succeed.
	Retry bool
	// Wait is the server's own minimum delay before that retry, from
	// Retry-After or the quota reset time; zero when it named none and the
	// caller's backoff alone applies. Already bounded by the caps below.
	Wait time.Duration
	// RateLimited marks quota exhaustion. The budget is shared across the
	// token, so the caller should pause every worker, not just this request.
	RateLimited bool
}

const (
	// secondaryLimitWait is GitHub's documented floor for a secondary rate
	// limit that arrives without a Retry-After header.
	secondaryLimitWait = 60 * time.Second
	// maxRetryAfter bounds a Retry-After header. The value is an arbitrary
	// number and a bogus one must not park a worker for hours.
	maxRetryAfter = 10 * time.Minute
	// maxResetWait bounds a wait derived from x-ratelimit-reset. The primary
	// window is an hour, so a genuine reset is never further away.
	maxResetWait = time.Hour
)

// Classify reports whether an API error is worth retrying and how long the
// server says to wait first. Server errors, rate limits, transient GraphQL
// faults and transport failures are retryable; semantic errors (bad query,
// not found, forbidden resource, missing scope) are not.
func Classify(err error, now time.Time) Retryability {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		return classifyHTTP(httpErr, now)
	}
	var gqlErr *api.GraphQLError
	if errors.As(err, &gqlErr) {
		return classifyGraphQL(gqlErr)
	}
	// Anything else is transport-level (timeouts, resets, DNS): retry.
	return Retryability{Retry: true}
}

func classifyHTTP(e *api.HTTPError, now time.Time) Retryability {
	switch {
	case e.StatusCode >= 500:
		return Retryability{Retry: true}
	case e.StatusCode == http.StatusTooManyRequests:
		return rateLimited(e.Headers, now)
	case e.StatusCode == http.StatusForbidden:
		// 403 is also SAML enforcement, a missing scope, an IP allowlist
		// and revoked access — all permanent, and retrying them burns the
		// whole ladder for nothing. Only a rate-limit signal earns a retry:
		// a spent primary quota, a Retry-After, or the secondary-limit
		// message.
		if e.Headers.Get("X-Ratelimit-Remaining") == "0" || e.Headers.Get("Retry-After") != "" || isSecondaryLimit(e) {
			return rateLimited(e.Headers, now)
		}
		return Retryability{}
	default:
		return Retryability{}
	}
}

// rateLimited builds the verdict for a rate-limit reply from the server's
// own instruction on when to come back: Retry-After (seconds) for a
// secondary limit, else x-ratelimit-reset (unix seconds) when
// x-ratelimit-remaining says the primary quota is spent, else GitHub's
// documented floor. The reset header alone is not enough — it is on every
// response and names the primary window, about which a secondary-limit
// reply says nothing.
func rateLimited(h http.Header, now time.Time) Retryability {
	r := Retryability{Retry: true, RateLimited: true, Wait: secondaryLimitWait}
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 {
		r.Wait = min(time.Duration(secs)*time.Second, maxRetryAfter)
		return r
	}
	if h.Get("X-Ratelimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			r.Wait = min(max(time.Unix(reset, 0).Sub(now), 0), maxResetWait)
		}
	}
	return r
}

func isSecondaryLimit(e *api.HTTPError) bool {
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "secondary rate limit") || strings.Contains(msg, "abuse detection")
}

// transientGraphQLTypes are the error types GitHub attaches to a 200
// response when the fault is on its side — a nested field timing out, an
// internal error, a spent budget — which a fresh request usually clears.
var transientGraphQLTypes = map[string]bool{
	"INTERNAL":            true,
	"SERVICE_UNAVAILABLE": true,
	"TIMEOUT":             true,
	"RATE_LIMITED":        true,
}

// classifyGraphQL retries only when every error item is transient. A
// single semantic item (NOT_FOUND, FORBIDDEN, a bad query) makes the whole
// reply permanent, and an empty list says nothing and is treated the same.
// RATE_LIMITED here is the primary budget, but a 200 reply carries no
// headers to read the reset from, so it gets the documented floor.
func classifyGraphQL(e *api.GraphQLError) Retryability {
	if len(e.Errors) == 0 {
		return Retryability{}
	}
	r := Retryability{Retry: true}
	for _, item := range e.Errors {
		if !transientGraphQLTypes[item.Type] {
			return Retryability{}
		}
		if item.Type == "RATE_LIMITED" {
			r.RateLimited = true
			r.Wait = secondaryLimitWait
		}
	}
	return r
}
