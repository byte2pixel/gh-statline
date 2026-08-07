package gh

import (
	"context"
	"time"
)

// Actor is a GraphQL actor with enough type info to flag bots. A nil Actor
// means the account was deleted ("ghost").
type Actor struct {
	Login    string `json:"login"`
	TypeName string `json:"__typename"`
}

func (a *Actor) SafeLogin() string {
	if a == nil || a.Login == "" {
		return "ghost"
	}
	return a.Login
}

func (a *Actor) IsBot() bool { return a != nil && a.TypeName == "Bot" }

// RateLimit mirrors the GraphQL rateLimit block returned with every query.
type RateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

const prPageQuery = `
query PRPage($owner: String!, $name: String!, $cursor: String, $pageSize: Int!) {
  rateLimit { cost remaining resetAt }
  repository(owner: $owner, name: $name) {
    pullRequests(first: $pageSize, orderBy: {field: UPDATED_AT, direction: DESC}, after: $cursor) {
      pageInfo { hasNextPage endCursor }
      nodes {
        id number title state isDraft
        author { login __typename }
        createdAt updatedAt mergedAt closedAt
        additions deletions changedFiles
        reviews(first: 50) {
          totalCount
          nodes {
            id author { login __typename } state submittedAt
            comments { totalCount }
          }
        }
        comments(first: 50) {
          totalCount
          nodes { id author { login __typename } createdAt }
        }
      }
    }
  }
}`

type PRPage struct {
	RateLimit   RateLimit
	HasNextPage bool
	EndCursor   string
	Nodes       []PRNode
}

type PRNode struct {
	ID           string     `json:"id"`
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	State        string     `json:"state"`
	IsDraft      bool       `json:"isDraft"`
	Author       *Actor     `json:"author"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	MergedAt     *time.Time `json:"mergedAt"`
	ClosedAt     *time.Time `json:"closedAt"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changedFiles"`
	Reviews      struct {
		TotalCount int          `json:"totalCount"`
		Nodes      []ReviewNode `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		TotalCount int           `json:"totalCount"`
		Nodes      []CommentNode `json:"nodes"`
	} `json:"comments"`
}

type ReviewNode struct {
	ID     string `json:"id"`
	Author *Actor `json:"author"`
	State  string `json:"state"`
	// SubmittedAt is null for PENDING (unsubmitted) reviews, which only the
	// review author can see; callers skip those.
	SubmittedAt *time.Time `json:"submittedAt"`
	Comments    struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
}

type CommentNode struct {
	ID        string    `json:"id"`
	Author    *Actor    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

// FetchPRPage fetches one page of a repo's pull requests, most recently
// updated first.
func FetchPRPage(ctx context.Context, doer Doer, owner, name, cursor string, pageSize int) (*PRPage, error) {
	vars := map[string]interface{}{
		"owner":    owner,
		"name":     name,
		"pageSize": pageSize,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}
	var resp struct {
		RateLimit  RateLimit `json:"rateLimit"`
		Repository struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []PRNode `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := doer.DoWithContext(ctx, prPageQuery, vars, &resp); err != nil {
		return nil, err
	}
	return &PRPage{
		RateLimit:   resp.RateLimit,
		HasNextPage: resp.Repository.PullRequests.PageInfo.HasNextPage,
		EndCursor:   resp.Repository.PullRequests.PageInfo.EndCursor,
		Nodes:       resp.Repository.PullRequests.Nodes,
	}, nil
}
