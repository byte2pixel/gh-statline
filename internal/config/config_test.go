package config

import "testing"

func TestBotMatcher(t *testing.T) {
	m := NewBotMatcher([]string{"*[bot]", "dependabot*", "renovate*", "exact-name", "c?-bot"})
	cases := []struct {
		login string
		want  bool
	}{
		// Literal brackets must match (path.Match would treat [bot] as a class).
		{"github-actions[bot]", true},
		{"ci-robot[bot]", true},
		{"dependabot", true},
		{"dependabot-preview", true},
		{"renovate-gtw", true},
		{"Renovate-GTW", true}, // case-insensitive
		{"exact-name", true},
		{"exact-name-2", false}, // anchored: no partial match
		{"ci-bot", true},        // ? matches one char
		{"cxy-bot", false},
		{"alice", false},
		{"bot", false}, // "*[bot]" needs the literal brackets
	}
	for _, c := range cases {
		if got := m.IsBot(c.login); got != c.want {
			t.Errorf("IsBot(%q) = %v, want %v", c.login, got, c.want)
		}
	}
	// Cache path: same answer on repeat.
	if !m.IsBot("dependabot") {
		t.Error("cached IsBot flipped")
	}
}

func TestApplyDefaults(t *testing.T) {
	var c Config
	c.Teams = []Team{{Name: "a"}, {Name: "b"}}
	c.Sync.PageSize = 500 // out of range: reset to default
	c.ApplyDefaults()

	if c.UI.Window != "30d" || c.UI.Sort != "prs_merged" {
		t.Errorf("UI defaults = %+v", c.UI)
	}
	if c.Sync.BackfillDays != 120 || c.Sync.PageSize != 25 || c.Sync.Concurrency != 3 {
		t.Errorf("Sync defaults = %+v", c.Sync)
	}
	if len(c.ExcludeBots) == 0 {
		t.Error("ExcludeBots not defaulted")
	}
	if c.DefaultTeam != "a" {
		t.Errorf("DefaultTeam = %q, want first team", c.DefaultTeam)
	}

	// Existing values are preserved.
	c2 := Config{UI: UI{Window: "7d", Sort: "prs_opened"}, DefaultTeam: "x",
		Teams: []Team{{Name: "x"}}}
	c2.ApplyDefaults()
	if c2.UI.Window != "7d" || c2.UI.Sort != "prs_opened" || c2.DefaultTeam != "x" {
		t.Errorf("existing values clobbered: %+v default=%q", c2.UI, c2.DefaultTeam)
	}
}

func TestValidate(t *testing.T) {
	valid := Config{
		DefaultTeam: "t",
		Teams: []Team{{
			Name:    "t",
			Members: []Member{{Login: "alice"}},
			Repos:   []Repo{{Owner: "acme", Name: "api"}},
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"no teams", func(c *Config) { c.Teams = nil }},
		{"empty team name", func(c *Config) { c.Teams[0].Name = "" }},
		{"duplicate team", func(c *Config) { c.Teams = append(c.Teams, c.Teams[0]) }},
		{"repo missing owner", func(c *Config) { c.Teams[0].Repos[0].Owner = "" }},
		{"repo missing name", func(c *Config) { c.Teams[0].Repos[0].Name = "" }},
		{"member empty login", func(c *Config) { c.Teams[0].Members[0].Login = "" }},
		{"unknown default team", func(c *Config) { c.DefaultTeam = "nope" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			c.Teams = append([]Team{}, valid.Teams...)
			c.Teams[0].Members = append([]Member{}, valid.Teams[0].Members...)
			c.Teams[0].Repos = append([]Repo{}, valid.Teams[0].Repos...)
			tc.mut(&c)
			if err := c.Validate(); err == nil {
				t.Error("invalid config accepted")
			}
		})
	}
}

func TestTeamByName(t *testing.T) {
	c := Config{DefaultTeam: "b", Teams: []Team{{Name: "a"}, {Name: "b"}}}
	if got, ok := c.TeamByName(""); !ok || got.Name != "b" {
		t.Errorf("empty name → default: got %q ok=%v", got.Name, ok)
	}
	if got, ok := c.TeamByName("a"); !ok || got.Name != "a" {
		t.Errorf("by name: got %q ok=%v", got.Name, ok)
	}
	if _, ok := c.TeamByName("missing"); ok {
		t.Error("missing team reported found")
	}
}
