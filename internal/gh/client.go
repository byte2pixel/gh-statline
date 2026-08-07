package gh

import (
	"context"
	"errors"
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

// IsRetryable reports whether an API error is worth retrying with backoff:
// server errors, secondary rate limits, and transport failures. Semantic
// GraphQL errors (bad query, not found, forbidden resource) are not.
func IsRetryable(err error) bool {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode >= 500:
			return true
		case httpErr.StatusCode == 403 || httpErr.StatusCode == 429:
			return true // primary/secondary rate limit
		default:
			return false
		}
	}
	var gqlErr *api.GraphQLError
	if errors.As(err, &gqlErr) {
		return false
	}
	// Anything else is transport-level (timeouts, resets, DNS): retry.
	return true
}
