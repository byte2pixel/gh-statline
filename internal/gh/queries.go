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

// IsBot reports a GitHub App account. An actor without a login is never one:
// those collapse into the shared "ghost" login, and users.is_bot is
// last-write-wins per login, so a single flagged sighting would hide every
// deleted account's activity at once.
func (a *Actor) IsBot() bool { return a != nil && a.Login != "" && a.TypeName == "Bot" }

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
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes {
        id number title state isDraft
        author { login __typename }
        createdAt updatedAt mergedAt closedAt
        additions deletions changedFiles
        reviews(first: 50) {
          totalCount
          pageInfo { hasNextPage endCursor }
          nodes {
            id author { login __typename } state submittedAt
            comments { totalCount }
          }
        }
        comments(first: 50) {
          totalCount
          pageInfo { hasNextPage endCursor }
          nodes { id author { login __typename } createdAt }
        }
      }
    }
  }
}`

type PRPage struct {
	RateLimit   RateLimit
	TotalCount  int
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
		PageInfo   PageInfo     `json:"pageInfo"`
		Nodes      []ReviewNode `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		TotalCount int           `json:"totalCount"`
		PageInfo   PageInfo      `json:"pageInfo"`
		Nodes      []CommentNode `json:"nodes"`
	} `json:"comments"`
}

type PageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
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

// maxListPages caps every wizard walk at 50 pages of 100 nodes. It is a
// loop guard against an API that never stops offering a next page, not a
// quota saver: even the ceiling costs about one rate-limit point per page.
// The wizard reports any shortfall from the connection's totalCount, so a
// walk stopped here is still an honest one.
const maxListPages = 50

// walkPages pages one connection to its end or to maxListPages. fetch runs
// one request after cursor (empty on the first page) and returns that
// page's nodes, the connection's totalCount, and its pageInfo. Any error
// discards everything: the wizard has no resume path, so a partial list
// would look like a complete one.
func walkPages[T any](fetch func(cursor string) ([]T, int, PageInfo, error)) ([]T, int, error) {
	var all []T
	var total int
	cursor := ""
	for range maxListPages {
		nodes, count, page, err := fetch(cursor)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, nodes...)
		total = count
		if !page.HasNextPage {
			break
		}
		cursor = page.EndCursor
	}
	return all, total, nil
}

// withCursor adds the page cursor to vars only when one is set: an
// explicit null would be a different query to the API.
func withCursor(vars map[string]interface{}, cursor string) map[string]interface{} {
	if cursor == "" {
		return vars
	}
	if vars == nil {
		vars = map[string]interface{}{}
	}
	vars["cursor"] = cursor
	return vars
}

const viewerQuery = `
query Viewer($cursor: String) {
  viewer {
    login
    organizations(first: 100, after: $cursor) {
      pageInfo { hasNextPage endCursor }
      nodes { login }
    }
  }
}`

// Viewer returns the authenticated login and every organization they
// belong to.
func Viewer(ctx context.Context, doer Doer) (login string, orgs []string, err error) {
	orgs, _, err = walkPages(func(cursor string) ([]string, int, PageInfo, error) {
		var resp struct {
			Viewer struct {
				Login         string `json:"login"`
				Organizations struct {
					PageInfo PageInfo `json:"pageInfo"`
					Nodes    []struct {
						Login string `json:"login"`
					} `json:"nodes"`
				} `json:"organizations"`
			} `json:"viewer"`
		}
		if err := doer.DoWithContext(ctx, viewerQuery, withCursor(nil, cursor), &resp); err != nil {
			return nil, 0, PageInfo{}, err
		}
		login = resp.Viewer.Login
		conn := resp.Viewer.Organizations
		page := make([]string, 0, len(conn.Nodes))
		for _, o := range conn.Nodes {
			page = append(page, o.Login)
		}
		return page, 0, conn.PageInfo, nil
	})
	if err != nil {
		return "", nil, err
	}
	return login, orgs, nil
}

// TeamInfo is one org team the viewer can see.
type TeamInfo struct {
	Slug string
	Name string
}

// TeamList is every team OrgTeams could fetch, with the count the API
// reported so the wizard can say when the list was cut.
type TeamList struct {
	Teams []TeamInfo
	Total int
}

// Missing is how many teams the API reported but did not deliver.
func (l *TeamList) Missing() int { return max(0, l.Total-len(l.Teams)) }

const orgTeamsQuery = `
query OrgTeams($org: String!, $cursor: String) {
  organization(login: $org) {
    teams(first: 100, after: $cursor, orderBy: {field: NAME, direction: ASC}) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes { slug name }
    }
  }
}`

