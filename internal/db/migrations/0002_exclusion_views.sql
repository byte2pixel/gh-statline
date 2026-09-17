-- Bot and hidden-member exclusion, defined once (#106). The config
-- exclude_bots globs are mirrored into bot_globs on every startup
-- (Store.MirrorBotGlobs), as teams are; the table is a mirror, never the
-- source. Metric SQL scopes every actor column through one of the two
-- views and spells out no bot or hidden predicate of its own.

CREATE TABLE bot_globs (
  pattern TEXT PRIMARY KEY  -- SQLite GLOB: lowercase, [ escaped as [[]
);

-- Every known login that is a bot: typed Bot by GitHub, or matching a glob.
-- Logins come from users (every PR, review and comment author has a row)
-- and from the rosters, so a glob-matched member who never acted is still
-- excluded.
CREATE VIEW bot_actors AS
  SELECT login FROM users WHERE is_bot = 1
  UNION
  SELECT l.login
  FROM (SELECT login FROM users UNION SELECT login FROM team_members) l
  WHERE EXISTS (SELECT 1 FROM bot_globs g WHERE lower(l.login) GLOB g.pattern);

-- The members a team's views show: on the roster, not hidden, not a bot.
CREATE VIEW visible_members AS
  SELECT tm.team_id, tm.login
  FROM team_members tm
  WHERE tm.hidden = 0
    AND tm.login NOT IN (SELECT login FROM bot_actors);
