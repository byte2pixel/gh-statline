package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// scriptedDoer answers the wizard's three queries with canned payloads.
type scriptedDoer struct{}

func (scriptedDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	var payload string
	switch {
	case strings.Contains(query, "viewer"):
		payload = `{"viewer": {"login": "mel", "organizations": {"nodes": [{"login": "acme"}, {"login": "other-org"}]}}}`
	case strings.Contains(query, "teams(first"):
		payload = `{"organization": {"teams": {"nodes": [{"slug": "platform", "name": "Platform Eng"}]}}}`
	case strings.Contains(query, "team(slug"):
		payload = `{"organization": {"team": {
			"members": {"nodes": [{"login": "alice"}, {"login": "bob"}]},
			"repositories": {"nodes": [
				{"name": "api", "isArchived": false, "owner": {"login": "acme"}},
				{"name": "legacy", "isArchived": true, "owner": {"login": "acme"}}
			]}}}}`
	default:
		payload = `{}`
	}
	return json.Unmarshal([]byte(payload), resp)
}

// orglessDoer simulates a personal account with no org memberships.
type orglessDoer struct{}

func (orglessDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	if strings.Contains(query, "viewer") {
		return json.Unmarshal([]byte(`{"viewer": {"login": "solo", "organizations": {"nodes": []}}}`), resp)
	}
	return json.Unmarshal([]byte(`{}`), resp)
}

func TestWizardManualFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(orglessDoer{}, nil), teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	wait("Describe the team") // no orgs → straight to the manual form
	tm.Type("myspace")
	tm.Send(enter) // → members field
	tm.Type("alice, bob")
	tm.Send(enter) // → repos field
	tm.Type("acme/api charm/tea")
	tm.Send(enter) // submit → review
	wait("alice")
	tm.Send(enter) // accept all → name step
	wait("profile")
	tm.Send(enter) // default name = org ("myspace")

	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if !ok {
		t.Fatal("unexpected final model type")
	}
	if final.Result == nil {
		t.Fatalf("wizard returned no team (err=%v)", final.err)
	}
	team := *final.Result
	if team.Name != "myspace" || team.Org != "myspace" || team.GHTeamSlug != "" {
		t.Errorf("name/org/slug = %q/%q/%q", team.Name, team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 2 || team.Members[0].Login != "alice" || team.Members[1].Login != "bob" {
		t.Errorf("members = %+v", team.Members)
	}
	if len(team.Repos) != 2 || team.Repos[0].String() != "acme/api" || team.Repos[1].String() != "charm/tea" {
		t.Errorf("repos = %+v", team.Repos)
	}
}

func TestWizardFullFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(scriptedDoer{}, []string{"platform"}),
		teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	// Wait on strings unique to each step: the diff renderer may split
	// static prefixes across frames, so shared words never re-appear whole.
	wait("acme")
	tm.Send(enter) // acme
	wait("Platform Eng")
	tm.Send(enter) // platform
	wait("alice")
	// Exclude bob: move down once, toggle.
	tm.Send(tea.KeyPressMsg{Code: 'j', Text: "j"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	tm.Send(enter)
	wait("profile")
	tm.Send(enter) // accept suggested unique name

	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if !ok {
		t.Fatal("unexpected final model type")
	}
	if final.Result == nil {
		t.Fatalf("wizard returned no team (err=%v)", final.err)
	}
	team := *final.Result
	if team.Name != "platform-2" { // "platform" already existed
		t.Errorf("name = %q, want platform-2", team.Name)
	}
	if team.Org != "acme" || team.GHTeamSlug != "platform" {
		t.Errorf("org/slug = %q/%q", team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 1 || team.Members[0].Login != "alice" {
		t.Errorf("members = %+v, want just alice (bob toggled off)", team.Members)
	}
	if len(team.Repos) != 1 || team.Repos[0].Name != "api" {
		t.Errorf("repos = %+v, want just acme/api (legacy is archived)", team.Repos)
	}
}
