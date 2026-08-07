package gh

import (
	"context"
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
