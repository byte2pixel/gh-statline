// Package config defines Statline's user-editable configuration: team
// profiles (members + repos), UI preferences, and sync tuning. The YAML file
// on disk is the source of truth; the wizard writes it and users may edit it
// freely between runs.
package config

import "fmt"

type Config struct {
	DefaultTeam string   `yaml:"default_team"`
	ExcludeBots []string `yaml:"exclude_bots"`
	Teams       []Team   `yaml:"teams"`
	UI          UI       `yaml:"ui"`
	Sync        Sync     `yaml:"sync"`
}

type Team struct {
	Name string `yaml:"name"`
	Org  string `yaml:"org"`
	// GHTeamSlug records which GitHub org team seeded this profile, enabling
	// re-import later. Empty for fully manual teams.
	GHTeamSlug string   `yaml:"gh_team_slug,omitempty"`
	Members    []Member `yaml:"members"`
	Repos      []Repo   `yaml:"repos"`
}

type Member struct {
	Login string `yaml:"login"`
	// Hidden keeps the member in config (and their data in the cache) but
	// excludes them from views — e.g. someone who left the team.
	Hidden bool `yaml:"hidden,omitempty"`
}

type Repo struct {
	Owner string `yaml:"owner"`
	Name  string `yaml:"name"`
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

type UI struct {
	Window string `yaml:"window"` // e.g. "7d", "14d", "30d", "90d"
	Sort   string `yaml:"sort"`   // leaderboard sort column key
}

type Sync struct {
	BackfillDays int `yaml:"backfill_days"`
	PageSize     int `yaml:"page_size"`
	Concurrency  int `yaml:"concurrency"`
}

// Default returns the settings applied when fields are absent from the file.
func Default() Config {
	return Config{
		ExcludeBots: []string{"*[bot]", "dependabot*", "renovate*"},
		UI:          UI{Window: "30d", Sort: "prs_merged"},
		Sync:        Sync{BackfillDays: 90, PageSize: 25, Concurrency: 3},
	}
}

// ApplyDefaults fills zero-valued fields with defaults after load.
func (c *Config) ApplyDefaults() {
	d := Default()
	if c.ExcludeBots == nil {
		c.ExcludeBots = d.ExcludeBots
	}
	if c.UI.Window == "" {
		c.UI.Window = d.UI.Window
	}
	if c.UI.Sort == "" {
		c.UI.Sort = d.UI.Sort
	}
	if c.Sync.BackfillDays <= 0 {
		c.Sync.BackfillDays = d.Sync.BackfillDays
	}
	if c.Sync.PageSize <= 0 || c.Sync.PageSize > 100 {
		c.Sync.PageSize = d.Sync.PageSize
	}
	if c.Sync.Concurrency <= 0 {
		c.Sync.Concurrency = d.Sync.Concurrency
	}
	if c.DefaultTeam == "" && len(c.Teams) > 0 {
		c.DefaultTeam = c.Teams[0].Name
	}
}

// Validate reports structural problems a hand-edited file may contain.
func (c *Config) Validate() error {
	if len(c.Teams) == 0 {
		return fmt.Errorf("config has no teams")
	}
	seen := map[string]bool{}
	for _, t := range c.Teams {
		if t.Name == "" {
			return fmt.Errorf("team with empty name")
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate team name %q", t.Name)
		}
		seen[t.Name] = true
		for _, r := range t.Repos {
			if r.Owner == "" || r.Name == "" {
				return fmt.Errorf("team %q: repo entries need both owner and name", t.Name)
			}
		}
		for _, m := range t.Members {
			if m.Login == "" {
				return fmt.Errorf("team %q: member with empty login", t.Name)
			}
		}
	}
	if c.DefaultTeam != "" && !seen[c.DefaultTeam] {
		return fmt.Errorf("default_team %q does not match any team", c.DefaultTeam)
	}
	return nil
}

// TeamByName returns the named team, or the default team when name is empty.
func (c *Config) TeamByName(name string) (Team, bool) {
	if name == "" {
		name = c.DefaultTeam
	}
	for _, t := range c.Teams {
		if t.Name == name {
			return t, true
		}
	}
	return Team{}, false
}