// OrgTeams lists an organization's teams by name (requires read:org),
// paging to the end.
func OrgTeams(ctx context.Context, doer Doer, org string) (*TeamList, error) {
	teams, total, err := walkPages(func(cursor string) ([]TeamInfo, int, PageInfo, error) {
		var resp struct {
			Organization struct {
				Teams struct {
					TotalCount int      `json:"totalCount"`
					PageInfo   PageInfo `json:"pageInfo"`
					Nodes      []struct {
						Slug string `json:"slug"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"teams"`
			} `json:"organization"`
		}
		vars := withCursor(map[string]interface{}{"org": org}, cursor)
		if err := doer.DoWithContext(ctx, orgTeamsQuery, vars, &resp); err != nil {
			return nil, 0, PageInfo{}, err
		}
		conn := resp.Organization.Teams
		page := make([]TeamInfo, 0, len(conn.Nodes))
		for _, t := range conn.Nodes {
			page = append(page, TeamInfo{Slug: t.Slug, Name: t.Name})
		}
		return page, conn.TotalCount, conn.PageInfo, nil
	})
	if err != nil {
		return nil, err
	}
	return &TeamList{Teams: teams, Total: total}, nil
}

// TeamRepo is one repository assigned to an org team.
type TeamRepo struct {
	Owner    string
	Name     string
	Archived bool
}

// TeamImport is everything TeamDetails could fetch for one team, with the
// counts the API reported so the wizard can say when a list was cut.
type TeamImport struct {
	Members      []string
	MembersTotal int
	Repos        []TeamRepo
	ReposTotal   int
}

// MissingMembers is how many members the API reported but did not deliver.
func (i *TeamImport) MissingMembers() int { return max(0, i.MembersTotal-len(i.Members)) }

// MissingRepos is how many repositories the API reported but did not deliver.
func (i *TeamImport) MissingRepos() int { return max(0, i.ReposTotal-len(i.Repos)) }

// Members and repositories are separate connections on the team node, each
// with its own cursor, so they are walked as two documents: one document
// would re-fetch a finished connection on every page of the other. A small
// team pays one extra request for it.
const teamMembersQuery = `
query TeamMembers($org: String!, $slug: String!, $cursor: String) {
  organization(login: $org) {
    team(slug: $slug) {
      members(first: 100, after: $cursor) {
        totalCount
        pageInfo { hasNextPage endCursor }
        nodes { login }
      }
    }
  }
}`

const teamReposQuery = `
query TeamRepos($org: String!, $slug: String!, $cursor: String) {
  organization(login: $org) {
    team(slug: $slug) {
      repositories(first: 100, after: $cursor) {
        totalCount
        pageInfo { hasNextPage endCursor }
        nodes { name isArchived owner { login } }
      }
    }
  }
}`

// TeamDetails returns an org team's members and assigned repositories,
// paging each to the end.
func TeamDetails(ctx context.Context, doer Doer, org, slug string) (*TeamImport, error) {
	vars := func(cursor string) map[string]interface{} {
		return withCursor(map[string]interface{}{"org": org, "slug": slug}, cursor)
	}
	members, membersTotal, err := walkPages(func(cursor string) ([]string, int, PageInfo, error) {
		var resp struct {
			Organization struct {
				Team struct {
					Members struct {
						TotalCount int      `json:"totalCount"`
						PageInfo   PageInfo `json:"pageInfo"`
						Nodes      []struct {
							Login string `json:"login"`
						} `json:"nodes"`
					} `json:"members"`
				} `json:"team"`
			} `json:"organization"`
		}
		if err := doer.DoWithContext(ctx, teamMembersQuery, vars(cursor), &resp); err != nil {
			return nil, 0, PageInfo{}, err
		}
		conn := resp.Organization.Team.Members
		page := make([]string, 0, len(conn.Nodes))
		for _, m := range conn.Nodes {
			page = append(page, m.Login)
		}
		return page, conn.TotalCount, conn.PageInfo, nil
	})
	if err != nil {
		return nil, err
	}
	repos, reposTotal, err := walkPages(func(cursor string) ([]TeamRepo, int, PageInfo, error) {
		var resp struct {
			Organization struct {
				Team struct {
					Repositories struct {
						TotalCount int      `json:"totalCount"`
						PageInfo   PageInfo `json:"pageInfo"`
						Nodes      []struct {
							Name       string `json:"name"`
							IsArchived bool   `json:"isArchived"`
							Owner      struct {
								Login string `json:"login"`
							} `json:"owner"`
						} `json:"nodes"`
					} `json:"repositories"`
				} `json:"team"`
			} `json:"organization"`
		}
		if err := doer.DoWithContext(ctx, teamReposQuery, vars(cursor), &resp); err != nil {
			return nil, 0, PageInfo{}, err
		}
		conn := resp.Organization.Team.Repositories
		page := make([]TeamRepo, 0, len(conn.Nodes))
		for _, r := range conn.Nodes {
			page = append(page, TeamRepo{Owner: r.Owner.Login, Name: r.Name, Archived: r.IsArchived})
		}
		return page, conn.TotalCount, conn.PageInfo, nil
	})
	if err != nil {
		return nil, err
	}
	return &TeamImport{Members: members, MembersTotal: membersTotal, Repos: repos, ReposTotal: reposTotal}, nil
}

const prReviewsQuery = `
query PRReviews($id: ID!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  node(id: $id) {
    ... on PullRequest {
      reviews(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id author { login __typename } state submittedAt
          comments { totalCount }
        }
      }
    }
  }
}`

const prCommentsQuery = `
query PRComments($id: ID!, $cursor: String) {
  rateLimit { cost remaining resetAt }
  node(id: $id) {
    ... on PullRequest {
      comments(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes { id author { login __typename } createdAt }
      }
    }
  }
}`

// FetchAllReviews pages a single PR's remaining reviews starting after
// cursor. Used when the main walk's nested first:50 overflowed.
func FetchAllReviews(ctx context.Context, doer Doer, prID, cursor string) ([]ReviewNode, RateLimit, error) {
	var all []ReviewNode
	var rl RateLimit
	for {
		var resp struct {
			RateLimit RateLimit `json:"rateLimit"`
			Node      struct {
				Reviews struct {
					PageInfo PageInfo     `json:"pageInfo"`
					Nodes    []ReviewNode `json:"nodes"`
				} `json:"reviews"`
			} `json:"node"`
		}
		vars := map[string]interface{}{"id": prID}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		if err := doer.DoWithContext(ctx, prReviewsQuery, vars, &resp); err != nil {
			return all, rl, err
		}
		rl = resp.RateLimit
		all = append(all, resp.Node.Reviews.Nodes...)
		if !resp.Node.Reviews.PageInfo.HasNextPage {
			return all, rl, nil
		}
		cursor = resp.Node.Reviews.PageInfo.EndCursor
	}
}

// FetchAllComments pages a single PR's remaining conversation comments
// starting after cursor.
func FetchAllComments(ctx context.Context, doer Doer, prID, cursor string) ([]CommentNode, RateLimit, error) {
	var all []CommentNode
	var rl RateLimit
	for {
		var resp struct {
			RateLimit RateLimit `json:"rateLimit"`
			Node      struct {
				Comments struct {
					PageInfo PageInfo      `json:"pageInfo"`
					Nodes    []CommentNode `json:"nodes"`
				} `json:"comments"`
			} `json:"node"`
		}
		vars := map[string]interface{}{"id": prID}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		if err := doer.DoWithContext(ctx, prCommentsQuery, vars, &resp); err != nil {
			return all, rl, err
		}
		rl = resp.RateLimit
		all = append(all, resp.Node.Comments.Nodes...)
		if !resp.Node.Comments.PageInfo.HasNextPage {
			return all, rl, nil
		}
		cursor = resp.Node.Comments.PageInfo.EndCursor
	}
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
				TotalCount int `json:"totalCount"`
				PageInfo   struct {
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
		TotalCount:  resp.Repository.PullRequests.TotalCount,
		HasNextPage: resp.Repository.PullRequests.PageInfo.HasNextPage,
		EndCursor:   resp.Repository.PullRequests.PageInfo.EndCursor,
		Nodes:       resp.Repository.PullRequests.Nodes,
	}, nil
}

const prProbeQuery = `
query PRProbe($owner: String!, $name: String!) {
  rateLimit { cost remaining resetAt }
  repository(owner: $owner, name: $name) {
    pullRequests(first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      nodes { id updatedAt }
    }
  }
}`

// PRProbe is a snapshot of the top of a repo's PR list, used to detect
// concurrent mutation across a multi-page walk. A repo with no PRs has an
// empty FirstID and a zero FirstUpdated.
type PRProbe struct {
	RateLimit    RateLimit
	TotalCount   int
	FirstID      string
	FirstUpdated time.Time
}

// FetchPRProbe fetches just the most recently updated PR's identity plus
// the total PR count — one cheap round-trip, no nested connections.
func FetchPRProbe(ctx context.Context, doer Doer, owner, name string) (*PRProbe, error) {
	vars := map[string]interface{}{"owner": owner, "name": name}
	var resp struct {
		RateLimit  RateLimit `json:"rateLimit"`
		Repository struct {
			PullRequests struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					ID        string    `json:"id"`
					UpdatedAt time.Time `json:"updatedAt"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := doer.DoWithContext(ctx, prProbeQuery, vars, &resp); err != nil {
		return nil, err
	}
	probe := &PRProbe{
		RateLimit:  resp.RateLimit,
		TotalCount: resp.Repository.PullRequests.TotalCount,
	}
	if nodes := resp.Repository.PullRequests.Nodes; len(nodes) > 0 {
		probe.FirstID = nodes[0].ID
		probe.FirstUpdated = nodes[0].UpdatedAt
	}
	return probe, nil
}
