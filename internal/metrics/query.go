package metrics

import (
	"database/sql"
	"encoding/json"
)

// The rule for every query in this package: a query never spells out a bot
// or hidden-member predicate, and never appends a positional argument.
// Exclusion lives in the database as two views fed by the config mirror
// (migration 0002): visible_members(team_id, login) is the roster minus
// hidden members and bots, bot_actors(login) is every known login typed
// Bot by GitHub or matching an exclude_bots glob. A query scopes an actor
// column with visibleMember or notBot, scopes rows to the team with
// teamRepos, honours the repo picker with repoScope, and binds namedArgs.
// Adding a metric means writing the SELECT and nothing about exclusion.
//
// Every statement receives the same four named parameters whether or not
// it uses them all: :team, :start, :end, and :repos, the picker's repo ids
// as one JSON array or NULL. A parameter a statement does not mention is
// simply not bound, so argument order cannot be wrong.

// teamRepos joins pull_requests p to the team's repos.
func teamRepos() string {
	return ` JOIN team_repos tr ON tr.repo_id = p.repo_id AND tr.team_id = :team`
}

// repoScope narrows pull_requests p to the picker's repos; NULL means every
// team repo.
func repoScope() string {
	return ` AND (:repos IS NULL OR p.repo_id IN (SELECT value FROM json_each(:repos)))`
}

// visibleMember restricts an actor column to the members the team's views
// show: on the roster, not hidden, not a bot.
func visibleMember(col string) string {
	return " AND " + col + ` IN (SELECT login FROM visible_members WHERE team_id = :team)`
}

// notBot keeps a counterparty column human: a reviewer or commenter who is
// not on the roster still counts towards a member, unless it is a bot.
func notBot(col string) string {
	return " AND " + col + ` NOT IN (SELECT login FROM bot_actors)`
}

// namedArgs is the parameter set every query binds.
func namedArgs(f Filter, w Window) []any {
	return []any{
		sql.Named("team", f.TeamID),
		sql.Named("start", w.Start),
		sql.Named("end", w.End),
		sql.Named("repos", jsonOrNil(f.RepoIDs)),
	}
}

// jsonOrNil encodes the repo filter for json_each, or NULL when the filter
// is off: nil and empty both mean every team repo.
func jsonOrNil(ids []int64) any {
	if len(ids) == 0 {
		return nil
	}
	b, err := json.Marshal(ids)
	if err != nil {
		// A slice of int64 always encodes.
		panic(err)
	}
	return string(b)
}
