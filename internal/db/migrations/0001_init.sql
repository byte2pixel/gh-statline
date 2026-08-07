CREATE TABLE users (
  login  TEXT PRIMARY KEY,
  name   TEXT,
  is_bot INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE repos (
  id    INTEGER PRIMARY KEY,
  owner TEXT NOT NULL,
  name  TEXT NOT NULL,
  UNIQUE (owner, name)
);

-- teams/team_members/team_repos mirror the config file for joins;
-- the config file is the source of truth and is re-mirrored on load.
CREATE TABLE teams (
  id           INTEGER PRIMARY KEY,
  name         TEXT UNIQUE NOT NULL,
  org          TEXT,
  gh_team_slug TEXT
);

CREATE TABLE team_members (
  team_id INTEGER NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
  login   TEXT NOT NULL,
  hidden  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (team_id, login)
);

CREATE TABLE team_repos (
  team_id INTEGER NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
  repo_id INTEGER NOT NULL REFERENCES repos (id),
  PRIMARY KEY (team_id, repo_id)
);

CREATE TABLE pull_requests (
  id            TEXT PRIMARY KEY, -- GraphQL node id
  repo_id       INTEGER NOT NULL REFERENCES repos (id),
  number        INTEGER NOT NULL,
  author_login  TEXT NOT NULL,
  title         TEXT NOT NULL,
  state         TEXT NOT NULL, -- OPEN | MERGED | CLOSED
  is_draft      INTEGER NOT NULL,
  created_at    INTEGER NOT NULL, -- all times: unix epoch seconds, UTC
  merged_at     INTEGER,
  closed_at     INTEGER,
  updated_at    INTEGER NOT NULL, -- drives incremental sync
  additions     INTEGER NOT NULL,
  deletions     INTEGER NOT NULL,
  changed_files INTEGER NOT NULL,
  synced_at     INTEGER NOT NULL,
  UNIQUE (repo_id, number)
);

CREATE TABLE reviews (
  id            TEXT PRIMARY KEY,
  pr_id         TEXT NOT NULL REFERENCES pull_requests (id) ON DELETE CASCADE,
  author_login  TEXT NOT NULL,
  state         TEXT NOT NULL, -- APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED
  submitted_at  INTEGER NOT NULL,
  comment_count INTEGER NOT NULL DEFAULT 0 -- review-thread comments authored in this review
);

-- PR conversation-tab comments, one row each.
CREATE TABLE issue_comments (
  id           TEXT PRIMARY KEY,
  pr_id        TEXT NOT NULL REFERENCES pull_requests (id) ON DELETE CASCADE,
  author_login TEXT NOT NULL,
  created_at   INTEGER NOT NULL
);

CREATE TABLE sync_state (
  repo_id           INTEGER PRIMARY KEY REFERENCES repos (id),
  watermark_updated INTEGER, -- max PR updatedAt fully processed
  backfill_until    INTEGER, -- oldest updatedAt horizon covered
  last_synced_at    INTEGER,
  last_error        TEXT
);

CREATE INDEX idx_pr_author_created ON pull_requests (author_login, created_at);
CREATE INDEX idx_pr_merged ON pull_requests (merged_at);
CREATE INDEX idx_pr_repo_updated ON pull_requests (repo_id, updated_at);
CREATE INDEX idx_rev_author_time ON reviews (author_login, submitted_at);
CREATE INDEX idx_rev_pr ON reviews (pr_id);
CREATE INDEX idx_ic_author_time ON issue_comments (author_login, created_at);
CREATE INDEX idx_ic_pr ON issue_comments (pr_id);
