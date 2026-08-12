package db

import (
	"database/sql"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
)

// PullRequest is a fully-hydrated PR as fetched from GitHub, including its
// reviews and conversation comments. Times are unix epoch seconds UTC.
type PullRequest struct {
	ID           string // GraphQL node id
	RepoID       int64
	Number       int
	Author       string
	AuthorIsBot  bool
	Title        string
	State        string // OPEN | MERGED | CLOSED
	IsDraft      bool
	CreatedAt    int64
	MergedAt     *int64
	ClosedAt     *int64
	UpdatedAt    int64
	Additions    int
	Deletions    int
	ChangedFiles int
	Reviews      []Review
	Comments     []IssueComment
}

type Review struct {
	ID           string
	Author       string
	AuthorIsBot  bool
	State        string // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED
	SubmittedAt  int64
	CommentCount int // review-thread comments authored in this review
}

type IssueComment struct {
	ID          string
	Author      string
	AuthorIsBot bool
	CreatedAt   int64
}

// Store wraps the SQL handle with Statline's write and lookup operations.
type Store struct {
	DB *sql.DB
}

func NewStore(sqldb *sql.DB) *Store { return &Store{DB: sqldb} }

// UpsertRepo ensures a repos row exists and returns its id.
func (s *Store) UpsertRepo(owner, name string) (int64, error) {
	_, err := s.DB.Exec(`INSERT INTO repos (owner, name) VALUES (?, ?)
		ON CONFLICT (owner, name) DO NOTHING`, owner, name)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM repos WHERE owner = ? AND name = ?`, owner, name).Scan(&id)
	return id, err
}

// SavePullRequests writes one fetched page transactionally. Each PR is
// upserted; its reviews and comments are delete-and-replaced, which makes
// re-syncs idempotent and handles dismissed/deleted items without diffing.
func (s *Store) SavePullRequests(prs []PullRequest) error {
	if len(prs) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for _, pr := range prs {
		if err := saveUser(tx, pr.Author, pr.AuthorIsBot); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO pull_requests
			  (id, repo_id, number, author_login, title, state, is_draft,
			   created_at, merged_at, closed_at, updated_at,
			   additions, deletions, changed_files, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
			  repo_id = excluded.repo_id, number = excluded.number,
			  author_login = excluded.author_login, title = excluded.title,
			  state = excluded.state, is_draft = excluded.is_draft,
			  created_at = excluded.created_at, merged_at = excluded.merged_at,
			  closed_at = excluded.closed_at, updated_at = excluded.updated_at,
			  additions = excluded.additions, deletions = excluded.deletions,
			  changed_files = excluded.changed_files, synced_at = excluded.synced_at`,
			pr.ID, pr.RepoID, pr.Number, pr.Author, pr.Title, pr.State, pr.IsDraft,
			pr.CreatedAt, pr.MergedAt, pr.ClosedAt, pr.UpdatedAt,
			pr.Additions, pr.Deletions, pr.ChangedFiles, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM reviews WHERE pr_id = ?`, pr.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM issue_comments WHERE pr_id = ?`, pr.ID); err != nil {
			return err
		}
		for _, r := range pr.Reviews {
			if err := saveUser(tx, r.Author, r.AuthorIsBot); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				INSERT INTO reviews (id, pr_id, author_login, state, submitted_at, comment_count)
				VALUES (?, ?, ?, ?, ?, ?)`,
				r.ID, pr.ID, r.Author, r.State, r.SubmittedAt, r.CommentCount); err != nil {
				return err
			}
		}
		for _, c := range pr.Comments {
			if err := saveUser(tx, c.Author, c.AuthorIsBot); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				INSERT INTO issue_comments (id, pr_id, author_login, created_at)
				VALUES (?, ?, ?, ?)`,
				c.ID, pr.ID, c.Author, c.CreatedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func saveUser(tx *sql.Tx, login string, isBot bool) error {
	_, err := tx.Exec(`INSERT INTO users (login, is_bot) VALUES (?, ?)
		ON CONFLICT (login) DO UPDATE SET is_bot = excluded.is_bot`, login, boolToInt(isBot))
	return err
}

// SyncState is the per-repo incremental-sync bookkeeping.
type SyncState struct {
	RepoID           int64
	WatermarkUpdated *int64
	BackfillUntil    *int64
	LastSyncedAt     *int64
	LastError        *string
}

func (s *Store) GetSyncState(repoID int64) (SyncState, error) {
	st := SyncState{RepoID: repoID}
	err := s.DB.QueryRow(`SELECT watermark_updated, backfill_until, last_synced_at, last_error
		FROM sync_state WHERE repo_id = ?`, repoID).
		Scan(&st.WatermarkUpdated, &st.BackfillUntil, &st.LastSyncedAt, &st.LastError)
	if err == sql.ErrNoRows {
		return st, nil
	}
	return st, err
}

func (s *Store) SetSyncState(st SyncState) error {
	_, err := s.DB.Exec(`
		INSERT INTO sync_state (repo_id, watermark_updated, backfill_until, last_synced_at, last_error)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (repo_id) DO UPDATE SET
		  watermark_updated = excluded.watermark_updated,
		  backfill_until = excluded.backfill_until,
		  last_synced_at = excluded.last_synced_at,
		  last_error = excluded.last_error`,
		st.RepoID, st.WatermarkUpdated, st.BackfillUntil, st.LastSyncedAt, st.LastError)
	return err
}

// MirrorTeam syncs one config team profile into the teams tables so metrics
// queries can join against membership. Config is the source of truth; rows
// not present in the config are removed. Returns the team id and the repo
// ids for the team's repos (creating repos rows as needed).
func (s *Store) MirrorTeam(t config.Team) (teamID int64, repoIDs map[string]int64, err error) {
	repoIDs = make(map[string]int64, len(t.Repos))
	for _, r := range t.Repos {
		id, err := s.UpsertRepo(r.Owner, r.Name)
		if err != nil {
			return 0, nil, err
		}
		repoIDs[r.String()] = id
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`INSERT INTO teams (name, org, gh_team_slug) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET org = excluded.org, gh_team_slug = excluded.gh_team_slug`,
		t.Name, t.Org, t.GHTeamSlug); err != nil {
		return 0, nil, err
	}
	if err = tx.QueryRow(`SELECT id FROM teams WHERE name = ?`, t.Name).Scan(&teamID); err != nil {
		return 0, nil, err
	}
	if _, err = tx.Exec(`DELETE FROM team_members WHERE team_id = ?`, teamID); err != nil {
		return 0, nil, err
	}
	for _, m := range t.Members {
		if _, err = tx.Exec(`INSERT INTO team_members (team_id, login, hidden) VALUES (?, ?, ?)`,
			teamID, m.Login, boolToInt(m.Hidden)); err != nil {
			return 0, nil, err
		}
	}
	if _, err = tx.Exec(`DELETE FROM team_repos WHERE team_id = ?`, teamID); err != nil {
		return 0, nil, err
	}
	for _, id := range repoIDs {
		if _, err = tx.Exec(`INSERT INTO team_repos (team_id, repo_id) VALUES (?, ?)`,
			teamID, id); err != nil {
			return 0, nil, err
		}
	}
	return teamID, repoIDs, tx.Commit()
}

// DeleteTeam removes a team profile row; team_members and team_repos cascade.
// Shared cache tables (repos, pull_requests, sync_state) are deliberately left
// alone: they are a rebuildable cache and repos may be shared across teams.
func (s *Store) DeleteTeam(name string) error {
	_, err := s.DB.Exec(`DELETE FROM teams WHERE name = ?`, name)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
